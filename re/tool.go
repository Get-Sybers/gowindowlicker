// gore — Linux-native Windows Registry batch extractor for the DX_DFIR pipeline.
//
// Reads a batch definition (.reb YAML) and, for every registry
// hive it is pointed at, extracts the keys/values the batch names, on
// Velociraptor's regparser. One record per value in the shape byakugan's
// recmd_batch map consumes: HivePath, HiveType, Category, Description, Comment,
// KeyPath, ValueName, ValueType, ValueData, LastWriteTimestamp, Recursive,
// Deleted. Runs on Linux with no .NET, no shell, no libc (Dockerfile: FROM
// scratch, uid 2000).
//
// Dirty-hive .LOG1/.LOG2 transaction logs ARE replayed (regparser.RecoverHive)
// unless --nl is given; the recovered copy
// is written under --work-dir (a writable tmpfs, since the rootfs is read-only).
//
// SCOPE (never faked): gore runs the .reb batch's KEY/VALUE extraction
// (KeyPath, ValueName, Recursive) — it does NOT run derived-value plugin
// transforms (per-artefact decoders, e.g. UserAssist ROT13, AppCompatCache),
// and it does not recover *deleted* cells (regparser reads live cells).
// Records are live values (Deleted=false). The bundled batch
// (batch/default.reb) is a curated forensic-key set; supply your own with --bn.
//
// With no arguments the binary runs the container-framework batch mode (the shared
// batch package): it reads GORE_INPUT_DIR / GORE_OUT_DIR / GORE_FORCE / GORE_FORMAT /
// GORE_BATCH / GORE_REPLAY, content-detects every registry hive under the input
// tree, writes one output folder per hive and prints one JSON summary line.
// The argv flags below are the debug pass-through.
//
// argv exit codes: 0 = ok; 1 = usage/fatal; 2 = at least one hive failed to
// parse. Batch mode uses the uniform 0/1/2/3 table.
package re

import (
	"encoding/csv"
	"encoding/hex"
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

	batch "github.com/get-sybers/gopinfo/framework"

	"gopkg.in/yaml.v3"
	"www.velocidex.com/golang/regparser"
)

// rebBatch is a parsed .reb definition — YAML: a Description/Author
// header and a Keys list; gore reads the fields that drive extraction.
type rebBatch struct {
	Description string     `yaml:"Description"`
	Author      string     `yaml:"Author"`
	Keys        []batchKey `yaml:"Keys"`
}

type batchKey struct {
	Description string `yaml:"Description"`
	HiveType    string `yaml:"HiveType"`
	Category    string `yaml:"Category"`
	KeyPath     string `yaml:"KeyPath"`
	ValueName   string `yaml:"ValueName"`
	Recursive   bool   `yaml:"Recursive"`
	Comment     string `yaml:"Comment"`
}

// record is one extracted value — the recmd_batch shape byakugan reads.
type record struct {
	HivePath           string `json:"HivePath"`
	HiveType           string `json:"HiveType"`
	Category           string `json:"Category"`
	Description        string `json:"Description"`
	Comment            string `json:"Comment"`
	KeyPath            string `json:"KeyPath"`
	ValueName          string `json:"ValueName"`
	ValueType          string `json:"ValueType"`
	ValueData          string `json:"ValueData"`
	LastWriteTimestamp string `json:"LastWriteTimestamp"`
	Recursive          bool   `json:"Recursive"`
	Deleted            bool   `json:"Deleted"`
}

var csvHeader = []string{"HivePath", "HiveType", "Category", "Description", "Comment",
	"KeyPath", "ValueName", "ValueType", "ValueData", "LastWriteTimestamp", "Recursive", "Deleted"}

func (r *record) csvRow() []string {
	return []string{r.HivePath, r.HiveType, r.Category, r.Description, r.Comment,
		r.KeyPath, r.ValueName, r.ValueType, r.ValueData, r.LastWriteTimestamp,
		strconv.FormatBool(r.Recursive), strconv.FormatBool(r.Deleted)}
}

// hiveTypeOf maps a hive FILE name to the batch HiveType token (the file name
// is an exact, reliable proxy for
// the standard hives the batch targets). "" = unknown (skipped for typed keys).
func hiveTypeOf(path string) string {
	switch strings.ToUpper(filepath.Base(path)) {
	case "NTUSER.DAT":
		return "NtUser"
	case "USRCLASS.DAT":
		return "UsrClass"
	case "SYSTEM":
		return "System"
	case "SOFTWARE":
		return "Software"
	case "SAM":
		return "Sam"
	case "SECURITY":
		return "Security"
	case "AMCACHE.HVE":
		return "Amcache"
	}
	return ""
}

