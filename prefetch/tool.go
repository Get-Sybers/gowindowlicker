// goprefetch — Linux-native Windows Prefetch parser for the DX_DFIR pipeline.
//
// Uses Velociraptor's go-prefetch, which carries a pure-Go LZXpress-Huffman
// implementation, so XP-era through Win11 prefetch — MAM compressed included —
// parse natively on Linux.
//
// Output is JSONL (one object per .pf) or CSV. Field notes:
// Executable/RunCount/LastRun/PreviousRunN/FilesAccessed/Hash/Version carry
// the same artifact facts; volume info blocks are not emitted (not exposed by
// go-prefetch). SourceFilename and SourceModified come from the input file.
//
// Inputs: -f a single file, -d a directory tree, or --tar a TAR archive on
// stdin. --tar consumes `gomount stream` — one tar entry per regular file, entry
// name = the file's volume path, body = the file's bytes — and parses every
// *.pf entry, so a disk image is processed by a plain pipe with no mount:
//
//	gomount stream --filter '*.pf' disk.E01 | gowindowlicker goprefetch --tar
//
// With no arguments the binary runs the container-framework batch mode (the shared
// batch package): it reads GOPREFETCH_INPUT_DIR / GOPREFETCH_OUT_DIR /
// GOPREFETCH_FORCE / GOPREFETCH_FORMAT, finds every *.pf under the input tree,
// writes one output folder per file and prints one JSON summary line. The
// argv flags below are the debug pass-through.
//
// argv exit codes: 0 = every file parsed; 1 = usage or fatal error; 2 = at
// least one file failed to parse (failures listed on stderr, the rest still
// emitted). Batch mode uses the uniform 0/1/2/3 table.
package prefetch

import (
	"archive/tar"
	"bytes"
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
	"strings"
	"time"

	batch "github.com/Get-Sybers/gopinfo/framework"

	prefetch "www.velocidex.com/golang/go-prefetch"
)

type record struct {
	SourceFilename string   `json:"SourceFilename"`
	SourceModified string   `json:"SourceModified"`
	Executable     string   `json:"Executable"`
	Path           string   `json:"Path,omitempty"`
	Hash           string   `json:"Hash"`
	Version        string   `json:"Version"`
	FileSize       uint32   `json:"FileSize"`
	RunCount       uint32   `json:"RunCount"`
	LastRun        string   `json:"LastRun,omitempty"`
	PreviousRuns   []string `json:"PreviousRuns,omitempty"`
	FilesAccessed  []string `json:"FilesAccessed"`
}

var csvHeader = []string{"SourceFilename", "SourceModified", "Executable", "Path",
	"Hash", "Version", "FileSize", "RunCount", "LastRun", "PreviousRuns", "FilesAccessed"}

func (r *record) csvRow() []string {
	return []string{r.SourceFilename, r.SourceModified, r.Executable,
		r.Path, r.Hash, r.Version, strconv.FormatUint(uint64(r.FileSize), 10),
		strconv.FormatUint(uint64(r.RunCount), 10), r.LastRun,
		strings.Join(r.PreviousRuns, "|"), strings.Join(r.FilesAccessed, "|")}
}

// Tool binds this parser to the shared batch runtime.
var Tool = batch.Tool{
	Name:     "goprefetch",
	Formats:  []string{"json", "csv"},
	Discover: batchDiscover,
	Process:  batchProcess,
}

// batchDiscover walks the input tree and keeps every *.pf — the same selection
// -d applies.
func batchDiscover(cfg *batch.Config) ([]string, error) {
	return collectInputs("", cfg.InputDir)
}

// batchProcess parses one prefetch file into its record file: a JSONL object,
// or a CSV header plus one row.
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

// parseReader parses one prefetch file from r, tagging the record with name as
// its SourceFilename and mod as its SourceModified (mod is dropped when zero).
// go-prefetch reads via io.ReaderAt, so callers with a sequential stream (the
// --tar path) buffer the entry into a bytes.Reader first — prefetch files are
// small, so the whole file lives in memory anyway.
func parseReader(r io.ReaderAt, name string, mod time.Time) (*record, error) {
	info, err := prefetch.LoadPrefetch(r)
	if err != nil {
		return nil, err
	}

	rec := &record{
		SourceFilename: name,
		Executable:     info.Executable,
		Path:           info.Path,
		Hash:           info.Hash,
		Version:        info.Version,
		FileSize:       info.FileSize,
		RunCount:       info.RunCount,
		FilesAccessed:  info.FilesAccessed,
	}
	if !mod.IsZero() {
		rec.SourceModified = mod.UTC().Format(time.RFC3339)
	}

	// go-prefetch returns run times newest-first for Win8+; normalise anyway.
	runs := append([]time.Time(nil), info.LastRunTimes...)
	sort.Slice(runs, func(i, j int) bool { return runs[i].After(runs[j]) })
	if len(runs) > 0 {
		rec.LastRun = runs[0].UTC().Format(time.RFC3339)
		for _, t := range runs[1:] {
			rec.PreviousRuns = append(rec.PreviousRuns, t.UTC().Format(time.RFC3339))
		}
	}
	return rec, nil
}

func parseOne(path string) (*record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var mod time.Time
	if st, err := f.Stat(); err == nil {
		mod = st.ModTime()
	}
	return parseReader(f, path, mod)
}

