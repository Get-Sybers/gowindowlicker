package origin

import (
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf16"

	batch "github.com/Get-Sybers/gopinfo/framework"
	"github.com/Get-Sybers/gopinfo/record"
)

// softwareRel is where the SOFTWARE hive sits in a staged Windows tree.
var softwareRel = filepath.Join("Windows", "System32", "config", "SOFTWARE")

// writeManifest writes a gomount v0.4.0 materialise.jsonl into root: a path-less
// metadata row carrying the image-level identity, then one per-file row carrying
// the volume-scoped identity, exactly as `gomount materialise --manifest --hash`
// emits them.
func writeManifest(t *testing.T, root string, rows ...map[string]any) {
	t.Helper()
	f, err := os.Create(filepath.Join(root, record.ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	for _, r := range rows {
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
}

// stageHive writes data as the staged SOFTWARE hive under root.
func stageHive(t *testing.T, root string, data []byte) string {
	t.Helper()
	p := filepath.Join(root, softwareRel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHookStampsManifestAndHost(t *testing.T) {
	root := t.TempDir()
	stageHive(t, root, buildSoftwareHive("11111111-2222-3333-4444-555555555555"))
	writeManifest(t, root,
		// image-level metadata row (path-less): what gomount --hash records.
		map[string]any{
			"image": "disk.E01", "disk_serial": "WD-WX11A", "disk_id": "DISKGUID-01",
			"image_sha256": "a1b2c3",
		},
		// a volume-scoped per-file row for the staged SOFTWARE hive.
		map[string]any{
			"path": "/Windows/System32/config/SOFTWARE", "image": "disk.E01",
			"volume": "p2", "fsuuid": "A1B2-C3D4", "label": "OS",
			"volume_guid": "VOLGUID-02", "part_guid": "PARTGUID-02", "part_offset": 1048576,
			"inode": 55,
		},
	)

	st := Hook(&batch.Config{InputDir: root}, filepath.Join(root, softwareRel), "")

	if st.Origin == nil {
		t.Fatal("Origin not stamped")
	}
	o := st.Origin
	for _, c := range []struct{ name, got, want string }{
		{"Image", o.Image, "disk.E01"},
		{"Volume", o.Volume, "p2"},
		{"FSUUID", o.FSUUID, "A1B2-C3D4"},
		{"Label", o.Label, "OS"},
		{"ImageSHA256", o.ImageSHA256, "a1b2c3"},
		{"DiskSerial", o.DiskSerial, "WD-WX11A"},
		{"DiskID", o.DiskID, "DISKGUID-01"},
		{"VolumeGUID", o.VolumeGUID, "VOLGUID-02"},
		{"PartGUID", o.PartGUID, "PARTGUID-02"},
		{"Path", o.Path, "/Windows/System32/config/SOFTWARE"},
	} {
		if c.got != c.want {
			t.Errorf("Origin.%s = %q, want %q", c.name, c.got, c.want)
		}
	}
	if o.PartOffset != 1048576 {
		t.Errorf("Origin.PartOffset = %d, want 1048576", o.PartOffset)
	}
	if o.Inode != 55 {
		t.Errorf("Origin.Inode = %d, want 55", o.Inode)
	}
	if st.Host == nil || st.Host.MachineID != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("host.id = %+v, want MachineID 11111111-2222-3333-4444-555555555555", st.Host)
	}
}

func TestHookNoProvenance(t *testing.T) {
	// A loose tree: no manifest, no SOFTWARE hive. The stamp is empty, so rows
	// look exactly as they did before this change.
	st := Hook(&batch.Config{InputDir: t.TempDir()}, "whatever.pf", "")
	if st.Origin != nil || st.Host != nil {
		t.Fatalf("expected the zero stamp for a loose tree, got %+v", st)
	}
}

func TestHookHostWithoutManifest(t *testing.T) {
	// A live-style collection stages the SOFTWARE hive but writes no manifest:
	// host.id is read, Origin stays absent.
	root := t.TempDir()
	stageHive(t, root, buildSoftwareHive("abcdef00-0000-0000-0000-000000000001"))
	st := Hook(&batch.Config{InputDir: root}, filepath.Join(root, softwareRel), "")
	if st.Origin != nil {
		t.Errorf("Origin = %+v, want nil without a manifest", st.Origin)
	}
	if st.Host == nil || st.Host.MachineID != "abcdef00-0000-0000-0000-000000000001" {
		t.Errorf("host.id = %+v, want MachineID abcdef00-0000-0000-0000-000000000001", st.Host)
	}
}

func TestHostHiveCaseInsensitive(t *testing.T) {
	// A staged tree keeps the volume's own casing; the hive must still be found.
	root := t.TempDir()
	p := filepath.Join(root, "windows", "system32", "Config", "software")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, buildSoftwareHive("deadbeef-0000-0000-0000-00000000cafe"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readHost(root); got == nil || got.MachineID != "deadbeef-0000-0000-0000-00000000cafe" {
		t.Fatalf("readHost found %+v, want MachineID deadbeef-0000-0000-0000-00000000cafe", got)
	}
}

func TestReadHostAbsentOrInvalid(t *testing.T) {
	// No hive at all.
	if got := readHost(t.TempDir()); got != nil {
		t.Errorf("readHost on an empty tree = %+v, want nil", got)
	}
	// A file at the hive path that is not a registry hive: graceful nil, no panic.
	root := t.TempDir()
	stageHive(t, root, []byte("not a hive"))
	if got := readHost(root); got != nil {
		t.Errorf("readHost on a non-hive = %+v, want nil", got)
	}
}

func TestIdentityMarshal(t *testing.T) {
	type row struct {
		Name string `json:"Name"`
		Identity
	}
	// Unset: Origin and Host are omitted entirely.
	b, err := json.Marshal(&row{Name: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"Name":"x"}` {
		t.Errorf("unset row = %s, want {\"Name\":\"x\"}", b)
	}
	// Set: both ride the row at the top level.
	r := &row{Name: "x"}
	r.Set(record.Stamp{Origin: &record.Origin{DiskSerial: "s"}, Host: &record.Host{MachineID: "m"}})
	b, err = json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"Name":"x","Origin":{"DiskSerial":"s"},"Host":{"MachineID":"m"}}`
	if string(b) != want {
		t.Errorf("set row = %s, want %s", b, want)
	}
}

// ---- a minimal SOFTWARE hive for the host.id read -----------------------------
//
// buildSoftwareHive writes a regf hive holding exactly
// HKLM\SOFTWARE\Microsoft\Cryptography\MachineGuid = guid (REG_SZ) — enough for
// the velocidex regparser to walk, so the test needs no committed binary fixture.
// The cell offsets below are relative to the hive-bins data (0x1000); the layout
// is root → Microsoft → Cryptography, each via a one-element lh index, then the
// value list, the vk and its data cell.

func buildSoftwareHive(guid string) []byte {
	data := utf16leZ(guid)

	// Cell sizes are fixed by their payloads, 8-byte aligned; offsets follow in
	// sequence from 32 (just past the 32-byte hbin header).
	rootNK := 32
	lhRoot := rootNK + cellLen(nkPayload("ROOT", 1, 0, 0, 0))
	msNK := lhRoot + cellLen(lhPayload(0))
	lhMS := msNK + cellLen(nkPayload("Microsoft", 1, 0, 0, 0))
	cryptoNK := lhMS + cellLen(lhPayload(0))
	vList := cryptoNK + cellLen(nkPayload("Cryptography", 0, 0, 1, 0))
	vk := vList + cellLen(make([]byte, 4))
	dataOff := vk + cellLen(vkPayload("MachineGuid", uint32(len(data)), 0))

	buf := make([]byte, 0x2000) // 4 KiB base block + one 4 KiB hbin
	copy(buf, "regf")
	put32(buf, 4, 1)           // Sequence1
	put32(buf, 8, 1)           // Sequence2
	put32(buf, 20, 1)          // Major
	put32(buf, 24, 3)          // Minor
	put32(buf, 36, 32)         // RootCell (rel offset of the root nk)
	put32(buf, 40, 0x1000)     // Length (hive-bins data size)
	copy(buf[0x1000:], "hbin") // hbin header
	put32(buf, 0x1000+8, 0x1000)

	put := func(off int, payload []byte) { copy(buf[0x1000+off:], wrapCell(payload)) }
	put(rootNK, nkPayload("ROOT", 1, uint32(lhRoot), 0, 0))
	put(lhRoot, lhPayload(uint32(msNK)))
	put(msNK, nkPayload("Microsoft", 1, uint32(lhMS), 0, 0))
	put(lhMS, lhPayload(uint32(cryptoNK)))
	put(cryptoNK, nkPayload("Cryptography", 0, 0, 1, uint32(vList)))
	vl := make([]byte, 4)
	binary.LittleEndian.PutUint32(vl, uint32(vk))
	put(vList, vl)
	put(vk, vkPayload("MachineGuid", uint32(len(data)), uint32(dataOff)))
	put(dataOff, data)
	return buf
}

func utf16leZ(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 0, len(u)*2+2)
	for _, r := range u {
		b = append(b, byte(r), byte(r>>8))
	}
	return append(b, 0, 0) // NUL terminator
}

func put32(b []byte, off int, v uint32) { binary.LittleEndian.PutUint32(b[off:], v) }

func cellLen(payload []byte) int { return (4 + len(payload) + 7) &^ 7 }

func wrapCell(payload []byte) []byte {
	b := make([]byte, cellLen(payload))
	binary.LittleEndian.PutUint32(b, uint32(int32(-len(b)))) // negative size = allocated
	copy(b[4:], payload)
	return b
}

func nkPayload(name string, subCount, subList, valCount, valList uint32) []byte {
	p := make([]byte, 76+len(name))
	copy(p, "nk")
	binary.LittleEndian.PutUint16(p[2:], 0x20) // KEY_COMP_NAME
	put32(p, 20, subCount)
	put32(p, 28, subList)
	put32(p, 36, valCount)
	put32(p, 40, valList)
	binary.LittleEndian.PutUint16(p[72:], uint16(len(name)))
	copy(p[76:], name)
	return p
}

func lhPayload(nodeOffset uint32) []byte {
	p := make([]byte, 12)
	copy(p, "lh")
	binary.LittleEndian.PutUint16(p[2:], 1) // one element
	put32(p, 4, nodeOffset)
	return p
}

func vkPayload(name string, dataLen, dataOffset uint32) []byte {
	p := make([]byte, 20+len(name))
	copy(p, "vk")
	binary.LittleEndian.PutUint16(p[2:], uint16(len(name)))
	put32(p, 4, dataLen)
	put32(p, 8, dataOffset)
	put32(p, 12, 1)                          // Type = REG_SZ
	binary.LittleEndian.PutUint16(p[16:], 1) // VALUE_COMP_NAME
	copy(p[20:], name)
	return p
}