// valueDataString renders a value the way the recmd_batch map expects to read
// it: strings/multi-sz/ints as text, binary as hex (never a Go artefact).
// The integer branch is keyed off the value *type*, not off Uint64 being
// non-zero, so a genuine DWORD/QWORD of 0 renders as "0" rather than falling
// through to the raw-byte (hex/empty) branch.
func valueDataString(vd *regparser.ValueData) string {
	if vd == nil {
		return ""
	}
	switch vd.Type {
	case regparser.REG_DWORD, regparser.REG_DWORD_BIG_ENDIAN, regparser.REG_QWORD:
		return strconv.FormatUint(vd.Uint64, 10)
	case regparser.REG_SZ, regparser.REG_EXPAND_SZ:
		return vd.String
	case regparser.REG_MULTI_SZ:
		return strings.Join(vd.MultiSz, " ")
	}
	// REG_BINARY / REG_NONE / unknown: prefer a decoded string if the parser
	// filled one, else the raw bytes as hex — never a Go artefact.
	switch {
	case vd.String != "":
		return vd.String
	case len(vd.MultiSz) > 0:
		return strings.Join(vd.MultiSz, " ")
	case len(vd.Data) > 0:
		return hex.EncodeToString(vd.Data)
	}
	return ""
}

func lastWrite(node *regparser.CM_KEY_NODE) string {
	if node == nil {
		return ""
	}
	ft := node.LastWriteTime()
	if ft == nil || ft.Time.IsZero() {
		return ""
	}
	// space-separated: byakugan's recmd_batch normalises " " -> "T" to ISO.
	return ft.Time.UTC().Format("2006-01-02 15:04:05.0000000")
}

// emitKey emits one record per value of `node` (the key found at bk.KeyPath, or
// a subkey when recursing). keyPath is the full path of `node`.
func emitKey(reg *regparser.Registry, node *regparser.CM_KEY_NODE, keyPath string,
	bk batchKey, hivePath, hiveType string, e *emitter) (int, error) {
	if node == nil {
		return 0, nil
	}
	lw := lastWrite(node)
	n := 0
	values := node.Values()
	for _, v := range values {
		name := v.ValueName()
		if bk.ValueName != "" && !strings.EqualFold(name, bk.ValueName) {
			continue // a named-value key emits only that value
		}
		rec := &record{
			HivePath: hivePath, HiveType: hiveType, Category: bk.Category,
			Description: bk.Description, Comment: bk.Comment, KeyPath: keyPath,
			ValueName: name, ValueType: v.TypeString(),
			ValueData: valueDataString(v.ValueData()), LastWriteTimestamp: lw,
			Recursive: bk.Recursive, Deleted: false,
		}
		if err := e.emit(rec); err != nil {
			return n, err
		}
		n++
	}
	// a key with no matching values but a real KeyPath is still a record byakugan
	// keeps (recmd_is_value_record needs a KeyPath, ValueName may be empty) — emit
	// a keystub when the batch didn't name a specific value and the key was empty.
	if bk.ValueName == "" && len(values) == 0 {
		rec := &record{HivePath: hivePath, HiveType: hiveType, Category: bk.Category,
			Description: bk.Description, Comment: bk.Comment, KeyPath: keyPath,
			LastWriteTimestamp: lw, Recursive: bk.Recursive}
		if err := e.emit(rec); err != nil {
			return n, err
		}
		n++
	}
	if bk.Recursive {
		for _, sub := range node.Subkeys() {
			sn, err := emitKey(reg, sub, keyPath+"\\"+sub.Name(), bk, hivePath, hiveType, e)
			if err != nil {
				return n, err
			}
			n += sn
		}
	}
	return n, nil
}

func runHive(hivePath string, b *rebBatch, workDir string, replay, quiet bool, e *emitter) (int, error) {
	reg, cleanup, note, replayFailed, err := openHive(hivePath, workDir, replay)
	if err != nil {
		return 0, err
	}
	defer cleanup()
	// A dirty-hive replay that fell back to the committed state is a fidelity
	// warning (recent transactions may be missing), so surface it even under -q;
	// a successful recovery is routine progress, shown only when not quiet.
	if replayFailed {
		fmt.Fprintf(os.Stderr, "gore: WARNING %s: %s\n", hivePath, note)
	} else if !quiet && note != "" && note != "committed" {
		fmt.Fprintf(os.Stderr, "gore: %s: %s\n", hivePath, note)
	}
	hiveType := hiveTypeOf(hivePath)
	n := 0
	for _, bk := range b.Keys {
		if bk.HiveType != "" && !strings.EqualFold(bk.HiveType, hiveType) {
			continue // this batch key targets a different hive type
		}
		node := reg.OpenKey(bk.KeyPath)
		if node == nil {
			continue // key absent in this hive
		}
		kn, err := emitKey(reg, node, bk.KeyPath, bk, hivePath, hiveType, e)
		if err != nil {
			return n, err
		}
		n += kn
	}
	return n, nil
}

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

