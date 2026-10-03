package main

// main_test.go proves the one-binary shape: subtool dispatch runs a
// parser's ordinary batch mode under its own env block, the sweep runs
// every parser into its own output tree and prints exactly one aggregate
// JSON line, and the registry stays consistent.

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	batch "github.com/Get-Sybers/gopinfo/framework"
)

// v2Record builds a Windows 10 style Recycle Bin $I record — the smallest
// self-contained Windows artefact in the matrix, so the dispatcher tests
// need no committed fixture of their own.
func v2Record(path string) []byte {
	u := utf16.Encode([]rune(path))
	u = append(u, 0)
	b := make([]byte, 28+len(u)*2)
	binary.LittleEndian.PutUint64(b[0:8], 2)
	binary.LittleEndian.PutUint64(b[8:16], 1234)
	binary.LittleEndian.PutUint64(b[16:24], 133528896000000000) // 2024-03-01T12:00:00Z
	binary.LittleEndian.PutUint32(b[24:28], uint32(len(u)))
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[28+i*2:], c)
	}
	return b
}

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// goreBatch points GORE_BATCH at the bundled definition, which in the image
// lives at /batch/default.reb but on a test host sits beside the re package.
func goreBatch(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("re", "batch", "default.reb"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSubtoolDispatch(t *testing.T) {
	in, out := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(in, "_I3XKCd7.pdf"), v2Record(`C:\Users\x\y.pdf`), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	code := run([]string{"gorb"}, env(map[string]string{
		"GORB_INPUT_DIR": in, "GORB_OUT_DIR": out, "GORB_WORK_DIR": t.TempDir(),
	}), &buf)
	if code != 0 {
		t.Fatalf("dispatch exit %d: %s", code, buf.String())
	}
	var sum map[string]any
	json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &sum)
	if sum["tool"] != "gorb" || sum["records"] != float64(1) {
		t.Fatalf("summary: %v", sum)
	}
	if _, err := os.Stat(filepath.Join(out, "_I3XKCd7.pdf", "gorb.jsonl")); err != nil {
		t.Fatalf("record file: %v", err)
	}
}

// TestEverySubtoolEmptyTree is the batch contract over the whole registry
// (the per-parser binaries are gone; this is their binary-level test now):
// an empty evidence tree is exit 1, status "nothing", the tool's own name.
func TestEverySubtoolEmptyTree(t *testing.T) {
	for _, s := range subs {
		pfx := batch.Prefix(s.name)
		e := map[string]string{
			pfx + "_INPUT_DIR": t.TempDir(), pfx + "_OUT_DIR": t.TempDir(), pfx + "_WORK_DIR": t.TempDir(),
			// gore resolves its .reb definition at discovery time; the image
			// default /batch/default.reb does not exist on a test host.
			"GORE_BATCH": goreBatch(t),
		}
		var buf bytes.Buffer
		code := run([]string{s.name}, env(e), &buf)
		if code != 1 {
			t.Fatalf("%s: empty-tree exit %d: %s", s.name, code, buf.String())
		}
		var sum map[string]any
		json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &sum)
		if sum["tool"] != s.name || sum["status"] != "nothing" {
			t.Fatalf("%s: summary %v", s.name, sum)
		}
	}
}

func TestUnknownAndUsage(t *testing.T) {
	var buf bytes.Buffer
	if code := run([]string{"gonosuch"}, env(nil), &buf); code != 2 {
		t.Fatalf("unknown subtool exit %d", code)
	}
	// a stream/model word is not part of the calling vocabulary — only
	// sub-tool names are accepted.
	if code := run([]string{"process"}, env(nil), &buf); code != 2 {
		t.Fatalf("stream word accepted: exit %d", code)
	}
	buf.Reset()
	if code := run([]string{"--version"}, env(nil), &buf); code != 0 || !strings.HasPrefix(buf.String(), "gowindowlicker ") {
		t.Fatalf("version: %d %q", code, buf.String())
	}
	buf.Reset()
	if code := run([]string{"--print-contract"}, env(nil), &buf); code != 0 ||
		!strings.Contains(buf.String(), "tool: gowindowlicker") || !strings.Contains(buf.String(), "entrypoint:") {
		t.Fatalf("print-contract: %d", code)
	}
}

