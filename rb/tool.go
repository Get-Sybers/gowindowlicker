// gorb — Linux-native Windows Recycle Bin ($I) parser for the DX_DFIR pipeline.
//
// Parses the modern Recycle Bin metadata files ($I<id>, one per deleted item,
// that pair with the $R<id> payload) and emits the facts — original path,
// logical size, deletion time — as CSV or JSONL. Runs on Linux with no .NET,
// no shell and no libc, matching the
// get-sybers hardening contract of the other GoDFIR tools.
//
// Two on-disk layouts are handled (both little-endian):
//   - v1 (Windows Vista–8.0): [int64 version=1][int64 size][FILETIME deleted]
//     [520 bytes UTF-16LE path, fixed 260 wchar, null-terminated] = 544 bytes.
//   - v2 (Windows 8.1/10/11):  [int64 version=2][int64 size][FILETIME deleted]
//     [uint32 nameLen (wchar, incl NUL)][nameLen*2 bytes UTF-16LE path].
//
// The legacy XP INFO2 container is NOT handled — it
// is obsolete and not in the pipeline's extraction filter; such a file is
// reported as a parse failure rather than mis-read.
//
// Output columns: SourceName, FileType, FileName, FileSize, DeletedOn.
// DeletedOn is rendered RFC3339 UTC (the pipeline consumes the field, not a
// particular locale rendering).
//
// Input is one of three modes: -f parses a single file; -d scans a directory
// recursively, picking the $I records out of the tree by header; --tar reads a
// TAR archive on stdin — the stream `gomount stream` emits, one entry per file
// with the entry name set to the file's volume path — and picks the $I records
// out of the entries by the same header check. The tar mode is a pipe over the
// $Recycle.Bin/* glob, so the entries also carry the $R payloads (whole deleted
// files) and desktop.ini; those are skipped, only the $I records are parsed:
//
//	gomount stream --filter '$Recycle.Bin/*' <image> | gowindowlicker gorb --tar --csv /output
//
// With no arguments the binary runs the container-framework batch mode (the shared
// batch package): it reads GORB_INPUT_DIR / GORB_OUT_DIR / GORB_FORCE / GORB_FORMAT,
// picks every $I record out of the input tree by header, writes one output
// folder per record and prints one JSON summary line. The argv flags below
// are the debug pass-through.
//
// argv exit codes: 0 = every file parsed; 1 = usage or fatal error; 2 = at
// least one file failed to parse (failures listed on stderr, the rest still
// emitted). Batch mode uses the uniform 0/1/2/3 table.
package rb

import (
	"archive/tar"
	"encoding/binary"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
	"unicode/utf16"

	batch "github.com/get-sybers/gopinfo/framework"
)

type record struct {
	SourceName string `json:"SourceName"`
	FileType   string `json:"FileType"`
	FileName   string `json:"FileName"`
	FileSize   int64  `json:"FileSize"`
	DeletedOn  string `json:"DeletedOn"`
}

var csvHeader = []string{"SourceName", "FileType", "FileName", "FileSize", "DeletedOn"}

func (r *record) csvRow() []string {
	return []string{r.SourceName, r.FileType, r.FileName, strconv.FormatInt(r.FileSize, 10), r.DeletedOn}
}

// Tool binds this parser to the shared batch runtime.
var Tool = batch.Tool{
	Name:     "gorb",
	Formats:  []string{"json", "csv"},
	Discover: batchDiscover,
	Process:  batchProcess,
}

// batchDiscover walks the input tree and keeps every file whose header is a $I
// record — the same content test -d applies, so $R payloads, desktop.ini and
// unrelated files are left out.
func batchDiscover(cfg *batch.Config) ([]string, error) {
	files, err := collectInputs("", cfg.InputDir)
	if err != nil {
		return nil, err
	}
	var items []string
	for _, p := range files {
		if peekLooksLikeRecord(p) {
			items = append(items, p)
		}
	}
	return items, nil
}

