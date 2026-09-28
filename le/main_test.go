package le

import (
	"archive/tar"
	"bytes"
	"github.com/get-sybers/gopinfo/tstamp"
	"os"
	"path/filepath"
	"testing"
	"time"

	lnk "github.com/parsiya/golnk"
)

func TestTsZeroIsEmpty(t *testing.T) {
	if got := tstamp.RFC3339Nano(time.Time{}); got != "" {
		t.Fatalf("zero time should render empty, got %q", got)
	}
}

func TestTsRendersUTCRFC3339Nano(t *testing.T) {
	in := time.Date(2025, 3, 18, 5, 39, 30, 673307900, time.FixedZone("x", 3600))
	got := tstamp.RFC3339Nano(in)
	want := "2025-03-18T04:39:30.6733079Z" // normalised to UTC
	if got != want {
		t.Fatalf("tstamp.RFC3339Nano() = %q, want %q", got, want)
	}
}

func TestSetFlagsSortedAndSetOnly(t *testing.T) {
	fm := lnk.FlagMap{"HasLinkInfo": true, "IsUnicode": true, "HasArguments": false}
	got := setFlags(fm)
	want := "HasLinkInfo, IsUnicode" // sorted, unset dropped
	if got != want {
		t.Fatalf("setFlags() = %q, want %q", got, want)
	}
}

func TestSetFlagsEmpty(t *testing.T) {
	if got := setFlags(lnk.FlagMap{"A": false}); got != "" {
		t.Fatalf("no set flags should render empty, got %q", got)
	}
}

func TestCsvHeaderMatchesRowWidth(t *testing.T) {
	r := &record{}
	if len(r.csvRow()) != len(csvHeader) {
		t.Fatalf("csvRow width %d != header width %d", len(r.csvRow()), len(csvHeader))
	}
}

// tarEntry writes one regular-file entry into tw.
func tarEntry(t *testing.T, tw *tar.Writer, name string, body []byte, mtime time.Time) {
	t.Helper()
	hdr := &tar.Header{
		Name:     name,
		Mode:     0o400,
		Size:     int64(len(body)),
		ModTime:  mtime,
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
}

// TestParseTarStream drives --tar mode over a synthetic `gomount stream` tar: a
// real .lnk entry (asserted target fields), the same bytes under a name with no
// .lnk extension (recognised by the 0x4C magic), a plain-text entry (skipped),
// and a .lnk-named entry of garbage (a counted per-file failure).
func TestParseTarStream(t *testing.T) {
	sample, err := os.ReadFile(filepath.Join("testdata", "sample.lnk"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	mtime := time.Date(2021, 6, 1, 12, 0, 0, 0, time.UTC)

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	tarEntry(t, tw, "Users/Parsia/Recent/sample.lnk", sample, mtime)       // parsed, fields asserted
	tarEntry(t, tw, "Users/Parsia/renamed_4C", sample, mtime)              // no .lnk ext -> magic-detected
	tarEntry(t, tw, "Users/Parsia/notes.txt", []byte("plain text"), mtime) // skipped: no ext, no magic
	tarEntry(t, tw, "Users/Parsia/broken.lnk", []byte("not a lnk"), mtime) // .lnk name, garbage -> failure
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	var got []*record
	parsed, failed, err := parseTarStream(&buf, func(r *record) error {
		got = append(got, r)
		return nil
	}, true)
	if err != nil {
		t.Fatalf("parseTarStream returned fatal error: %v", err)
	}
	if parsed != 2 || failed != 1 {
		t.Fatalf("parsed=%d failed=%d, want parsed=2 failed=1", parsed, failed)
	}
	if len(got) != 2 {
		t.Fatalf("emitted %d records, want 2", len(got))
	}

	rec := got[0]
	if want := "Users/Parsia/Recent/sample.lnk"; rec.SourceFile != want {
		t.Errorf("SourceFile = %q, want %q (the tar entry name)", rec.SourceFile, want)
	}
	if want := `C:\Users\Parsia\AppData\Local\Programs\Microsoft VS Code\Code.exe`; rec.LocalPath != want {
		t.Errorf("LocalPath = %q, want %q", rec.LocalPath, want)
	}
	if want := "2018-10-26T14:58:40.1516068Z"; rec.TargetCreated != want {
		t.Errorf("TargetCreated = %q, want %q", rec.TargetCreated, want)
	}
	if want := "2018-10-26T14:58:40.1516068Z"; rec.TargetModified != want {
		t.Errorf("TargetModified = %q, want %q", rec.TargetModified, want)
	}
	if want := "2018-10-17T04:30:32Z"; rec.TargetAccessed != want {
		t.Errorf("TargetAccessed = %q, want %q", rec.TargetAccessed, want)
	}
	if rec.FileSize != 67665152 {
		t.Errorf("FileSize = %d, want 67665152", rec.FileSize)
	}
	// SourceModified is filled from the tar header mtime; a tar header carries no
	// atime, so SourceAccessed degrades to empty in --tar mode.
	if want := tstamp.RFC3339Nano(mtime); rec.SourceModified != want {
		t.Errorf("SourceModified = %q, want %q (tar header mtime)", rec.SourceModified, want)
	}
	if rec.SourceAccessed != "" {
		t.Errorf("SourceAccessed = %q, want empty (tar carries no atime)", rec.SourceAccessed)
	}

	// the extension-less entry parsed only because hasLnkMagic recognised the 0x4C header
	if got[1].SourceFile != "Users/Parsia/renamed_4C" {
		t.Errorf("second record SourceFile = %q, want the magic-detected entry", got[1].SourceFile)
	}
	if got[1].LocalPath != rec.LocalPath {
		t.Errorf("magic-detected entry LocalPath = %q, want %q", got[1].LocalPath, rec.LocalPath)
	}
}

func TestLooksLikeLnkMagic(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "a.lnk")
	if err := os.WriteFile(good, []byte(lnkMagic+"rest"), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, "b.bin")
	if err := os.WriteFile(bad, []byte("NOPEnope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !looksLikeLnk(good) {
		t.Errorf("expected magic file to be detected as .lnk")
	}
	if looksLikeLnk(bad) {
		t.Errorf("non-magic file must not be detected as .lnk")
	}
	if looksLikeLnk(filepath.Join(dir, "missing")) {
		t.Errorf("missing file must not be detected as .lnk")
	}
}
