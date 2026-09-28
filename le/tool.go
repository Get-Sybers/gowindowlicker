// gole — Linux-native Windows .lnk (shortcut) parser for the DX_DFIR pipeline.
//
// Parses .lnk shell-link files with parsiya/golnk and emits JSON columns
// (source mtime/atime,
// target MACB times, target path, working dir, arguments, name, relative path,
// file size, header/attribute flags). It runs on Linux with no .NET, no shell
// and no libc (FROM scratch, uid 2000), matching the get-sybers hardening
// contract. A source birth time (SourceCreated) is not exposed by the Go
// stdlib on Linux, so that column is dropped rather than emitted always-empty.
//
// Fields golnk does not resolve (a fully-walked TargetIDAbsolutePath from the
// ID list, MFT entry/sequence, tracker MAC) are not emitted — never faked; the
// target path is LinkInfo's LocalBasePath(+CommonPathSuffix), which is what the
// artefact records directly.
//
// Input is either a single file (-f), a directory walked recursively for .lnk
// (-d), or a TAR archive on stdin (--tar) as `gomount stream` emits — one entry
// per file, entry name = the file's volume path, body = the file's bytes. In
// --tar mode SourceModified comes from the tar header's mtime; a tar header
// carries no atime, so SourceAccessed is empty there.
//
// With no arguments the binary runs the container-framework batch mode (the shared
// batch package): it reads GOLE_INPUT_DIR / GOLE_OUT_DIR / GOLE_FORCE / GOLE_FORMAT,
// finds every .lnk under the input tree, writes one output folder per shortcut
// and prints one JSON summary line. The argv flags below are the debug
// pass-through.
//
// argv exit codes: 0 = every file parsed; 1 = usage or fatal error; 2 = at
// least one file failed (failures listed on stderr, the rest still emitted).
// Batch mode uses the uniform 0/1/2/3 table.
package le

import (
	"archive/tar"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/get-sybers/gopinfo/tstamp"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	batch "github.com/get-sybers/gopinfo/framework"

	lnk "github.com/parsiya/golnk"
)

type record struct {
	SourceFile string `json:"SourceFile"`
	// SourceCreated (the .lnk's own birth time) is deliberately absent: Linux
	// does not expose a file birth time through the Go stdlib, so a
	// SourceCreated column can never be populated here — we drop it rather than
	// emit an always-empty field. SourceModified/SourceAccessed come from the
	// .lnk file's own mtime/atime.
	SourceModified       string `json:"SourceModified"`
	SourceAccessed       string `json:"SourceAccessed"`
	TargetCreated        string `json:"TargetCreated"`
	TargetModified       string `json:"TargetModified"`
	TargetAccessed       string `json:"TargetAccessed"`
	FileSize             uint32 `json:"FileSize"`
	Name                 string `json:"Name,omitempty"`
	RelativePath         string `json:"RelativePath,omitempty"`
	WorkingDirectory     string `json:"WorkingDirectory,omitempty"`
	Arguments            string `json:"Arguments,omitempty"`
	IconLocation         string `json:"IconLocation,omitempty"`
	LocalPath            string `json:"LocalPath,omitempty"`
	CommonPath           string `json:"CommonPath,omitempty"`
	TargetIDAbsolutePath string `json:"TargetIDAbsolutePath,omitempty"`
	HeaderFlags          string `json:"HeaderFlags,omitempty"`
	FileAttributes       string `json:"FileAttributes,omitempty"`
}

var csvHeader = []string{
	"SourceFile", "SourceModified", "SourceAccessed",
	"TargetCreated", "TargetModified", "TargetAccessed", "FileSize", "Name",
	"RelativePath", "WorkingDirectory", "Arguments", "IconLocation", "LocalPath",
	"CommonPath", "TargetIDAbsolutePath", "HeaderFlags", "FileAttributes",
}

func (r *record) csvRow() []string {
	return []string{r.SourceFile, r.SourceModified, r.SourceAccessed,
		r.TargetCreated, r.TargetModified, r.TargetAccessed, strconv.FormatUint(uint64(r.FileSize), 10),
		r.Name, r.RelativePath, r.WorkingDirectory, r.Arguments, r.IconLocation, r.LocalPath,
		r.CommonPath, r.TargetIDAbsolutePath, r.HeaderFlags, r.FileAttributes}
}

// setFlags renders a golnk FlagMap (map[string]bool) as a stable, comma-joined
// list of the set flags.
func setFlags(fm lnk.FlagMap) string {
	var on []string
	for k, v := range fm {
		if v {
			on = append(on, k)
		}
	}
	sort.Strings(on)
	return strings.Join(on, ", ")
}

