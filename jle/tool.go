// gojle — Linux-native Windows Jump List parser for the DX_DFIR pipeline.
//
// Parses AutomaticDestinations
// (*.automaticDestinations-ms — an OLE compound file, via richardlehane/mscfb) and
// their DestList stream, emitting one record per jump-list FILE in the shape
// byakugan's jlecmd_dest map / jlecmd adapter consume:
//
//	{"AppId":{"AppId":..,"Description":..}, "SourceFile":..,
//	 "DestListEntries":[{"Path","EntryNumber","CreatedOn","LastModified","Hostname",
//	                     "InteractionCount","MRUPosition","Pinned","MacAddress","VolumeDroid"}...]}
//
// CreatedOn/LastModified are the .NET "/Date(ms)/" form (the byakugan adapter's
// DotnetDate() decodes it). It runs FROM scratch, uid 2000, no .NET/shell/libc.
//
// Coverage: DestList versions 1/3/4. CustomDestinations (a bare LNK sequence) are
// NOT emitted here — byakugan consumes the AutomaticDestinations jlecmd_dest shape;
// a CustomDestinations file is skipped with a note, never mis-parsed. The AppId ->
// friendly-name table covers well-known ids only; those get a Description,
// unknown ids get "" (never invented).
//
// Input is one of three modes: a single file (-f), a directory scanned recursively
// (-d), or a tar archive on stdin (--tar) as `gomount stream` emits — one entry
// per file, entry name = the file's volume path. The tar mode buffers each
// AutomaticDestinations entry (mscfb needs an io.ReaderAt) and runs the same
// OLE/DestList parse the file modes use, one file in memory at a time:
//
//	gomount stream --filter '*.automaticDestinations-ms' <image> | gowindowlicker gojle --tar
//
// With no arguments the binary runs the container-framework batch mode (the shared
// batch package): it reads GOJLE_INPUT_DIR / GOJLE_OUT_DIR / GOJLE_FORCE, finds
// every AutomaticDestinations file under the input tree, writes one output
// folder per file and prints one JSON summary line. The argv flags below are
// the debug pass-through.
//
// argv exit codes: 0 = every file parsed; 1 = usage/fatal; 2 = a file failed
// to parse. Batch mode uses the uniform 0/1/2/3 table.
package jle

import (
	"archive/tar"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf16"

	batch "github.com/Get-Sybers/gopinfo/framework"

	lnk "github.com/parsiya/golnk"
	"github.com/richardlehane/mscfb"
)

type destEntry struct {
	Path             string      `json:"Path"`
	EntryNumber      uint32      `json:"EntryNumber"`
	CreatedOn        string      `json:"CreatedOn"`
	LastModified     string      `json:"LastModified"`
	Hostname         string      `json:"Hostname"`
	InteractionCount interface{} `json:"InteractionCount"`
	MRUPosition      int         `json:"MRUPosition"`
	Pinned           bool        `json:"Pinned"`
	MacAddress       string      `json:"MacAddress"`
	VolumeDroid      string      `json:"VolumeDroid"`
}

type appID struct {
	AppId       string `json:"AppId"`
	Description string `json:"Description"`
}

type record struct {
	AppId           appID       `json:"AppId"`
	SourceFile      string      `json:"SourceFile"`
	DestListEntries []destEntry `json:"DestListEntries"`
}

// dotnetDate renders a FILETIME-derived time in the record shape's
// "/Date(ms)/" .NET-JSON form; ""
// when zero.
func dotnetDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return fmt.Sprintf("/Date(%d)/", t.UnixMilli())
}

// filetime -> time (100ns since 1601).
func filetimeToTime(ft uint64) time.Time {
	if ft == 0 {
		return time.Time{}
	}
	const epochGap = 11644473600
	secs := int64(ft/10_000_000) - epochGap
	nsec := int64(ft%10_000_000) * 100
	return time.Unix(secs, nsec).UTC()
}

