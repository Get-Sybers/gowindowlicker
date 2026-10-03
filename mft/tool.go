// gomft — Linux-native Windows $MFT parser for the DX_DFIR pipeline.
//
// Parses a raw $MFT file with Velociraptor's go-ntfs and emits one record per
// MFT entry — entry/sequence, parent reference, file name + extension, size,
// the $STANDARD_INFORMATION (0x10) and
// $FILE_NAME (0x30) MACB timestamps, flags, ADS — as JSONL or CSV. It runs on
// Linux with no .NET, no shell and no libc, matching the get-sybers hardening
// contract of the other GoDFIR tools.
//
// A $MFT record begins with the "FILE" signature, so `-d` finds the table by
// header regardless of name — a raw-mount "$MFT" and Plaso image_export's
// rename ("$" → "_", i.e. "_MFT") both parse.
//
// `--tar` reads a tar archive on stdin instead of a path — one entry per file,
// as `gomount stream` emits — and parses every entry carrying the FILE
// signature, so `gomount stream --filter '$MFT' <image> | gowindowlicker gomft --tar` parses an
// NTFS image's $MFT without a mount. A tar entry is sequential, so each candidate
// is buffered whole into memory before parsing (go-ntfs needs random access).
//
// Fields go-ntfs does not expose are not emitted (ReparseTarget, SecurityId,
// ObjectId, ZoneId) — never faked. Timestamps are RFC3339 (UTC), omitted when
// zero. $MFT-only parsing resolves paths and resident data; a non-resident
// $DATA run length beyond the record is not followed (there is no volume here).
//
// With no arguments the binary runs the container-framework batch mode (the shared
// batch package): it reads GOMFT_INPUT_DIR / GOMFT_OUT_DIR / GOMFT_FORCE /
// GOMFT_FORMAT, content-detects every $MFT file under the input tree, writes
// one output folder per $MFT file (the input item) and prints one JSON summary
// line. The argv flags below are the debug pass-through.
//
// argv exit codes: 0 = every $MFT parsed; 1 = usage or fatal error; 2 = at
// least one file failed to parse (failures listed on stderr, the rest still
// emitted). Batch mode uses the uniform 0/1/2/3 table.
package mft

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/Get-Sybers/gopinfo/tstamp"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	batch "github.com/Get-Sybers/gopinfo/framework"

	ntfs "www.velocidex.com/golang/go-ntfs/parser"
)

// record is the emitted per-entry JSON object (the columns go-ntfs can
// supply). Zero timestamps are omitted rather than emitted as a fake epoch.
type record struct {
	EntryNumber           int64  `json:"EntryNumber"`
	SequenceNumber        uint16 `json:"SequenceNumber"`
	InUse                 bool   `json:"InUse"`
	ParentEntryNumber     uint64 `json:"ParentEntryNumber"`
	ParentSequenceNumber  uint16 `json:"ParentSequenceNumber"`
	ParentPath            string `json:"ParentPath,omitempty"`
	FileName              string `json:"FileName"`
	Extension             string `json:"Extension,omitempty"`
	FileSize              int64  `json:"FileSize"`
	ReferenceCount        int64  `json:"ReferenceCount"`
	IsDirectory           bool   `json:"IsDirectory"`
	HasAds                bool   `json:"HasAds"`
	NameType              string `json:"NameType,omitempty"`
	SiFlags               string `json:"SiFlags,omitempty"`
	SILtFN                bool   `json:"SI<FN"`
	USecZeros             bool   `json:"uSecZeros"`
	LogfileSequenceNumber uint64 `json:"LogfileSequenceNumber"`
	Created0x10           string `json:"Created0x10,omitempty"`
	Created0x30           string `json:"Created0x30,omitempty"`
	LastModified0x10      string `json:"LastModified0x10,omitempty"`
	LastModified0x30      string `json:"LastModified0x30,omitempty"`
	LastRecordChange0x10  string `json:"LastRecordChange0x10,omitempty"`
	LastRecordChange0x30  string `json:"LastRecordChange0x30,omitempty"`
	LastAccess0x10        string `json:"LastAccess0x10,omitempty"`
	LastAccess0x30        string `json:"LastAccess0x30,omitempty"`
}