// recordFromLnk maps a parsed golnk LnkFile into a record. It fills everything
// that comes from the .lnk content itself — the target MACB times, path, flags —
// and leaves the Source* fs timestamps to the caller, since those come from the
// file's own metadata, which differs between an on-disk file and a tar entry.
func recordFromLnk(f lnk.LnkFile, sourceFile string) *record {
	local := f.LinkInfo.LocalBasePathUnicode
	if local == "" {
		local = f.LinkInfo.LocalBasePath
	}
	common := f.LinkInfo.CommonPathSuffixUnicode
	if common == "" {
		common = f.LinkInfo.CommonPathSuffix
	}
	return &record{
		SourceFile:       sourceFile,
		TargetCreated:    tstamp.RFC3339Nano(f.Header.CreationTime),
		TargetModified:   tstamp.RFC3339Nano(f.Header.WriteTime),
		TargetAccessed:   tstamp.RFC3339Nano(f.Header.AccessTime),
		FileSize:         f.Header.TargetFileSize,
		Name:             f.StringData.NameString,
		RelativePath:     f.StringData.RelativePath,
		WorkingDirectory: f.StringData.WorkingDir,
		Arguments:        f.StringData.CommandLineArguments,
		IconLocation:     f.StringData.IconLocation,
		LocalPath:        local,
		CommonPath:       common,
		HeaderFlags:      setFlags(f.Header.LinkFlags),
		FileAttributes:   setFlags(f.Header.FileAttributes),
	}
}

func parseOne(path string) (*record, error) {
	f, err := lnk.File(path)
	if err != nil {
		return nil, err
	}
	rec := recordFromLnk(f, path)
	// the .lnk file's own fs timestamps (the Source* columns). mtime is
	// portable; atime comes from the Linux stat_t (the container is Linux). A
	// birth time (SourceCreated) is not exposed by the Go stdlib on Linux, so
	// that column is intentionally not part of the schema — see the record type.
	if st, err := os.Stat(path); err == nil {
		rec.SourceModified = tstamp.RFC3339Nano(st.ModTime())
		if sys, ok := st.Sys().(*syscall.Stat_t); ok {
			rec.SourceAccessed = tstamp.RFC3339Nano(time.Unix(sys.Atim.Unix()))
		}
	}
	return rec, nil
}

const lnkMagic = "\x4c\x00\x00\x00" // ShellLinkHeader HeaderSize = 0x4C

// hasLnkMagic reports whether b begins with the 0x4C shell-link header, the same
// content signal looksLikeLnk uses on disk — so a tar entry with a renamed or
// missing extension is still recognised as a .lnk.
func hasLnkMagic(b []byte) bool {
	return len(b) >= len(lnkMagic) && string(b[:len(lnkMagic)]) == lnkMagic
}

func looksLikeLnk(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var hdr [4]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return false
	}
	return hasLnkMagic(hdr[:])
}

// maxTarEntry caps how many bytes of a single tar entry are buffered before the
// .lnk parse. A shell link is a few KB; the cap only bounds memory against a
// pathological oversized entry (which is not a real .lnk and fails the parse,
// counted and skipped like any other bad file).
const maxTarEntry int64 = 32 << 20 // 32 MiB

// parseTarStream reads a TAR archive from r — as `gomount stream` emits it: one
// entry per regular file, entry name = the file's volume path, body = its bytes —
// and parses each .lnk entry, selected by the .lnk extension or the 0x4C
// shell-link magic (the same content detection -d uses). emit is called once per
// parsed record. It returns the number of records emitted and the number of
// per-file failures (a read or parse failure on one entry is counted and
// skipped; the stream keeps going). err is non-nil only for a fatal condition: a
// corrupt tar, or an emit callback that itself failed.
//
// A tar header carries the entry's mtime but not its atime, so SourceModified is
// filled from the header while SourceAccessed stays empty in this mode.
func parseTarStream(r io.Reader, emit func(*record) error, quiet bool) (parsed, failed int, err error) {
	tr := tar.NewReader(r)
	for {
		hdr, nextErr := tr.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			// A malformed tar desynchronises every entry after it — fatal.
			return parsed, failed, fmt.Errorf("read tar: %w", nextErr)
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			continue // only regular files carry bytes to parse
		}
		byExt := strings.EqualFold(filepath.Ext(hdr.Name), ".lnk")
		buf, readErr := io.ReadAll(io.LimitReader(tr, maxTarEntry))
		if !byExt && !(readErr == nil && hasLnkMagic(buf)) {
			continue // not a .lnk by name or magic — skip without counting
		}
		if readErr != nil {
			failed++
			fmt.Fprintf(os.Stderr, "gole: FAILED %s: read: %v\n", hdr.Name, readErr)
			continue
		}
		f, parseErr := lnk.Read(bytes.NewReader(buf), uint64(len(buf)))
		if parseErr != nil {
			failed++
			fmt.Fprintf(os.Stderr, "gole: FAILED %s: %v\n", hdr.Name, parseErr)
			continue
		}
		rec := recordFromLnk(f, hdr.Name)
		// The tar header carries mtime (SourceModified) but no atime, so
		// SourceAccessed is left empty here rather than faked.
		rec.SourceModified = tstamp.RFC3339Nano(hdr.ModTime)
		if emitErr := emit(rec); emitErr != nil {
			return parsed, failed, emitErr
		}
		parsed++
		if !quiet {
			fmt.Fprintf(os.Stderr, "gole: parsed %s -> %s\n", hdr.Name, rec.LocalPath)
		}
	}
	return parsed, failed, nil
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
			fmt.Fprintf(os.Stderr, "gole: skipping unreadable %s: %v\n", p, err)
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() && (strings.EqualFold(filepath.Ext(p), ".lnk") || looksLikeLnk(p)) {
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
	Name:     "gole",
	Formats:  []string{"json", "csv"},
	Discover: batchDiscover,
	Process:  batchProcess,
}

