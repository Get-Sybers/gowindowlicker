package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func stageFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func lineCount(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.Count(bytes.TrimRight(b, "\n"), []byte("\n")) + 1
}

// mergeFlat collapses the runtime's per-item tree into one <out>/<assetID>.jsonl:
// the first touch of a final file this run truncates, a later same-asset pass
// appends (two acquisitions of one disk merge), and a fresh run rebuilds.
func TestMergeFlat(t *testing.T) {
	out := t.TempDir()
	final := filepath.Join(out, "WD-WX11A.jsonl")
	opened := map[string]bool{}

	// pass 1: two per-item files concatenate into the one flat file
	t1 := t.TempDir()
	stageFile(t, filepath.Join(t1, "a", "gorb.jsonl"), "{\"n\":1}\n")
	stageFile(t, filepath.Join(t1, "b", "gorb.jsonl"), "{\"n\":2}\n")
	outs, err := mergeFlat(t1, out, "WD-WX11A", opened)
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 1 || outs[0] != final {
		t.Fatalf("outs = %v, want [%s]", outs, final)
	}
	if n := lineCount(t, final); n != 2 {
		t.Fatalf("pass 1 lines = %d, want 2", n)
	}

	// pass 2, same asset id: appends — the second acquisition merges, not clobbers
	t2 := t.TempDir()
	stageFile(t, filepath.Join(t2, "c", "gorb.jsonl"), "{\"n\":3}\n")
	if _, err := mergeFlat(t2, out, "WD-WX11A", opened); err != nil {
		t.Fatal(err)
	}
	if n := lineCount(t, final); n != 3 {
		t.Fatalf("same-asset append lines = %d, want 3", n)
	}

	// a fresh run (new opened set) truncates and rebuilds to the same content
	if _, err := mergeFlat(t2, out, "WD-WX11A", map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	if n := lineCount(t, final); n != 1 {
		t.Fatalf("rebuild lines = %d, want 1 (truncate-first-touch)", n)
	}
}

func TestMergeFlatIgnoresPartAndEmpty(t *testing.T) {
	out := t.TempDir()

	// a crashed item the runtime did not rename is not merged
	tp := t.TempDir()
	stageFile(t, filepath.Join(tp, "x", "gorb.jsonl.part"), "{\"torn\":true}\n")
	outs, err := mergeFlat(tp, out, "ASSET", map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 0 {
		t.Fatalf(".part merged: %v", outs)
	}
	if _, err := os.Stat(filepath.Join(out, "ASSET.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("a flat file was written from only a .part: %v", err)
	}

	// an empty temp (the parser matched nothing) writes nothing
	if outs, err := mergeFlat(t.TempDir(), out, "ASSET", map[string]bool{}); err != nil || len(outs) != 0 {
		t.Fatalf("empty temp: outs=%v err=%v", outs, err)
	}
}