var csvHeader = []string{
	"EntryNumber", "SequenceNumber", "InUse", "ParentEntryNumber", "ParentSequenceNumber",
	"ParentPath", "FileName", "Extension", "FileSize", "ReferenceCount", "IsDirectory",
	"HasAds", "NameType", "SiFlags", "SI<FN", "uSecZeros", "LogfileSequenceNumber",
	"Created0x10", "Created0x30", "LastModified0x10", "LastModified0x30",
	"LastRecordChange0x10", "LastRecordChange0x30", "LastAccess0x10", "LastAccess0x30",
}

func (r *record) csvRow() []string {
	return []string{
		strconv.FormatInt(r.EntryNumber, 10), strconv.FormatUint(uint64(r.SequenceNumber), 10),
		strconv.FormatBool(r.InUse), strconv.FormatUint(r.ParentEntryNumber, 10),
		strconv.FormatUint(uint64(r.ParentSequenceNumber), 10), r.ParentPath, r.FileName,
		r.Extension, strconv.FormatInt(r.FileSize, 10), strconv.FormatInt(r.ReferenceCount, 10),
		strconv.FormatBool(r.IsDirectory), strconv.FormatBool(r.HasAds), r.NameType, r.SiFlags,
		strconv.FormatBool(r.SILtFN), strconv.FormatBool(r.USecZeros),
		strconv.FormatUint(r.LogfileSequenceNumber, 10),
		r.Created0x10, r.Created0x30, r.LastModified0x10, r.LastModified0x30,
		r.LastRecordChange0x10, r.LastRecordChange0x30, r.LastAccess0x10, r.LastAccess0x30,
	}
}

// ts renders an MFT timestamp as RFC3339 UTC, or "" when zero (a null time is
// honest; a fabricated epoch is not).

func highlightToRecord(h *ntfs.MFTHighlight) *record {
	name := h.FileName()
	parent := ""
	if full := h.FullPath(); full != "" {
		parent = path.Dir(full)
	}
	return &record{
		EntryNumber:           h.EntryNumber,
		SequenceNumber:        h.SequenceNumber,
		InUse:                 h.InUse,
		ParentEntryNumber:     h.ParentEntryNumber,
		ParentSequenceNumber:  h.ParentSequenceNumber,
		ParentPath:            parent,
		FileName:              name,
		Extension:             strings.ToLower(filepath.Ext(name)),
		FileSize:              h.FileSize,
		ReferenceCount:        h.ReferenceCount,
		IsDirectory:           h.IsDir,
		HasAds:                h.HasADS,
		NameType:              h.FileNameTypes(),
		SiFlags:               h.SIFlags,
		SILtFN:                h.SI_Lt_FN,
		USecZeros:             h.USecZeros,
		LogfileSequenceNumber: h.LogFileSeqNum,
		Created0x10:           tstamp.RFC3339Nano(h.Created0x10),
		Created0x30:           tstamp.RFC3339Nano(h.Created0x30),
		LastModified0x10:      tstamp.RFC3339Nano(h.LastModified0x10),
		LastModified0x30:      tstamp.RFC3339Nano(h.LastModified0x30),
		LastRecordChange0x10:  tstamp.RFC3339Nano(h.LastRecordChange0x10),
		LastRecordChange0x30:  tstamp.RFC3339Nano(h.LastRecordChange0x30),
		LastAccess0x10:        tstamp.RFC3339Nano(h.LastAccess0x10),
		LastAccess0x30:        tstamp.RFC3339Nano(h.LastAccess0x30),
	}
}

// emitter writes one record, JSONL or CSV.
type emitter struct {
	enc *json.Encoder
	cw  *csv.Writer
}

func (e *emitter) emit(r *record) error {
	if e.cw != nil {
		return e.cw.Write(r.csvRow())
	}
	return e.enc.Encode(r)
}

// parseFile streams every MFT entry in `path` through `emit`, returning the
// number of entries emitted. record_size/cluster_size are the standard NTFS
// values (a raw $MFT has 1024-byte records); expose them for the rare volume
// with a different geometry.
func parseFile(p string, recordSize, clusterSize int64, e *emitter) (int, error) {
	f, err := os.Open(p)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return parseReaderAt(f, st.Size(), recordSize, clusterSize, e)
}