// batchDiscover walks the input tree and keeps every .lnk (by extension or the
// 0x4C header) — the same selection -d applies.
func batchDiscover(cfg *batch.Config) ([]string, error) {
	return collectInputs("", cfg.InputDir)
}

// batchProcess parses one shortcut into its record file: a JSONL object, or a
// CSV header plus one row.
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

func Main(version, contractYML string) {
	batch.Entry(Tool, batch.Options{Version: version, Contract: contractYML})
	var (
		file    = flag.String("f", "", "single .lnk file to parse")
		dir     = flag.String("d", "", "directory to scan recursively for .lnk")
		tarMode = flag.Bool("tar", false, "read a TAR archive on stdin (gomount stream) and parse each .lnk entry")
		jsonDir = flag.String("json", "", "directory to write JSONL output to (default: stdout)")
		jsonF   = flag.String("jsonf", "", "JSONL file name (default: LECmd_Output.json)")
		csvDir  = flag.String("csv", "", "directory to write CSV output to instead of JSONL")
		csvF    = flag.String("csvf", "", "CSV file name (default: LECmd_Output.csv)")
		quiet   = flag.Bool("q", false, "suppress per-file progress on stderr")
	)
	flag.Parse()

	switch {
	case *tarMode:
		if *file != "" || *dir != "" {
			fmt.Fprintln(os.Stderr, "gole: --tar reads the tar stream on stdin; do not combine it with -f or -d")
			flag.Usage()
			os.Exit(1)
		}
	case (*file == "") == (*dir == ""):
		fmt.Fprintln(os.Stderr, "gole: exactly one of -f <file>, -d <dir> or --tar is required")
		flag.Usage()
		os.Exit(1)
	}

	var inputs []string
	var err error
	if !*tarMode {
		inputs, err = collectInputs(*file, *dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gole: %v\n", err)
			os.Exit(1)
		}
		if len(inputs) == 0 {
			fmt.Fprintln(os.Stderr, "gole: no .lnk files found")
			os.Exit(1)
		}
	}

	var w io.WriteCloser
	var cw *csv.Writer
	if *csvDir != "" {
		w, err = openOut(*csvDir, *csvF, "LECmd_Output.csv")
	} else {
		w, err = openOut(*jsonDir, *jsonF, "LECmd_Output.json")
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "gole: %v\n", err)
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
			fmt.Fprintf(os.Stderr, "gole: write: %v\n", err)
			os.Exit(1)
		}
	}
	enc := json.NewEncoder(w)

	// emit routes one record to the active output (CSV row or JSONL object),
	// shared by the -f/-d walk and the --tar stream.
	emit := func(rec *record) error {
		if cw != nil {
			return cw.Write(rec.csvRow())
		}
		return enc.Encode(rec)
	}

	var failed, total int
	if *tarMode {
		var parsed int
		var perr error
		parsed, failed, perr = parseTarStream(os.Stdin, emit, *quiet)
		if perr != nil {
			fmt.Fprintf(os.Stderr, "gole: %v\n", perr)
			os.Exit(1)
		}
		total = parsed + failed
	} else {
		total = len(inputs)
		for _, p := range inputs {
			rec, err := parseOne(p)
			if err != nil {
				failed++
				fmt.Fprintf(os.Stderr, "gole: FAILED %s: %v\n", p, err)
				continue
			}
			if err := emit(rec); err != nil {
				fmt.Fprintf(os.Stderr, "gole: write: %v\n", err)
				os.Exit(1)
			}
			if !*quiet {
				fmt.Fprintf(os.Stderr, "gole: parsed %s -> %s\n", p, rec.LocalPath)
			}
		}
	}
	if cw != nil {
		cw.Flush()
		if err := cw.Error(); err != nil {
			fmt.Fprintf(os.Stderr, "gole: write: %v\n", err)
			os.Exit(1)
		}
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "gole: %d of %d files failed\n", failed, total)
		os.Exit(2)
	}
}