// TestBareIsLick: no arguments IS the default run — every parser, the sweep.
// (Here /input is absent, so it lands on config_error, but it lands there as
// a sweep: one summary line carrying all twelve sub-tool summaries.)
func TestBareIsLick(t *testing.T) {
	var buf bytes.Buffer
	code := run(nil, env(map[string]string{
		"GOWINDOWLICKER_INPUT_DIR": filepath.Join(t.TempDir(), "missing"),
		"GOWINDOWLICKER_OUT_DIR":   t.TempDir(),
		"GOWINDOWLICKER_WORK_DIR":  t.TempDir(),
	}), &buf)
	if code != 2 {
		t.Fatalf("bare run with missing input: exit %d", code)
	}
	var sum lickSummary
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &sum); err != nil {
		t.Fatalf("bare run printed no summary: %v", err)
	}
	if sum.Tool != "gowindowlicker" || len(sum.Subtools) != len(subs) {
		t.Fatalf("bare run is not the all-parsers sweep: %+v", sum)
	}
}

// TestLickSweep: the default run walks one evidence tree with every parser,
// each into its own <OUT_DIR>/<subtool>/ tree, parsers whose artefact class
// is absent report "nothing" without failing the sweep, and a rerun is
// idempotent.
func TestLickSweep(t *testing.T) {
	in, out := t.TempDir(), t.TempDir()
	rbDir := filepath.Join(in, "$Recycle.Bin", "S-1-5-21-1")
	if err := os.MkdirAll(rbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(rbDir, "_IQ3K5N7.docx"), v2Record(`C:\Users\x\gone.docx`), 0o644)
	lnk, err := os.ReadFile(filepath.Join("le", "testdata", "sample.lnk"))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(in, "sample.lnk"), lnk, 0o644)

	e := map[string]string{
		"GOWINDOWLICKER_INPUT_DIR": in, "GOWINDOWLICKER_OUT_DIR": out,
		"GOWINDOWLICKER_WORK_DIR": t.TempDir(), "GORE_BATCH": goreBatch(t),
	}
	var buf bytes.Buffer
	code := run([]string{"lick"}, env(e), &buf)
	if code != 0 {
		t.Fatalf("sweep exit %d: %s", code, buf.String())
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("stdout is not one line: %d", len(lines))
	}
	var sum lickSummary
	if err := json.Unmarshal([]byte(lines[0]), &sum); err != nil {
		t.Fatal(err)
	}
	if sum.Status != "ok" || sum.Tool != "gowindowlicker" || len(sum.Subtools) != len(subs) {
		t.Fatalf("aggregate: %+v", sum)
	}
	if sum.Records < 2 {
		t.Fatalf("records: %d", sum.Records)
	}
	for _, want := range []string{
		filepath.Join(out, "gorb", "$Recycle.Bin_S-1-5-21-1__IQ3K5N7.docx", "gorb.jsonl"),
		filepath.Join(out, "gole", "sample.lnk", "gole.jsonl"),
	} {
		if _, err := os.Stat(want); err != nil {
			t.Fatalf("sweep output missing: %v", err)
		}
	}

	// rerun: idempotent — the processed items are skipped, nothing rewritten
	buf.Reset()
	if code := run(nil, env(e), &buf); code != 0 {
		t.Fatalf("rerun exit %d: %s", code, buf.String())
	}
	var again lickSummary
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &again); err != nil {
		t.Fatal(err)
	}
	if again.Processed != 0 || again.Skipped != sum.Processed {
		t.Fatalf("rerun not idempotent: %+v", again)
	}
}

// TestRegistryConsistent: every registered sub-tool keeps its canonical
// go-name (the record files and env prefixes byakugan and the pipeline
// already consume), and the registry order is deterministic and complete.
func TestRegistryConsistent(t *testing.T) {
	want := []string{"goprefetch", "goese", "gorb", "gomft", "goamcache", "goappcompat",
		"goevtx", "gore", "gosbe", "gole", "gojle", "gowxt"}
	if len(subs) != len(want) {
		t.Fatalf("registry size %d, want %d", len(subs), len(want))
	}
	for i, s := range subs {
		if s.name != want[i] {
			t.Fatalf("registry[%d] = %q, want %q", i, s.name, want[i])
		}
		if s.tool.Name != s.name {
			t.Fatalf("%s: batch binding names %q", s.name, s.tool.Name)
		}
		if s.main == nil {
			t.Fatalf("%s: no argv Main", s.name)
		}
	}
}