// batchProcess parses one $I record into its record file: a JSONL object, or
// a CSV header plus one row.
func batchProcess(cfg *batch.Config, item, _ string, w io.Writer) (int, error) {
	rec, err := parseOne(item)
	if err != nil {
		return 0, err
	}
	if cfg.Format == "csv" {
		cw := csv.NewWriter(w)
		if err := cw.Write(csvHeader); err != nil {
			return 0, err
		}
		if err := cw.Write(rec.csvRow()); err != nil {
			return 0, err
		}
		cw.Flush()
		return 1, cw.Error()
	}
	return 1, json.NewEncoder(w).Encode(rec)
}

// filetimeToTime converts a Windows FILETIME (100-ns ticks since 1601-01-01 UTC)
// to a Go UTC time. 11644473600 is the 1601→1970 epoch gap in seconds.
func filetimeToTime(ft int64) time.Time {
	const ticksPerSecond = 10_000_000
	const epochGap = 11644473600
	secs := ft/ticksPerSecond - epochGap
	nsec := (ft % ticksPerSecond) * 100
	return time.Unix(secs, nsec).UTC()
}

// utf16leString decodes a little-endian UTF-16 buffer up to the first NUL, or
// the whole buffer if unterminated.
func utf16leString(b []byte) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		c := binary.LittleEndian.Uint16(b[i : i+2])
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	return string(utf16.Decode(u))
}

func parseOne(path string) (*record, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseBytes(path, data)
}

// looksLikeRecord is a cheap header check: a $I record opens with a version
// dword of 1 or 2. The -d scan uses it to pick $I metadata records out of a
// tree by CONTENT, not filename — the pipeline feeds files that Plaso's
// image_export has renamed ($ -> _, so "$IXXXX" arrives as "_IXXXX"), and a
// raw mount keeps "$IXXXX"; matching on the header finds both (and skips
// desktop.ini, the $R payloads, and everything else) regardless of name.
func looksLikeRecord(data []byte) bool {
	if len(data) < 24 {
		return false
	}
	v := int64(binary.LittleEndian.Uint64(data[0:8]))
	return v == 1 || v == 2
}

// peekLooksLikeRecord reads just the 24-byte header so a large unrelated file in
// the scanned tree is not slurped whole just to reject it.
func peekLooksLikeRecord(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var hdr [24]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return false
	}
	return looksLikeRecord(hdr[:])
}

func parseBytes(path string, data []byte) (*record, error) {
	if len(data) < 24 {
		return nil, fmt.Errorf("too small (%d bytes) to be a $I record", len(data))
	}
	version := int64(binary.LittleEndian.Uint64(data[0:8]))
	size := int64(binary.LittleEndian.Uint64(data[8:16]))
	deleted := int64(binary.LittleEndian.Uint64(data[16:24]))

	var name string
	switch version {
	case 1:
		// fixed 260-wchar (520-byte) path field
		if len(data) < 24+520 {
			return nil, fmt.Errorf("v1 $I truncated: %d bytes, want >= 544", len(data))
		}
		name = utf16leString(data[24 : 24+520])
	case 2:
		if len(data) < 28 {
			return nil, fmt.Errorf("v2 $I truncated: %d bytes, want >= 28", len(data))
		}
		nameLen := int(binary.LittleEndian.Uint32(data[24:28])) // wchar count incl NUL
		want := 28 + nameLen*2
		if nameLen <= 0 || len(data) < want {
			return nil, fmt.Errorf("v2 $I name length %d overruns %d-byte file", nameLen, len(data))
		}
		name = utf16leString(data[28:want])
	default:
		return nil, fmt.Errorf("unknown $I version %d (only 1 and 2 are supported)", version)
	}

	return &record{
		SourceName: path,
		FileType:   "$I",
		FileName:   name,
		FileSize:   size,
		DeletedOn:  filetimeToTime(deleted).Format(time.RFC3339),
	}, nil
}