func guidString(b []byte) string {
	if len(b) < 16 {
		return ""
	}
	return fmt.Sprintf("%08X-%04X-%04X-%04X-%012X",
		binary.LittleEndian.Uint32(b[0:4]), binary.LittleEndian.Uint16(b[4:6]),
		binary.LittleEndian.Uint16(b[6:8]), be16(b[8:10]), b[10:16])
}
func be16(b []byte) uint16 { return uint16(b[0])<<8 | uint16(b[1]) }

// macFromFileDroid: only a v1 (time-based) UUID carries the creating host's MAC
// in its node (last 6 bytes). The version is the high nibble of the third GUID
// group, i.e. the top nibble of byte 7 (Windows stores that group little-endian,
// so byte 7 is its most-significant byte). Any other version means the node is
// random/hash bits, not a MAC — return empty rather than a bogus address. Also
// empty when the droid is absent or all-zero.
func macFromFileDroid(b []byte) string {
	if len(b) < 16 {
		return ""
	}
	if b[7]>>4 != 1 { // UUID version nibble != 1 (not time-based) → no MAC
		return ""
	}
	node := b[10:16]
	allZero := true
	for _, x := range node {
		if x != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return ""
	}
	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X", node[0], node[1], node[2], node[3], node[4], node[5])
}

func utf16le(b []byte) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, binary.LittleEndian.Uint16(b[i:i+2]))
	}
	return strings.TrimRight(string(utf16.Decode(u)), "\x00")
}

// parseDestList parses a DestList stream (v1/v3/v4) into entries. Layout: a
// 32-byte header, then per entry a fixed prefix (checksum, VolumeDroid, FileDroid,
// Hostname, EntryNumber, ..., LastModified, PinStatus), a version-dependent gap,
// then a uint16 path length and a UTF-16 path (+4 trailing bytes on v4).
func parseDestList(data []byte) ([]destEntry, error) {
	if len(data) < 32 {
		return nil, fmt.Errorf("DestList too small (%d bytes)", len(data))
	}
	version := binary.LittleEndian.Uint32(data[0:4])
	numEntries := binary.LittleEndian.Uint32(data[4:8]) // header's declared entry count
	off := 32
	var entries []destEntry
	pos := 0
	for off+120 <= len(data) { // fixed prefix (through AccessCount @0x74) must fit
		// DestList entry layout (confirmed against real v4 evidence, matches the
		// libfwsi Jump-List spec): checksum(8), NewVolumeDroid(16 @0x08),
		// NewFileDroid(16 @0x18), BirthVolumeDroid(16 @0x28), BirthFileDroid(16
		// @0x38), Hostname(16 @0x48), EntryNumber(4 @0x58), unknown(4), float(4),
		// LastModified FILETIME(8 @0x64), PinStatus(4 @0x6C). v1 then goes straight
		// to the uint16 path length @0x70; v3/v4 insert unknown(4)+AccessCount(4
		// @0x74)+unknown(8) and put the path length @0x80 (+4-byte tail after path).
		volDroid := data[off+8 : off+24]
		fileDroid := data[off+24 : off+40]
		host := strings.TrimRight(string(bytes.TrimRight(data[off+72:off+88], "\x00")), " ")
		entryNum := binary.LittleEndian.Uint32(data[off+88 : off+92])
		var interaction interface{}
		if version >= 3 {
			interaction = binary.LittleEndian.Uint32(data[off+116 : off+120]) // AccessCount @0x74
		}
		lastMod := filetimeToTime(binary.LittleEndian.Uint64(data[off+100 : off+108]))
		pinStatus := int32(binary.LittleEndian.Uint32(data[off+108 : off+112]))
		// path length position differs by version: v1 @0x70, v3/v4 @0x80.
		var pathOff, pathLenOff int
		if version == 1 {
			pathLenOff = off + 112
		} else {
			pathLenOff = off + 128
		}
		if pathLenOff+2 > len(data) {
			return entries, fmt.Errorf("DestList truncated: entry %d path length runs past end of stream", pos)
		}
		nChars := int(binary.LittleEndian.Uint16(data[pathLenOff : pathLenOff+2]))
		pathOff = pathLenOff + 2
		if nChars <= 0 || pathOff+nChars*2 > len(data) {
			return entries, fmt.Errorf("DestList corrupt/truncated: entry %d has bad path length %d", pos, nChars)
		}
		path := utf16le(data[pathOff : pathOff+nChars*2])
		entries = append(entries, destEntry{
			Path:             path,
			EntryNumber:      entryNum,
			LastModified:     dotnetDate(lastMod),
			Hostname:         host,
			InteractionCount: interaction,
			MRUPosition:      pos,
			Pinned:           pinStatus >= 0,
			MacAddress:       macFromFileDroid(fileDroid),
			VolumeDroid:      guidString(volDroid),
		})
		pos++
		next := pathOff + nChars*2
		if version == 4 {
			next += 4 // v4 has a 4-byte "unknown" tail after the path
		}
		if next <= off {
			return entries, fmt.Errorf("DestList corrupt: entry %d made no forward progress", pos)
		}
		off = next
	}
	// The loop exits when the remaining bytes can't hold another entry's fixed
	// prefix. If we parsed fewer than the header declared, the stream was cut
	// short mid-record — surface it so the caller counts the file as failed
	// rather than reporting a clean partial parse.
	if numEntries > 0 && uint32(len(entries)) < numEntries {
		return entries, fmt.Errorf("DestList truncated: parsed %d of %d declared entries", len(entries), numEntries)
	}
	return entries, nil
}