// parseReaderAt streams every MFT entry in a $MFT of the given byte size through
// `emit`, returning the number of entries emitted. go-ntfs needs random access,
// so the source is an io.ReaderAt — an *os.File for -f/-d, a *bytes.Reader for a
// buffered tar entry under --tar.
func parseReaderAt(r io.ReaderAt, size, recordSize, clusterSize int64, e *emitter) (int, error) {
	n := 0
	ch := ntfs.ParseMFTFile(context.Background(), r, size, clusterSize, recordSize)
	for h := range ch {
		if err := e.emit(highlightToRecord(h)); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

const mftMagic = "FILE"

// hasMFTMagic reports whether b begins with the "FILE" record signature every
// $MFT starts with — the content test both -d (on a file header) and --tar (on a
// buffered tar entry) pick the table out by.
func hasMFTMagic(b []byte) bool {
	return len(b) >= len(mftMagic) && string(b[:len(mftMagic)]) == mftMagic
}

// looksLikeMFT peeks the "FILE" record signature — a $MFT (raw "$MFT" or Plaso's
// renamed "_MFT") begins with it, letting -d find the table by content.
func looksLikeMFT(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	var hdr [4]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return false
	}
	return hasMFTMagic(hdr[:])
}

// parseTarStream reads a TAR archive from r — one entry per file, as
// `gomount stream` emits (entry name = the file's volume path, body = its bytes)
// — and parses every regular-file entry that carries the "FILE" $MFT signature,
// streaming its entries through `emit`. It returns the number of $MFT files
// parsed, the total entries emitted, and the number of per-file failures: a read
// or parse error on one entry is counted and skipped, and the stream keeps going.
// err is non-nil only for a fatal condition — a corrupt tar, or an emit that
// itself failed.
//
// go-ntfs's ParseMFTFile wants random access (io.ReaderAt + size), but a tar
// entry is sequential, so each candidate entry is buffered whole into a
// bytes.Reader before parsing. A raw $MFT can be large (~128 MB), so this one
// entry is the tool's heaviest buffer — one at a time, never the whole tar.
func parseTarStream(r io.Reader, recordSize, clusterSize int64, e *emitter) (parsed, entries, failed int, err error) {
	tr := tar.NewReader(r)
	for {
		hdr, nextErr := tr.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			// A malformed tar desynchronises every entry after it — fatal.
			return parsed, entries, failed, fmt.Errorf("read tar: %w", nextErr)
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			continue // only regular files carry $MFT bytes
		}
		buf, readErr := io.ReadAll(tr)
		if readErr != nil {
			failed++
			fmt.Fprintf(os.Stderr, "gomft: FAILED %s: read: %v\n", hdr.Name, readErr)
			continue
		}
		if !hasMFTMagic(buf) {
			continue // pick the $MFT out of the stream by its FILE signature
		}
		n, parseErr := parseReaderAt(bytes.NewReader(buf), int64(len(buf)), recordSize, clusterSize, e)
		if parseErr != nil {
			failed++
			fmt.Fprintf(os.Stderr, "gomft: FAILED %s: %v\n", hdr.Name, parseErr)
			continue
		}
		parsed++
		entries += n
	}
	return parsed, entries, failed, nil
}

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
			fmt.Fprintf(os.Stderr, "gomft: skipping unreadable %s: %v\n", p, err)
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

// Tool binds this parser to the shared batch runtime.
var Tool = batch.Tool{
	Name:     "gomft",
	Formats:  []string{"json", "csv"},
	Discover: batchDiscover,
	Process:  batchProcess,
}

// batchDiscover walks the input tree and keeps every file that carries the
// FILE record signature — the same content test -d applies.
func batchDiscover(cfg *batch.Config) ([]string, error) {
	files, err := collectInputs("", cfg.InputDir)
	if err != nil {
		return nil, err
	}
	var items []string
	for _, p := range files {
		if looksLikeMFT(p) {
			items = append(items, p)
		}
	}
	return items, nil
}

// batchEmitter builds the record emitter over w for the configured format:
// JSONL, or CSV with its header row. The returned flush commits buffered CSV.
func batchEmitter(w io.Writer, format string) (*emitter, func() error, error) {
	if format == "csv" {
		cw := csv.NewWriter(w)
		if err := cw.Write(csvHeader); err != nil {
			return nil, nil, err
		}
		return &emitter{cw: cw}, func() error { cw.Flush(); return cw.Error() }, nil
	}
	return &emitter{enc: json.NewEncoder(w)}, func() error { return nil }, nil
}