// maxRecordBytes bounds how much of a tar entry gorb reads once its 24-byte
// header marks it as a $I record. A real $I is tiny (v1 is 544 bytes; a v2 is 28
// bytes plus its UTF-16 path), so this cap is never reached by a genuine record —
// it only stops a large unrelated entry that happens to open with a 1-or-2
// version dword from being read whole.
const maxRecordBytes = 1 << 20 // 1 MiB

// parseTarStream reads a TAR archive from r — the stream `gomount stream` emits,
// one regular-file entry per file with the entry name set to the file's volume
// path — and applies the $I parse to each entry, calling emit once per parsed
// record. It returns the number of records emitted and the number of files that
// carried a $I header but failed to parse (counted and skipped — the stream keeps
// going). err is non-nil only for a fatal condition: a corrupt tar, or an emit
// callback that itself failed.
//
// Records are content-detected by header, exactly as the -d scan is: a
// $Recycle.Bin/* glob carries the $R payloads (whole deleted files, large) and
// desktop.ini alongside the $I files, and only entries whose 24-byte header is a
// $I record are read whole and parsed. Peeking the header first keeps the large
// $R payloads from being read into memory just to reject them.
func parseTarStream(r io.Reader, emit func(*record) error) (emitted, failed int, err error) {
	tr := tar.NewReader(r)
	for {
		hdr, nextErr := tr.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			// A malformed tar desynchronises every entry after it — fatal.
			return emitted, failed, fmt.Errorf("read tar: %w", nextErr)
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			continue // only regular files carry bytes to parse
		}

		// Peek the 24-byte header and skip anything that is not a $I record before
		// reading its body — mirrors the -d scan's peekLooksLikeRecord.
		var head [24]byte
		n, _ := io.ReadFull(tr, head[:])
		if !looksLikeRecord(head[:n]) {
			continue
		}
		rest, readErr := io.ReadAll(io.LimitReader(tr, maxRecordBytes))
		if readErr != nil {
			failed++
			fmt.Fprintf(os.Stderr, "gorb: FAILED %s: read: %v\n", hdr.Name, readErr)
			continue
		}
		data := append(head[:n:n], rest...)

		rec, perr := parseBytes(hdr.Name, data)
		if perr != nil {
			failed++
			fmt.Fprintf(os.Stderr, "gorb: FAILED %s: %v\n", hdr.Name, perr)
			continue
		}
		if emitErr := emit(rec); emitErr != nil {
			return emitted, failed, emitErr
		}
		emitted++
	}
	return emitted, failed, nil
}

// collectInputs returns the candidate files: a single -f file, or every regular
// file under -d. The -d set is NOT filtered by name here — the caller picks the
// real $I records out by header (looksLikeRecord), because the pipeline feeds
// Plaso-renamed files ($ -> _). Unreadable subtrees under -d are skipped with a
// note; an unreadable root is fatal.
func collectInputs(file, dir string) ([]string, error) {
	if file != "" {
		return []string{file}, nil
	}
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir {
				return err
			}
			fmt.Fprintf(os.Stderr, "gorb: skipping unreadable %s: %v\n", p, err)
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	sort.Strings(out)
	return out, err
}