func readStream(r *mscfb.Reader, f *mscfb.File) []byte {
	buf := make([]byte, f.Size)
	n, _ := io.ReadFull(r, buf)
	return buf[:n]
}

// parseReader parses one AutomaticDestinations OLE compound file read through ra
// and builds the record. ra is an *os.File on disk (-f/-d) or a *bytes.Reader over
// a buffered tar entry (--tar); mscfb.New needs an io.ReaderAt, which both satisfy.
// sourceFile is recorded verbatim as SourceFile, and its base name's stem is the
// AppId (mapped to a friendly Description for well-known ids).
func parseReader(ra io.ReaderAt, sourceFile string) (*record, error) {
	doc, err := mscfb.New(ra)
	if err != nil {
		return nil, err
	}
	var destListData []byte
	lnkStreams := map[string][]byte{}
	for {
		entry, err := doc.Next()
		if err == io.EOF {
			break // clean end of stream enumeration
		}
		if err != nil {
			return nil, fmt.Errorf("mscfb enumerate: %w", err) // parse/IO failure → file fails
		}
		data := readStream(doc, entry)
		if strings.EqualFold(entry.Name, "DestList") {
			destListData = data
		} else {
			lnkStreams[entry.Name] = data
		}
	}
	if destListData == nil {
		return nil, fmt.Errorf("no DestList stream (not an AutomaticDestinations file?)")
	}
	entries, err := parseDestList(destListData)
	if err != nil {
		return nil, err
	}
	// enrich CreatedOn from the numbered LNK stream (named by entry number, hex)
	for i := range entries {
		for _, cand := range []string{fmt.Sprintf("%d", entries[i].EntryNumber), fmt.Sprintf("%x", entries[i].EntryNumber)} {
			if raw, ok := lnkStreams[cand]; ok {
				if lf, err := lnk.Read(bytes.NewReader(raw), uint64(len(raw))); err == nil {
					entries[i].CreatedOn = dotnetDate(lf.Header.CreationTime)
					if entries[i].Path == "" {
						local := lf.LinkInfo.LocalBasePathUnicode
						if local == "" {
							local = lf.LinkInfo.LocalBasePath
						}
						entries[i].Path = local
					}
				}
				break
			}
		}
	}
	base := filepath.Base(sourceFile)
	id := strings.SplitN(base, ".", 2)[0] // the AppId is the file stem
	return &record{
		AppId:           appID{AppId: id, Description: appIDName(id)},
		SourceFile:      sourceFile,
		DestListEntries: entries,
	}, nil
}

