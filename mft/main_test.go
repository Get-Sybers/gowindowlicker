package mft

import (
	"archive/tar"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"github.com/get-sybers/gopinfo/tstamp"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTSZeroAndValue(t *testing.T) {
	if got := tstamp.RFC3339Nano(time.Time{}); got != "" {
		t.Errorf("zero time -> %q, want empty", got)
	}
	when := time.Date(2025, 2, 24, 19, 24, 9, 0, time.UTC)
	if got := tstamp.RFC3339Nano(when); got != "2025-02-24T19:24:09Z" {
		t.Errorf("ts = %q", got)
	}
}

func TestCsvRowMatchesHeaderLen(t *testing.T) {
	r := &record{}
	if len(r.csvRow()) != len(csvHeader) {
		t.Errorf("csvRow has %d cols, header has %d", len(r.csvRow()), len(csvHeader))
	}
}

func TestLooksLikeMFTByHeader(t *testing.T) {
	dir := t.TempDir()
	// a $MFT / plaso-renamed _MFT begins with the "FILE" record signature
	mft := filepath.Join(dir, "_MFT")
	if err := os.WriteFile(mft, append([]byte("FILE"), make([]byte, 1020)...), 0o644); err != nil {
		t.Fatal(err)
	}
	if !looksLikeMFT(mft) {
		t.Error("FILE-signature file not detected as $MFT")
	}
	// a registry hive ("regf") or short file is not an $MFT
	hive := filepath.Join(dir, "SYSTEM")
	if err := os.WriteFile(hive, []byte("regf....."), 0o644); err != nil {
		t.Fatal(err)
	}
	if looksLikeMFT(hive) {
		t.Error("non-$MFT file wrongly detected")
	}
	tiny := filepath.Join(dir, "x")
	if err := os.WriteFile(tiny, []byte("FI"), 0o644); err != nil {
		t.Fatal(err)
	}
	if looksLikeMFT(tiny) {
		t.Error("too-short file wrongly detected")
	}
}

// parseFile must handle a minimal well-formed 1 KiB FILE record without error
// (0 emitted highlights is fine — the point is the go-ntfs integration parses,
// end-to-end coverage over a real $MFT is in the pipeline validation).
func TestParseMinimalRecordNoError(t *testing.T) {
	rec := make([]byte, 1024)
	copy(rec, "FILE")
	binary.LittleEndian.PutUint16(rec[24:], 1024) // mft_entry_size
	binary.LittleEndian.PutUint16(rec[28:], 1024) // mft_entry_allocated
	p := filepath.Join(t.TempDir(), "_MFT")
	if err := os.WriteFile(p, rec, 0o644); err != nil {
		t.Fatal(err)
	}
	e := &emitter{enc: json.NewEncoder(io.Discard)}
	if _, err := parseFile(p, 1024, 4096, e); err != nil {
		t.Errorf("parseFile on a minimal FILE record: %v", err)
	}
}

// minimalMFT returns a single well-formed 1 KiB $MFT record — the "FILE"
// signature plus the record-size fields go-ntfs reads — the same record
// TestParseMinimalRecordNoError parses from disk.
func minimalMFT() []byte {
	rec := make([]byte, 1024)
	copy(rec, "FILE")
	binary.LittleEndian.PutUint16(rec[24:], 1024) // mft_entry_size
	binary.LittleEndian.PutUint16(rec[28:], 1024) // mft_entry_allocated
	return rec
}

// tarOf builds an in-memory tar of (name, body) pairs, mirroring gomount's
// stream: regular-file entries, mode 0400, Size = body length.
func tarOf(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name:     name,
			Size:     int64(len(body)),
			Mode:     0o400,
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestParseTarStream feeds a tar carrying one $MFT (FILE signature) and one
// non-$MFT file (a registry hive) through parseTarStream, the same end-to-end
// path as `gomount stream --filter '$MFT' <image> | gomft --tar`. It confirms the
// $MFT is buffered, content-detected and parsed while the other entry is skipped,
// and that every counted entry reached the emitter.
func TestParseTarStream(t *testing.T) {
	tarball := tarOf(t, map[string][]byte{
		"$MFT":   minimalMFT(),
		"SYSTEM": []byte("regf"), // a hive, not the FILE signature — skipped
	})

	var out bytes.Buffer
	e := &emitter{enc: json.NewEncoder(&out)}
	parsed, entries, failed, err := parseTarStream(bytes.NewReader(tarball), 1024, 4096, e)
	if err != nil {
		t.Fatalf("parseTarStream: %v", err)
	}
	if parsed != 1 {
		t.Errorf("parsed = %d, want 1 (only the $MFT entry, the hive skipped)", parsed)
	}
	if failed != 0 {
		t.Errorf("failed = %d, want 0", failed)
	}
	// json.Encoder writes one line per record, so the emitted line count must
	// equal the entries parseTarStream reports.
	if emitted := bytes.Count(out.Bytes(), []byte("\n")); emitted != entries {
		t.Errorf("emitted %d records, want entries=%d", emitted, entries)
	}
}

// TestParseTarStreamCorrupt confirms a truncated/garbage tar is fatal (non-nil
// err) rather than silently producing partial output.
func TestParseTarStreamCorrupt(t *testing.T) {
	e := &emitter{enc: json.NewEncoder(io.Discard)}
	junk := bytes.Repeat([]byte{0x7f}, 2048) // not a valid tar header
	if _, _, _, err := parseTarStream(bytes.NewReader(junk), 1024, 4096, e); err == nil {
		t.Error("parseTarStream on garbage input: want a fatal error, got nil")
	}
}