const regfMagic = "regf"

func isLogFile(p string) bool {
	u := strings.ToUpper(p)
	return strings.HasSuffix(u, ".LOG") || strings.HasSuffix(u, ".LOG1") || strings.HasSuffix(u, ".LOG2")
}

func looksLikeHive(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	var hdr [4]byte
	if _, err := io.ReadFull(f, hdr[:]); err != nil {
		return false
	}
	return string(hdr[:]) == regfMagic
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
			fmt.Fprintf(os.Stderr, "gore: skipping unreadable %s: %v\n", p, err)
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

const defaultBatch = "/batch/default.reb"

// Tool binds this parser to the shared batch runtime.
var Tool = batch.Tool{
	Name:     "gore",
	Formats:  []string{"json", "csv"},
	Discover: batchDiscover,
	Process:  batchProcess,
}

// The batch-mode settings resolved once by batchDiscover (GORE_BATCH, the
// parsed .reb definition, and GORE_REPLAY) and read by every batchProcess call.
var (
	batchDef    *rebBatch
	batchReplay = true
)

// loadBatch reads and validates a .reb batch definition.
func loadBatch(path string) (*rebBatch, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var b rebBatch
	if err := yaml.Unmarshal(data, &b); err != nil {
		return nil, err
	}
	if len(b.Keys) == 0 {
		return nil, fmt.Errorf("no Keys")
	}
	return &b, nil
}

// batchDiscover resolves the batch definition and replay setting (a bad value
// is a config error), then walks the input tree and keeps every regf hive
// that is not a .LOG* transaction log.
func batchDiscover(cfg *batch.Config) ([]string, error) {
	bn := cfg.Env("BATCH", defaultBatch)
	b, err := loadBatch(bn)
	if err != nil {
		return nil, fmt.Errorf("%s_BATCH %s: %w", cfg.Prefix, bn, err)
	}
	batchDef = b
	replay, err := batch.ParseBool(cfg.Env("REPLAY", "1"))
	if err != nil {
		return nil, fmt.Errorf("%s_REPLAY: %w", cfg.Prefix, err)
	}
	batchReplay = replay
	files, err := collectInputs("", cfg.InputDir)
	if err != nil {
		return nil, err
	}
	var items []string
	for _, p := range files {
		if !isLogFile(p) && looksLikeHive(p) {
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

// batchProcess extracts the batch's keys from one hive (replaying sibling
// .LOG1/.LOG2 into the work dir) into its record file.
func batchProcess(cfg *batch.Config, item, _ string, w io.Writer) (int, error) {
	e, flush, err := batchEmitter(w, cfg.Format)
	if err != nil {
		return 0, err
	}
	n, err := runHive(item, batchDef, cfg.WorkDir, batchReplay, cfg.Quiet(), e)
	if err != nil {
		return n, err
	}
	return n, flush()
}

func Main(version, contractYML string) {
	batch.Entry(Tool, batch.Options{Version: version, Contract: contractYML})
	var (
		file    = flag.String("f", "", "single registry hive to process")
		dir     = flag.String("d", "", "directory to scan recursively for registry hives (regf)")
		bn      = flag.String("bn", defaultBatch, "batch definition file (.reb YAML)")
		jsonDir = flag.String("json", "", "directory to write JSONL output to (default: stdout)")
		jsonF   = flag.String("jsonf", "", "JSONL file name (default: RECmd_Batch_Output.json)")
		csvDir  = flag.String("csv", "", "directory to write CSV output to instead of JSONL")
		csvF    = flag.String("csvf", "", "CSV file name (default: RECmd_Batch_Output.csv)")
		workDir = flag.String("work-dir", os.TempDir(), "writable dir for the recovered hive during .LOG replay")
		nl      = flag.Bool("nl", false, "no transaction logs: skip dirty-hive .LOG1/.LOG2 replay")
		quiet   = flag.Bool("q", false, "suppress per-file progress on stderr")
	)
	flag.Parse()

	if (*file == "") == (*dir == "") {
		fmt.Fprintln(os.Stderr, "gore: exactly one of -f <hive> or -d <dir> is required")
		flag.Usage()
		os.Exit(1)
	}
	data, err := os.ReadFile(*bn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gore: batch file %s: %v\n", *bn, err)
		os.Exit(1)
	}
	var b rebBatch
	if err := yaml.Unmarshal(data, &b); err != nil {
		fmt.Fprintf(os.Stderr, "gore: batch file %s: %v\n", *bn, err)
		os.Exit(1)
	}
	if len(b.Keys) == 0 {
		fmt.Fprintf(os.Stderr, "gore: batch file %s has no Keys\n", *bn)
		os.Exit(1)
	}

	dirMode := *dir != ""
	if dirMode {
		if err := os.MkdirAll(*workDir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "gore: work dir %s not usable (%v); .LOG replay will fall back to committed hives\n", *workDir, err)
		}
	}
	inputs, err := collectInputs(*file, *dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gore: %v\n", err)
		os.Exit(1)
	}
	if len(inputs) == 0 {
		fmt.Fprintln(os.Stderr, "gore: no files found")
		os.Exit(1)
	}

	var w io.WriteCloser
	e := &emitter{}
	if *csvDir != "" {
		w, err = openOut(*csvDir, *csvF, "RECmd_Batch_Output.csv")
	} else {
		w, err = openOut(*jsonDir, *jsonF, "RECmd_Batch_Output.json")
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "gore: %v\n", err)
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
			fmt.Fprintf(os.Stderr, "gore: write: %v\n", err)
			os.Exit(1)
		}
	} else {
		e.enc = json.NewEncoder(w)
	}

	replay := !*nl
	failed, hives, records := 0, 0, 0
	for _, p := range inputs {
		if dirMode && (isLogFile(p) || !looksLikeHive(p)) {
			continue // a hive begins with "regf"; .LOG* are consumed via replay
		}
		n, err := runHive(p, &b, *workDir, replay, *quiet, e)
		if err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "gore: FAILED %s: %v\n", p, err)
			continue
		}
		hives++
		records += n
		if !*quiet {
			fmt.Fprintf(os.Stderr, "gore: %s (%s): %d records\n", p, hiveTypeOf(p), n)
		}
	}
	if e.cw != nil {
		e.cw.Flush()
		if err := e.cw.Error(); err != nil {
			fmt.Fprintf(os.Stderr, "gore: write: %v\n", err)
			os.Exit(1)
		}
	}
	if !*quiet {
		fmt.Fprintf(os.Stderr, "gore: %d records across %d hive(s)\n", records, hives)
	}
	if dirMode && hives == 0 && failed == 0 {
		fmt.Fprintf(os.Stderr, "gore: no registry hives found under %s\n", *dir)
		os.Exit(1)
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "gore: %d hive(s) failed to parse\n", failed)
		os.Exit(2)
	}
}

