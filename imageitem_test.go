package main

// imageitem_test proves the parsers run ON a disk image: a stub gomount
// (GOMOUNT_BIN) stands in for the real decoder and drops one Recycle Bin $I
// record where a materialised recyclebin set would put it; the sweep and a
// sub-tool run then find it through the ordinary batch loop, write the
// records under <OUT_DIR>/…/<image>/, and leave no scratch behind.

import (
	"bytes"
	"encoding/json"
	"github.com/Get-Sybers/gopinfo/diskimage"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gomountStub writes an executable that parses --out and stages the record
// file at $STUB_SRC under it; it prints a summary line like the real thing.
func gomountStub(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "record.bin")
	if err := os.WriteFile(src, v2Record(`C:\Users\x\y.pdf`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STUB_SRC", src)
	stub := filepath.Join(dir, "gomount")
	script := `#!/bin/sh
out=""
sets=""
while [ $# -gt 0 ]; do
  case "$1" in
    --out) out="$2"; shift ;;
    --set) sets="$sets $2"; shift ;;
  esac
  shift
done
[ -n "$out" ] || exit 2
mkdir -p "$out/\$Recycle.Bin/S-1-5-21-1"
cp "$STUB_SRC" "$out/\$Recycle.Bin/S-1-5-21-1/\$I3XKCd7.pdf"
printf '%s\n' "$sets" > "$out/.sets"
echo '{"tool":"gomount","subtool":"materialise","status":"ok","exit":0}'
`
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return stub
}

func TestLickRunsOnImage(t *testing.T) {
	stub := gomountStub(t)
	in, out, work := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(in, "disk.vmdk"), []byte("KDMV"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	code := run(nil, env(map[string]string{
		"GOMOUNT_BIN":              stub,
		"GOWINDOWLICKER_INPUT_DIR": in, "GOWINDOWLICKER_OUT_DIR": out,
		"GOWINDOWLICKER_WORK_DIR": work, "GOWINDOWLICKER_IMAGE": "disk.vmdk",
		"GORE_BATCH": goreBatch(t),
	}), &buf)
	var sum lickSummary
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &sum); err != nil {
		t.Fatalf("summary: %v: %s", err, buf.String())
	}
	if code != 0 && code != 3 { // 3: parsers whose set is empty report nothing
		t.Fatalf("exit %d: %s", code, buf.String())
	}
	if len(sum.Images) != 1 || sum.Images[0] != "disk.vmdk" {
		t.Fatalf("images = %v", sum.Images)
	}
	// the record landed one level down: <out>/gorb/<image>/<item>/gorb.jsonl
	matches, _ := filepath.Glob(filepath.Join(out, "gorb", "disk.vmdk", "*", "gorb.jsonl"))
	if len(matches) != 1 {
		t.Fatalf("gorb output under the image host: %v", matches)
	}
	if strays, _ := filepath.Glob(filepath.Join(out, "gorb", "*", "gorb.jsonl")); len(strays) != 0 {
		t.Fatalf("records at the loose level though an image was selected: %v", strays)
	}
	// the scratch tree is gone
	entries, _ := os.ReadDir(work)
	if len(entries) != 0 {
		t.Fatalf("scratch left behind: %v", entries)
	}
	// the sweep pulled the one union set
	for _, ss := range sum.Subtools {
		if ss.Subtool != "disk.vmdk" {
			t.Fatalf("sub-run not attributed to the image: %+v", ss)
		}
	}
}

func TestSubtoolRunsOnImage(t *testing.T) {
	stub := gomountStub(t)
	in, out, work := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(in, "host.E01"), []byte("EVF"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	code := run([]string{"gorb"}, env(map[string]string{
		"GOMOUNT_BIN":    stub,
		"GORB_INPUT_DIR": in, "GORB_OUT_DIR": out, "GORB_WORK_DIR": work, "GORB_IMAGE": "host.E01",
	}), &buf)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, buf.String())
	}
	matches, _ := filepath.Glob(filepath.Join(out, "host.E01", "*", "gorb.jsonl"))
	if len(matches) != 1 {
		t.Fatalf("gorb output under <out>/<image>/: %v", matches)
	}
	if entries, _ := os.ReadDir(work); len(entries) != 0 {
		t.Fatalf("scratch left behind: %v", entries)
	}
}

func TestImageSelectionRejectsParts(t *testing.T) {
	stub := gomountStub(t)
	in := t.TempDir()
	for _, name := range []string{"disk-flat.vmdk", "disk-s002.vmdk", "host.E02", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(in, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := diskimage.SelectedImages(in, name); err == nil {
			t.Errorf("diskimage.SelectedImages(%q) accepted a part of another image / a non-image", name)
		}
	}
	_ = stub
}

func TestImageSelectionErrors(t *testing.T) {
	stub := gomountStub(t)
	in, out := t.TempDir(), t.TempDir()
	var buf bytes.Buffer
	code := run(nil, env(map[string]string{
		"GOMOUNT_BIN":              stub,
		"GOWINDOWLICKER_INPUT_DIR": in, "GOWINDOWLICKER_OUT_DIR": out,
		"GOWINDOWLICKER_WORK_DIR": t.TempDir(), "GOWINDOWLICKER_IMAGE": "missing.vmdk",
	}), &buf)
	if code != 2 || !strings.Contains(buf.String(), "config_error") {
		t.Fatalf("missing image: exit %d: %s", code, buf.String())
	}
}

func TestImageItemRules(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"disk.vmdk", true}, {"disk-flat.vmdk", false}, {"disk-s001.vmdk", false},
		{"host.E01", true}, {"host.E02", false}, {"host.EAA", false}, {"host.Ex01", true},
		{"vm.vhdx", true}, {"vm.qcow2", true}, {"notes.txt", false},
		{"installer.dmg", true}, {"backup.sparseimage", true}, {"Installer.DMG", true},
	} {
		if got := diskimage.IsImageItem(tc.name); got != tc.want {
			t.Errorf("diskimage.IsImageItem(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
	if got := diskimage.ImageItemName("/in", "/in/VM files/disk 1.vmdk"); got != "VM_files_disk_1.vmdk" {
		t.Errorf("imageItemName: %q", got)
	}
}