// parseOne opens an AutomaticDestinations file on disk and parses it. The image
// is only ever read.
func parseOne(path string) (*record, error) {
	fh, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	return parseReader(fh, path)
}

// isJumpList reports whether name is an AutomaticDestinations jump list, by the
// same "automaticdestinations" substring the -d directory walk uses. It matches
// both the leaf (*.automaticDestinations-ms) and the containing directory, and
// never matches CustomDestinations.
func isJumpList(name string) bool {
	return strings.Contains(strings.ToLower(name), "automaticdestinations")
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
			fmt.Fprintf(os.Stderr, "gojle: skipping unreadable %s: %v\n", p, err)
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() && isJumpList(p) {
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

// runTar reads a tar archive from r (one entry per file, entry name = the file's
// volume path, as `gomount stream` emits) and parses every AutomaticDestinations
// entry with the same OLE/DestList path the -f/-d modes use, encoding one record
// per file. Each matching entry is buffered whole (mscfb needs an io.ReaderAt),
// one file in memory at a time. A per-file parse failure is counted and skipped —
// the stream keeps going. A corrupt tar or an output write error is fatal. It
// returns the number of files that failed to parse.
func runTar(r io.Reader, enc *json.Encoder, quiet bool) int {
	tr := tar.NewReader(r)
	failed := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			// A malformed tar desynchronises every entry after it — fatal.
			fmt.Fprintf(os.Stderr, "gojle: read tar: %v\n", err)
			os.Exit(1)
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			continue // only regular files carry a jump list
		}
		if !isJumpList(hdr.Name) {
			continue // e.g. CustomDestinations, or any other file gomount walked
		}
		// Buffer this one entry: mscfb.New seeks within the OLE compound file, so
		// it needs an io.ReaderAt. Jump lists are small OLE files and only one is
		// held at a time, so memory stays bounded to a single file.
		buf, err := io.ReadAll(tr)
		if err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "gojle: FAILED %s: read: %v\n", hdr.Name, err)
			continue
		}
		rec, err := parseReader(bytes.NewReader(buf), hdr.Name)
		if err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "gojle: FAILED %s: %v\n", hdr.Name, err)
			continue
		}
		if err := enc.Encode(rec); err != nil {
			fmt.Fprintf(os.Stderr, "gojle: write: %v\n", err)
			os.Exit(1)
		}
		if !quiet {
			fmt.Fprintf(os.Stderr, "gojle: parsed %s (%d entries)\n", hdr.Name, len(rec.DestListEntries))
		}
	}
	return failed
}

func usage() {
	fmt.Fprint(os.Stderr,
		"gojle — parse Windows AutomaticDestinations jump lists to the jlecmd_dest record shape\n\n"+
			"Input is one of: a single file (-f), a directory scanned recursively (-d), or a\n"+
			"tar archive on stdin (--tar), as `gomount stream` emits (one entry per file):\n\n"+
			"  gojle -d <dir> [--json OUT] [--jsonf NAME]\n"+
			"  gomount stream --filter '*.automaticDestinations-ms' <image> | gowindowlicker gojle --tar\n\n"+
			"Flags:\n")
	flag.PrintDefaults()
}

// Tool binds this parser to the shared batch runtime.
var Tool = batch.Tool{
	Name:     "gojle",
	Formats:  []string{"json"},
	Discover: batchDiscover,
	Process:  batchProcess,
}

// batchDiscover walks the input tree and keeps every AutomaticDestinations
// jump list — the same selection -d applies.
func batchDiscover(cfg *batch.Config) ([]string, error) {
	return collectInputs("", cfg.InputDir)
}