// openHive opens the hive at p for reading. When replay is set and sibling
// .LOG1/.LOG2 dirty-hive logs are present it replays them (regparser.RecoverHive)
// into a recovered copy written under workDir (RecoverHive honours $TMPDIR), and
// parses that; otherwise the committed hive. Graceful: a replay error or
// unusable workDir falls back to the committed hive, never a hard fail.
// The returned bool is set when a dirty-hive .LOG replay was attempted but
// failed and parsing fell back to the committed state (a fidelity warning).
func openHive(p, workDir string, replay bool) (*regparser.Registry, func(), string, bool, error) {
	hf, err := os.Open(p)
	if err != nil {
		return nil, nil, "", false, err
	}
	var logs []*os.File
	if replay {
		for _, suffix := range []string{".LOG1", ".LOG2"} {
			if lf, lerr := os.Open(p + suffix); lerr == nil {
				logs = append(logs, lf)
			}
		}
	}
	if len(logs) > 0 {
		old := os.Getenv("TMPDIR")
		_ = os.Setenv("TMPDIR", workDir)
		recovered, rerr := regparser.RecoverHive(hf, logs...)
		_ = os.Setenv("TMPDIR", old)
		for _, lf := range logs {
			lf.Close()
		}
		if rerr == nil {
			hf.Close()
			reg, nerr := regparser.NewRegistry(recovered)
			if nerr != nil {
				recovered.Close()
				os.Remove(recovered.Name())
				return nil, nil, "", false, nerr
			}
			return reg, func() { recovered.Close(); os.Remove(recovered.Name()) }, "recovered via .LOG replay", false, nil
		}
		reg, nerr := regparser.NewRegistry(hf)
		if nerr != nil {
			hf.Close()
			return nil, nil, "", false, nerr
		}
		return reg, func() { hf.Close() }, fmt.Sprintf("committed (.LOG replay failed: %v)", rerr), true, nil
	}
	reg, nerr := regparser.NewRegistry(hf)
	if nerr != nil {
		hf.Close()
		return nil, nil, "", false, nerr
	}
	return reg, func() { hf.Close() }, "committed", false, nil
}
