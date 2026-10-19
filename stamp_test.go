package main

// stamp_test proves the evidence-asset identity wiring end to end through the
// batch runtime: a stub gomount (GOMOUNT_BIN) stages a Recycle Bin $I record AND
// writes a gomount-style materialise.jsonl beside it; the gopinfo v0.3.0 Tool.Stamp
// hook (origin.Hook) then joins that manifest onto the row gorb emits, so the
// JSONL row carries an Origin with the frozen identity keys. It is the stub-gomount
// analogue of the real end-to-end run against gomount v0.4.0.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Get-Sybers/gopinfo/record"
)

// gomountStubManifest writes a stub gomount that stages the $I record under
// --out and, like `gomount materialise --manifest --hash`, drops a
// materialise.jsonl: a path-less metadata row with the image-level identity and
// a per-file row with the volume-scoped identity for the staged record.
func gomountStubManifest(t *testing.T) string {
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
while [ $# -gt 0 ]; do
  case "$1" in
    --out) out="$2"; shift ;;
    --set) shift ;;
    --select) shift ;;
  esac
  shift
done
[ -n "$out" ] || exit 2
mkdir -p "$out/\$Recycle.Bin/S-1-5-21-1"
cp "$STUB_SRC" "$out/\$Recycle.Bin/S-1-5-21-1/\$I3XKCd7.pdf"
cat > "$out/materialise.jsonl" <<'MANIFEST'
{"path":"","image":"disk.vmdk","image_sha256":"a1b2c3d4","disk_serial":"WD-WX11A","disk_id":"DISKGUID-01"}
{"path":"/$Recycle.Bin/S-1-5-21-1/$I3XKCd7.pdf","image":"disk.vmdk","volume":"p1","volume_guid":"VOLGUID-02","part_guid":"PARTGUID-02","part_offset":1048576}
MANIFEST
echo '{"tool":"gomount","subtool":"materialise","status":"ok","exit":0}'
`
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return stub
}

func TestSubtoolStampsOriginFromManifest(t *testing.T) {
	stub := gomountStubManifest(t)
	in, out, work := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(in, "disk.vmdk"), []byte("KDMV"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	code := run([]string{"gorb"}, env(map[string]string{
		"GOMOUNT_BIN":    stub,
		"GORB_INPUT_DIR": in, "GORB_OUT_DIR": out, "GORB_WORK_DIR": work, "GORB_IMAGE": "disk.vmdk",
	}), &buf)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, buf.String())
	}

	matches, _ := filepath.Glob(filepath.Join(out, "disk.vmdk", "*", "gorb.jsonl"))
	if len(matches) != 1 {
		t.Fatalf("gorb output: %v", matches)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	var row struct {
		FileName string         `json:"FileName"`
		Origin   *record.Origin `json:"Origin"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(data), &row); err != nil {
		t.Fatalf("decode row: %v: %s", err, data)
	}
	if row.Origin == nil {
		t.Fatalf("row carries no Origin: %s", data)
	}
	o := row.Origin
	for _, c := range []struct{ name, got, want string }{
		{"Image", o.Image, "disk.vmdk"},
		{"Volume", o.Volume, "p1"},
		{"ImageSHA256", o.ImageSHA256, "a1b2c3d4"}, // from the metadata row
		{"DiskSerial", o.DiskSerial, "WD-WX11A"},   // from the metadata row
		{"DiskID", o.DiskID, "DISKGUID-01"},        // from the metadata row
		{"VolumeGUID", o.VolumeGUID, "VOLGUID-02"}, // volume-scoped, per-file row
		{"PartGUID", o.PartGUID, "PARTGUID-02"},
		{"Path", o.Path, "/$Recycle.Bin/S-1-5-21-1/$I3XKCd7.pdf"},
	} {
		if c.got != c.want {
			t.Errorf("Origin.%s = %q, want %q", c.name, c.got, c.want)
		}
	}
	if o.PartOffset != 1048576 {
		t.Errorf("Origin.PartOffset = %d, want 1048576", o.PartOffset)
	}
}
