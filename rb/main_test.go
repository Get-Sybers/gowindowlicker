package rb

import (
	"archive/tar"
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unicode/utf16"
)

// filetimeOf is the inverse of filetimeToTime — build a FILETIME for a known
// instant so the round-trip is exact.
func filetimeOf(t time.Time) int64 {
	const ticksPerSecond = 10_000_000
	const epochGap = 11644473600
	return (t.Unix()+epochGap)*ticksPerSecond + int64(t.Nanosecond())/100
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func utf16le(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, len(u)*2)
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[i*2:], c)
	}
	return b
}

func makeV1(size int64, deleted time.Time, path string) []byte {
	b := make([]byte, 24+520)
	binary.LittleEndian.PutUint64(b[0:8], 1)
	binary.LittleEndian.PutUint64(b[8:16], uint64(size))
	binary.LittleEndian.PutUint64(b[16:24], uint64(filetimeOf(deleted)))
	copy(b[24:24+520], utf16le(path)) // remainder stays NUL — fixed 260-wchar field
	return b
}

func makeV2(size int64, deleted time.Time, path string) []byte {
	name := append(utf16le(path), 0, 0) // NUL-terminated
	nameLen := len(name) / 2            // wchar count incl NUL
	b := make([]byte, 28+len(name))
	binary.LittleEndian.PutUint64(b[0:8], 2)
	binary.LittleEndian.PutUint64(b[8:16], uint64(size))
	binary.LittleEndian.PutUint64(b[16:24], uint64(filetimeOf(deleted)))
	binary.LittleEndian.PutUint32(b[24:28], uint32(nameLen))
	copy(b[28:], name)
	return b
}