// batchProcess parses one jump list into its JSONL record file (one record per
// file, carrying every DestList entry).
func batchProcess(cfg *batch.Config, item, _ string, w io.Writer) (int, error) {
	rec, err := parseOne(item)
	if err != nil {
		return 0, err
	}
	if err := json.NewEncoder(w).Encode(rec); err != nil {
		return 0, err
	}
	cfg.Logf(batch.LogDebug, "%s: %d DestList entries", item, len(rec.DestListEntries))
	return 1, nil
}

func Main(version, contractYML string) {
	batch.Entry(Tool, batch.Options{Version: version, Contract: contractYML})
	var (
		file    = flag.String("f", "", "single *.automaticDestinations-ms file to parse")
		dir     = flag.String("d", "", "directory to scan recursively for AutomaticDestinations")
		tarIn   = flag.Bool("tar", false, "read a tar archive on stdin (one entry per file, as `gomount stream` emits) and parse each AutomaticDestinations entry")
		jsonDir = flag.String("json", "", "directory to write JSONL output to (default: stdout)")
		jsonF   = flag.String("jsonf", "", "JSONL file name (default: JLECmd_Output.json)")
		quiet   = flag.Bool("q", false, "suppress per-file progress on stderr")
	)
	flag.Usage = usage
	flag.Parse()

	// Exactly one input mode.
	modes := 0
	for _, on := range []bool{*file != "", *dir != "", *tarIn} {
		if on {
			modes++
		}
	}
	if modes != 1 {
		fmt.Fprintln(os.Stderr, "gojle: exactly one of -f <file>, -d <dir>, or --tar is required")
		flag.Usage()
		os.Exit(1)
	}

	w, err := openOut(*jsonDir, *jsonF, "JLECmd_Output.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "gojle: %v\n", err)
		os.Exit(1)
	}
	closeOut := func() {
		if w != os.Stdout {
			w.Close()
		}
	}
	enc := json.NewEncoder(w)

	if *tarIn {
		failed := runTar(os.Stdin, enc, *quiet)
		closeOut()
		if failed > 0 {
			fmt.Fprintf(os.Stderr, "gojle: %d file(s) failed\n", failed)
			os.Exit(2)
		}
		return
	}

	inputs, err := collectInputs(*file, *dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gojle: %v\n", err)
		os.Exit(1)
	}
	if len(inputs) == 0 {
		fmt.Fprintln(os.Stderr, "gojle: no AutomaticDestinations jump lists found")
		os.Exit(1)
	}

	failed := 0
	for _, p := range inputs {
		rec, err := parseOne(p)
		if err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "gojle: FAILED %s: %v\n", p, err)
			continue
		}
		if err := enc.Encode(rec); err != nil {
			fmt.Fprintf(os.Stderr, "gojle: write: %v\n", err)
			os.Exit(1)
		}
		if !*quiet {
			fmt.Fprintf(os.Stderr, "gojle: parsed %s (%d entries)\n", p, len(rec.DestListEntries))
		}
	}
	closeOut()
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "gojle: %d of %d files failed\n", failed, len(inputs))
		os.Exit(2)
	}
}

// appIDName maps a handful of well-known AppIds to their friendly name.
// Unknown ids get "" (never invented).
func appIDName(id string) string {
	return map[string]string{
		"1b4dd67f29cb1962": "Windows Explorer",
		"5f7b5f1e01b83767": "Quick Access",
		"9b9cdc69c1c24e2b": "Notepad",
		"9d1f905ce5044aee": "Windows Explorer (Win10)",
		"fb3b0dbfee58fac8": "Microsoft Word 2016",
		"a7bd71699cd38d1c": "Microsoft Word",
		"adecfb853d77462a": "Microsoft Word 2013",
		"e70d383b17e5c2a2": "Microsoft PowerPoint",
	}[strings.ToLower(id)]
}