func openOut(dir, name, defName string) (io.WriteCloser, error) {
	if dir == "" {
		return os.Stdout, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if name == "" {
		name = defName
	}
	return os.Create(filepath.Join(dir, name))
}

func Main(version, contractYML string) {
	batch.Entry(Tool, batch.Options{Version: version, Contract: contractYML})
	var (
		file    = flag.String("f", "", "single $I file to parse")
		dir     = flag.String("d", "", "directory to scan recursively for $I* files")
		tarMode = flag.Bool("tar", false, "read a tar archive on stdin (the stream 'gomount stream' emits) and parse each $I entry")
		jsonDir = flag.String("json", "", "directory to write JSONL output to (default: stdout)")
		jsonF   = flag.String("jsonf", "", "JSONL file name (default: RBCmd_Output.jsonl)")
		csvDir  = flag.String("csv", "", "directory to write CSV output to instead of JSONL")
		csvF    = flag.String("csvf", "", "CSV file name (default: RBCmd_Output.csv)")
		quiet   = flag.Bool("q", false, "suppress per-file progress on stderr")
	)
	flag.Parse()

	modes := 0
	for _, on := range []bool{*file != "", *dir != "", *tarMode} {
		if on {
			modes++
		}
	}
	if modes != 1 {
		fmt.Fprintln(os.Stderr, "gorb: exactly one of -f <file>, -d <dir> or --tar is required")
		flag.Usage()
		os.Exit(1)
	}

	dirMode := *dir != ""
	var inputs []string
	if !*tarMode {
		var err error
		inputs, err = collectInputs(*file, *dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gorb: %v\n", err)
			os.Exit(1)
		}
		if len(inputs) == 0 {
			fmt.Fprintln(os.Stderr, "gorb: no files found")
			os.Exit(1)
		}
	}

	var w io.WriteCloser
	var cw *csv.Writer
	var err error
	if *csvDir != "" {
		w, err = openOut(*csvDir, *csvF, "RBCmd_Output.csv")
	} else {
		w, err = openOut(*jsonDir, *jsonF, "RBCmd_Output.jsonl")
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "gorb: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		if w != os.Stdout {
			w.Close()
		}
	}()

	if *csvDir != "" {
		cw = csv.NewWriter(w)
		if err := cw.Write(csvHeader); err != nil {
			fmt.Fprintf(os.Stderr, "gorb: write: %v\n", err)
			os.Exit(1)
		}
	}

	enc := json.NewEncoder(w)
	failed := 0
	emitted := 0

	// emit writes one parsed record on the chosen output (CSV or JSONL) and logs
	// its progress. A write failure is returned so the caller can stop the run.
	// Both the -f/-d loop and the --tar stream go through here.
	emit := func(rec *record) error {
		if cw != nil {
			if err := cw.Write(rec.csvRow()); err != nil {
				return err
			}
		} else if err := enc.Encode(rec); err != nil {
			return err
		}
		if !*quiet {
			fmt.Fprintf(os.Stderr, "gorb: parsed %s (%s, %d bytes)\n", rec.SourceName, rec.FileName, rec.FileSize)
		}
		return nil
	}

	if *tarMode {
		// The tar stream picks the $I records out of its entries by header, the
		// same way the -d scan does — the $Recycle.Bin/* glob carries the $R
		// payloads and desktop.ini too, and those are skipped. parseTarStream
		// owns the emitted/failed counts.
		emitted, failed, err = parseTarStream(os.Stdin, emit)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gorb: %v\n", err)
			os.Exit(1)
		}
	} else {
		for _, p := range inputs {
			// Directory scan: pick $I records out by header, silently skipping the
			// other files in the tree (desktop.ini, $R payloads, unrelated files) —
			// so a plaso-renamed "_IXXXX" or a raw-mount "$IXXXX" both parse and a
			// mis-parse of a non-$I file is never reported. -f always parses.
			if dirMode && !peekLooksLikeRecord(p) {
				continue
			}
			rec, err := parseOne(p)
			if err != nil {
				failed++
				fmt.Fprintf(os.Stderr, "gorb: FAILED %s: %v\n", p, err)
				continue
			}
			if err := emit(rec); err != nil {
				fmt.Fprintf(os.Stderr, "gorb: write: %v\n", err)
				os.Exit(1)
			}
			emitted++
		}
	}
	if cw != nil {
		cw.Flush()
		if err := cw.Error(); err != nil {
			fmt.Fprintf(os.Stderr, "gorb: write: %v\n", err)
			os.Exit(1)
		}
	}
	// A directory or tar scan that matched no $I records is worth flagging (an
	// empty Recycle Bin, a wrong -d, or a filter that caught no $I entries) but is
	// not an error on its own.
	if (dirMode || *tarMode) && emitted == 0 && failed == 0 {
		where := *dir
		if *tarMode {
			where = "the tar stream"
		}
		fmt.Fprintf(os.Stderr, "gorb: no $I records found under %s\n", where)
		os.Exit(1)
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "gorb: %d $I record(s) failed to parse\n", failed)
		os.Exit(2)
	}
}
