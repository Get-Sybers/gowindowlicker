package prefetch

import (
	"archive/tar"
	"bytes"
	"testing"
)

// tarEntry is one file to place in a synthetic tar for the --tar tests.
type tarEntry struct {
	name string
	body []byte
	mode int64
}

// makeTar builds an in-memory tar of the given entries, mirroring gomount's
// stream: regular-file entries whose Size is the body length. mode 0 becomes the
// 0400 gomount writes.
func makeTar(t *testing.T, entries ...tarEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		mode := e.mode
		if mode == 0 {
			mode = 0o400
		}
		if err := tw.WriteHeader(&tar.Header{
			Name:     e.name,
			Size:     int64(len(e.body)),
			Mode:     mode,
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestParseTarStreamSelectsAndCounts covers the --tar plumbing without a
// committed artefact: a non-.pf entry is skipped and not counted, a .pf-named
// entry whose bytes are not prefetch is counted as a failure, and the stream
// keeps going. The prefetch decode itself is go-prefetch's, exercised by that
// library's own tests.
func TestParseTarStreamSelectsAndCounts(t *testing.T) {
	tarball := makeTar(t,
		tarEntry{name: "Windows/System32/kernel32.dll", body: []byte("not prefetch")},
		tarEntry{name: "Windows/Prefetch/BOGUS.EXE-DEADBEEF.pf", body: []byte("MAM\x04garbage")},
	)
	var got []*record
	parsed, failed, err := parseTarStream(bytes.NewReader(tarball), func(r *record) error {
		got = append(got, r)
		return nil
	}, true)
	if err != nil {
		t.Fatalf("parseTarStream: %v", err)
	}
	if parsed != 0 {
		t.Fatalf("parsed=%d, want 0 (no valid .pf)", parsed)
	}
	if failed != 1 {
		t.Fatalf("failed=%d, want 1 (the garbage .pf; the .dll is skipped uncounted)", failed)
	}
	if len(got) != 0 {
		t.Fatalf("emitted %d records, want 0", len(got))
	}
}

// TestParseTarStreamEmpty confirms an empty tar is not an error.
func TestParseTarStreamEmpty(t *testing.T) {
	parsed, failed, err := parseTarStream(bytes.NewReader(makeTar(t)), func(*record) error { return nil }, true)
	if err != nil || parsed != 0 || failed != 0 {
		t.Fatalf("empty tar: parsed=%d failed=%d err=%v, want 0/0/nil", parsed, failed, err)
	}
}