func TestParseV1(t *testing.T) {
	when := time.Date(2018, 1, 30, 15, 30, 7, 0, time.UTC)
	p := filepath.Join(t.TempDir(), "$IABCDEF.txt")
	if err := os.WriteFile(p, makeV1(78706, when, `C:\Users\jo\secret.docx`), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := parseOne(p)
	if err != nil {
		t.Fatalf("parseOne: %v", err)
	}
	if rec.FileName != `C:\Users\jo\secret.docx` {
		t.Errorf("FileName = %q", rec.FileName)
	}
	if rec.FileSize != 78706 {
		t.Errorf("FileSize = %d", rec.FileSize)
	}
	if rec.DeletedOn != "2018-01-30T15:30:07Z" {
		t.Errorf("DeletedOn = %q", rec.DeletedOn)
	}
	if rec.FileType != "$I" {
		t.Errorf("FileType = %q", rec.FileType)
	}
}

func TestParseV2(t *testing.T) {
	when := time.Date(2020, 9, 16, 13, 14, 30, 0, time.UTC)
	p := filepath.Join(t.TempDir(), "$I123456")
	if err := os.WriteFile(p, makeV2(1024, when, `D:\data\こんにちは.bin`), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := parseOne(p)
	if err != nil {
		t.Fatalf("parseOne: %v", err)
	}
	if rec.FileName != `D:\data\こんにちは.bin` { // UTF-16 incl. non-ASCII survives
		t.Errorf("FileName = %q", rec.FileName)
	}
	if rec.FileSize != 1024 {
		t.Errorf("FileSize = %d", rec.FileSize)
	}
	if rec.DeletedOn != "2020-09-16T13:14:30Z" {
		t.Errorf("DeletedOn = %q", rec.DeletedOn)
	}
}

func TestParseRejectsUnknownVersionAndTruncation(t *testing.T) {
	dir := t.TempDir()
	// unknown version
	bad := make([]byte, 544)
	binary.LittleEndian.PutUint64(bad[0:8], 99)
	badPath := filepath.Join(dir, "$IBAD")
	mustWrite(t, badPath, bad)
	if _, err := parseOne(badPath); err == nil {
		t.Error("expected error for unknown version")
	}
	// truncated v2 (name length overruns the file)
	v2 := make([]byte, 28+4)
	binary.LittleEndian.PutUint64(v2[0:8], 2)
	binary.LittleEndian.PutUint32(v2[24:28], 999)
	truncPath := filepath.Join(dir, "$ITRUNC")
	mustWrite(t, truncPath, v2)
	if _, err := parseOne(truncPath); err == nil {
		t.Error("expected error for overrunning v2 name length")
	}
}

// tarEntry is one regular-file entry for tarOf: name = the file's volume path (as
// gomount emits it), body = the file's bytes.
type tarEntry struct {
	name string
	body []byte
}

func entry(name string, body []byte) tarEntry { return tarEntry{name, body} }

// tarOf builds an in-memory tar of the given entries — the shape `gomount stream`
// emits (one regular-file entry per file, entry name = the file's volume path).
func tarOf(t *testing.T, entries ...tarEntry) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		if err := tw.WriteHeader(&tar.Header{
			Name:     e.name,
			Size:     int64(len(e.body)),
			Mode:     0o400,
			Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatalf("write header %s: %v", e.name, err)
		}
		if _, err := tw.Write(e.body); err != nil {
			t.Fatalf("write body %s: %v", e.name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	return &buf
}

func TestParseTarStreamPicksRecordsAndSkipsSiblings(t *testing.T) {
	when := time.Date(2021, 6, 1, 8, 15, 0, 0, time.UTC)
	// The $Recycle.Bin/* glob carries the $I records alongside the $R payloads
	// (whole deleted files) and desktop.ini; only the $I records are parsed, and
	// they are detected by header so a raw-mount "$I" and a Plaso-renamed "_I"
	// both parse regardless of name.
	tr := tarOf(t,
		entry(`$Recycle.Bin/S-1-5-21/$IAAAAAA.docx`, makeV2(4096, when, `C:\Users\jo\report.docx`)),
		entry(`$Recycle.Bin/S-1-5-21/_IBBBBBB.lnk`, makeV1(20, when, `C:\b.lnk`)),
		entry(`$Recycle.Bin/S-1-5-21/$RAAAAAA.docx`, []byte("the actual deleted file bytes, not a $I header")),
		entry(`$Recycle.Bin/S-1-5-21/desktop.ini`, []byte("[.ShellClassInfo]\n")),
	)

	var got []*record
	emitted, failed, err := parseTarStream(tr, func(r *record) error {
		got = append(got, r)
		return nil
	})
	if err != nil {
		t.Fatalf("parseTarStream: %v", err)
	}
	if failed != 0 {
		t.Errorf("failed = %d, want 0", failed)
	}
	if emitted != 2 || len(got) != 2 {
		t.Fatalf("emitted %d records, want 2 ($I only): %+v", emitted, got)
	}
	// SourceName is the tar entry name verbatim (the file's volume path).
	if got[0].SourceName != `$Recycle.Bin/S-1-5-21/$IAAAAAA.docx` {
		t.Errorf("SourceName = %q", got[0].SourceName)
	}
	if got[0].FileType != "$I" {
		t.Errorf("FileType = %q", got[0].FileType)
	}
	if got[0].FileName != `C:\Users\jo\report.docx` || got[0].FileSize != 4096 {
		t.Errorf("record[0] = %+v", got[0])
	}
	if got[0].DeletedOn != "2021-06-01T08:15:00Z" {
		t.Errorf("DeletedOn = %q", got[0].DeletedOn)
	}
	if got[1].FileName != `C:\b.lnk` {
		t.Errorf("record[1] FileName = %q", got[1].FileName)
	}
}

func TestParseTarStreamCountsPerFileErrorsAndKeepsGoing(t *testing.T) {
	when := time.Date(2022, 2, 2, 2, 2, 2, 0, time.UTC)
	// A $I header whose v2 name length overruns the entry is a per-file failure;
	// the good record before and after it still parse.
	badV2 := make([]byte, 28)
	binary.LittleEndian.PutUint64(badV2[0:8], 2)
	binary.LittleEndian.PutUint32(badV2[24:28], 999) // name overruns the 28-byte entry
	tr := tarOf(t,
		entry("$IGOOD1", makeV2(1, when, `C:\ok1`)),
		entry("$IBAD", badV2),
		entry("$IGOOD2", makeV1(2, when, `C:\ok2`)),
	)

	emitted, failed, err := parseTarStream(tr, func(*record) error { return nil })
	if err != nil {
		t.Fatalf("parseTarStream: %v", err)
	}
	if emitted != 2 {
		t.Errorf("emitted = %d, want 2", emitted)
	}
	if failed != 1 {
		t.Errorf("failed = %d, want 1", failed)
	}
}

func TestDirScanDetectsRecordsByHeaderNotName(t *testing.T) {
	dir := t.TempDir()
	when := time.Date(2025, 3, 18, 5, 40, 22, 0, time.UTC)
	// a raw-mount $I name, a Plaso-renamed $ -> _ name, and non-$I files that
	// share the tree (desktop.ini, a $R payload, an unrelated file)
	mustWrite(t, filepath.Join(dir, "$IRAWNAME.docx"), makeV2(10, when, `C:\a.docx`))
	mustWrite(t, filepath.Join(dir, "_IPLASO.lnk"), makeV1(20, when, `C:\b.lnk`)) // plaso rename
	mustWrite(t, filepath.Join(dir, "desktop.ini"), []byte("[.ShellClassInfo]\n"))
	mustWrite(t, filepath.Join(dir, "$RPAYLOAD.docx"), []byte("real file contents, not a header"))
	mustWrite(t, filepath.Join(dir, "notes.txt"), []byte("nope"))

	files, err := collectInputs("", dir)
	if err != nil {
		t.Fatal(err)
	}
	// collectInputs returns every file; the header check picks the two records
	var detected []string
	for _, p := range files {
		if peekLooksLikeRecord(p) {
			if _, err := parseOne(p); err != nil {
				t.Errorf("header-detected %s but parse failed: %v", p, err)
			}
			detected = append(detected, filepath.Base(p))
		}
	}
	if len(detected) != 2 {
		t.Errorf("detected %d $I records, want 2 ($IRAWNAME.docx + _IPLASO.lnk): %v", len(detected), detected)
	}
}