// parseTarStream reads a TAR archive from r and parses each *.pf entry with the
// same per-file parse as -f/-d, calling emit once per parsed file. It returns the
// count parsed and the count failed — a read or parse failure on one entry is
// counted and skipped, and the stream keeps going, mirroring gomount's first
// consumer (goyara). err is non-nil only for a fatal condition: a corrupt tar
// (which desynchronises every entry after it) or an emit callback that itself
// failed. One entry is buffered at a time, so the whole tar is never held.
func parseTarStream(r io.Reader, emit func(*record) error, quiet bool) (parsed, failed int, err error) {
	tr := tar.NewReader(r)
	for {
		hdr, nextErr := tr.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return parsed, failed, fmt.Errorf("read tar: %w", nextErr)
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			continue // only regular files carry bytes to parse
		}
		if !strings.EqualFold(filepath.Ext(hdr.Name), ".pf") {
			continue // located by the .pf extension, exactly like the -d walk
		}

		// go-prefetch needs an io.ReaderAt; the tar entry reader is sequential,
		// so buffer this one entry. tar.Reader bounds the read to the entry size.
		buf, readErr := io.ReadAll(tr)
		if readErr != nil {
			failed++
			fmt.Fprintf(os.Stderr, "goprefetch: FAILED %s: %v\n", hdr.Name, readErr)
			continue
		}
		rec, parseErr := parseReader(bytes.NewReader(buf), hdr.Name, hdr.ModTime)
		if parseErr != nil {
			failed++
			fmt.Fprintf(os.Stderr, "goprefetch: FAILED %s: %v\n", hdr.Name, parseErr)
			continue
		}
		if emitErr := emit(rec); emitErr != nil {
			return parsed, failed, emitErr
		}
		parsed++
		if !quiet {
			fmt.Fprintf(os.Stderr, "goprefetch: parsed %s (%s, run count %d)\n",
				hdr.Name, rec.Version, rec.RunCount)
		}
	}
	return parsed, failed, nil
}

func collectInputs(file, dir string) ([]string, error) {
	if file != "" {
		return []string{file}, nil
	}
	var out []string
	// Unreadable subtrees are skipped with a note, not fatal — mounted disk
	// image roots routinely contain them, especially rootless. A root that
	// cannot be read at all IS fatal: "no .pf files found" would mislead.
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir {
				return err
			}
			fmt.Fprintf(os.Stderr, "goprefetch: skipping unreadable %s: %v\n", p, err)
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(p), ".pf") {
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
		file    = flag.String("f", "", "single prefetch file to parse")
		dir     = flag.String("d", "", "directory to scan recursively for *.pf")
		tarMode = flag.Bool("tar", false, "read a TAR archive on stdin (as gomount stream emits) and parse each *.pf entry")
		jsonDir = flag.String("json", "", "directory to write JSONL output to (default: stdout)")
		jsonF   = flag.String("jsonf", "", "JSONL file name (default: PrefetchDump_Output.jsonl)")
		csvDir  = flag.String("csv", "", "directory to write CSV output to instead of JSONL")
		csvF    = flag.String("csvf", "", "CSV file name (default: PrefetchDump_Output.csv)")
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
		fmt.Fprintln(os.Stderr, "goprefetch: exactly one of -f <file>, -d <dir>, or --tar is required")
		flag.Usage()
		os.Exit(1)
	}

	var w io.WriteCloser
	var cw *csv.Writer
	var err error
	if *csvDir != "" {
		w, err = openOut(*csvDir, *csvF, "PrefetchDump_Output.csv")
	} else {
		w, err = openOut(*jsonDir, *jsonF, "PrefetchDump_Output.jsonl")
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "goprefetch: %v\n", err)
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
			fmt.Fprintf(os.Stderr, "goprefetch: write: %v\n", err)
			os.Exit(1)
		}
	}

	enc := json.NewEncoder(w)
	// emit writes one parsed record on the chosen output — CSV row or JSONL
	// object — and is the single record-emitting path both input modes share.
	emit := func(rec *record) error {
		if cw != nil {
			return cw.Write(rec.csvRow())
		}
		return enc.Encode(rec)
	}

	var failed, total int
	if *tarMode {
		parsed, tarFailed, err := parseTarStream(os.Stdin, emit, *quiet)
		failed, total = tarFailed, parsed+tarFailed
		if err != nil {
			fmt.Fprintf(os.Stderr, "goprefetch: %v\n", err)
			os.Exit(1)
		}
	} else {
		inputs, err := collectInputs(*file, *dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "goprefetch: %v\n", err)
			os.Exit(1)
		}
		if len(inputs) == 0 {
			fmt.Fprintln(os.Stderr, "goprefetch: no .pf files found")
			os.Exit(1)
		}
		total = len(inputs)
		for _, p := range inputs {
			rec, err := parseOne(p)
			if err != nil {
				failed++
				fmt.Fprintf(os.Stderr, "goprefetch: FAILED %s: %v\n", p, err)
				continue
			}
			if err := emit(rec); err != nil {
				fmt.Fprintf(os.Stderr, "goprefetch: write: %v\n", err)
				os.Exit(1)
			}
			if !*quiet {
				fmt.Fprintf(os.Stderr, "goprefetch: parsed %s (%s, run count %d)\n",
					p, rec.Version, rec.RunCount)
			}
		}
	}

	if cw != nil {
		cw.Flush()
		if err := cw.Error(); err != nil {
			fmt.Fprintf(os.Stderr, "goprefetch: write: %v\n", err)
			os.Exit(1)
		}
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "goprefetch: %d of %d files failed\n", failed, total)
		os.Exit(2)
	}
}