// batchProcess parses one $MFT into its record file with the standard NTFS
// geometry (1024-byte records, 4096-byte clusters).
func batchProcess(cfg *batch.Config, item, _ string, w io.Writer) (int, error) {
	e, flush, err := batchEmitter(w, cfg.Format)
	if err != nil {
		return 0, err
	}
	n, err := parseFile(item, 1024, 4096, e)
	if err != nil {
		return n, err
	}
	return n, flush()
}

func Main(version, contractYML string) {
	batch.Entry(Tool, batch.Options{Version: version, Contract: contractYML})
	var (
		file    = flag.String("f", "", "single $MFT file to parse")
		dir     = flag.String("d", "", "directory to scan recursively for a $MFT (by FILE signature)")
		tarMode = flag.Bool("tar", false, "read a tar archive on stdin and parse each $MFT entry (by FILE signature), as `gomount stream` emits")
		jsonDir = flag.String("json", "", "directory to write JSONL output to (default: stdout)")
		jsonF   = flag.String("jsonf", "", "JSONL file name (default: MFTECmd_Output.jsonl)")
		csvDir  = flag.String("csv", "", "directory to write CSV output to instead of JSONL")
		csvF    = flag.String("csvf", "", "CSV file name (default: MFTECmd_Output.csv)")
		recSize = flag.Int64("record-size", 1024, "MFT record size in bytes")
		cluSize = flag.Int64("cluster-size", 4096, "volume cluster size in bytes")
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
		fmt.Fprintln(os.Stderr, "gomft: exactly one of -f <file>, -d <dir> or --tar is required")
		flag.Usage()
		os.Exit(1)
	}

	dirMode := *dir != ""

	var (
		w   io.WriteCloser
		err error
	)
	e := &emitter{}
	if *csvDir != "" {
		w, err = openOut(*csvDir, *csvF, "MFTECmd_Output.csv")
	} else {
		w, err = openOut(*jsonDir, *jsonF, "MFTECmd_Output.jsonl")
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "gomft: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		if w != os.Stdout {
			w.Close()
		}
	}()
	if *csvDir != "" {
		e.cw = csv.NewWriter(w)
		if err := e.cw.Write(csvHeader); err != nil {
			fmt.Fprintf(os.Stderr, "gomft: write: %v\n", err)
			os.Exit(1)
		}
	} else {
		e.enc = json.NewEncoder(w)
	}

	failed, parsed, entries := 0, 0, 0
	if *tarMode {
		var tarErr error
		parsed, entries, failed, tarErr = parseTarStream(os.Stdin, *recSize, *cluSize, e)
		if tarErr != nil {
			fmt.Fprintf(os.Stderr, "gomft: %v\n", tarErr)
			os.Exit(1)
		}
	} else {
		inputs, cerr := collectInputs(*file, *dir)
		if cerr != nil {
			fmt.Fprintf(os.Stderr, "gomft: %v\n", cerr)
			os.Exit(1)
		}
		if len(inputs) == 0 {
			fmt.Fprintln(os.Stderr, "gomft: no files found")
			os.Exit(1)
		}
		for _, p := range inputs {
			if dirMode && !looksLikeMFT(p) {
				continue // -d: pick the $MFT out of the tree by its FILE signature
			}
			n, err := parseFile(p, *recSize, *cluSize, e)
			if err != nil {
				failed++
				fmt.Fprintf(os.Stderr, "gomft: FAILED %s: %v\n", p, err)
				continue
			}
			parsed++
			entries += n
			if !*quiet {
				fmt.Fprintf(os.Stderr, "gomft: parsed %s (%d entries)\n", p, n)
			}
		}
	}
	if e.cw != nil {
		e.cw.Flush()
		if err := e.cw.Error(); err != nil {
			fmt.Fprintf(os.Stderr, "gomft: write: %v\n", err)
			os.Exit(1)
		}
	}
	if dirMode && parsed == 0 && failed == 0 {
		fmt.Fprintf(os.Stderr, "gomft: no $MFT found under %s\n", *dir)
		os.Exit(1)
	}
	if *tarMode && parsed == 0 && failed == 0 {
		fmt.Fprintln(os.Stderr, "gomft: no $MFT found in the tar stream")
		os.Exit(1)
	}
	if !*quiet {
		fmt.Fprintf(os.Stderr, "gomft: %d entries across %d $MFT file(s)\n", entries, parsed)
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "gomft: %d file(s) failed to parse\n", failed)
		os.Exit(2)
	}
}
