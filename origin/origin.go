// Package origin brands gowindowlicker's records with evidence-asset identity.
//
// Unlike the daemon-side matrix, the Windows parsers each write their own
// bespoke record shape rather than going through a stamping record.Writer, so
// nothing stamped an Origin block onto their rows. gopinfo v0.3.0 added the
// framework Tool.Stamp hook for exactly this case (framework.Tool.Stamp /
// framework.Config.Stamp): the batch runtime resolves one record.Stamp per item
// before Process and hands it to the tool, which brands its own rows.
//
// Hook is that resolver, shared by all twelve parsers. For the item being
// processed it joins:
//
//   - the Origin from the stage manifest gomount writes (`gomount materialise
//     --manifest` → materialise.jsonl, read by gopinfo record.LoadManifest):
//     the image/volume identity plus the frozen evidence-asset keys
//     (ImageSHA256, DiskSerial, DiskID, VolumeGUID, PartGUID, PartOffset); and
//   - the imaged host's own identity — host.id, read as the MachineGuid from the
//     staged SOFTWARE hive (HKLM\SOFTWARE\Microsoft\Cryptography\MachineGuid) —
//     carried on record.Host.MachineID.
//
// Both are resolved once per input root and cached: the sweep runs every parser
// over one image's scratch tree before moving to the next, so a single-entry
// cache spares every item after the first a manifest reparse and a hive read.
// Provenance that is simply absent — a loose evidence tree, a live collection
// with no manifest, an image with no SOFTWARE hive — yields the zero stamp, so
// those rows look exactly as they did before (honest nulls, never fabricated).
package origin

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"www.velocidex.com/golang/regparser"

	batch "github.com/Get-Sybers/gopinfo/framework"
	"github.com/Get-Sybers/gopinfo/record"
)

// Stamp re-exports record.Stamp (what framework Config.Stamp returns) so a
// parser can hold the per-item stamp — e.g. in its emitter — without importing
// the record package, whose name collides with each parser's own `record` type.
type Stamp = record.Stamp

// Identity is the evidence-asset identity branded onto every JSONL row: the
// Origin joined from the gomount stage manifest and the imaged host's own
// identity (host.id via Host.MachineID). It is embedded anonymously in each
// parser's record struct so the two fields marshal at the top level of the row,
// both omitempty — a loose-evidence run, an argv debug pass, or a CSV row
// carries neither, exactly as before.
type Identity struct {
	Origin *record.Origin `json:"Origin,omitempty"`
	Host   *record.Host   `json:"Host,omitempty"`
}

// Set brands the row from the per-item stamp the batch runtime resolved
// (framework Config.Stamp — the gopinfo v0.3.0 Tool.Stamp hook). A zero stamp,
// which is what argv debug mode and loose evidence yield, leaves both fields
// nil, so they are omitted.
func (id *Identity) Set(s record.Stamp) {
	id.Origin, id.Host = s.Origin, s.Host
}

// Hook is the framework Tool.Stamp hook every gowindowlicker parser installs. It
// resolves the Origin (from the stage manifest) and host identity (MachineGuid)
// for one item, both cached per input root. It is safe on any input: an absent
// manifest or hive yields the zero record.Stamp.
func Hook(cfg *batch.Config, item, _ string) record.Stamp {
	r := forInput(cfg.InputDir)
	rel, err := filepath.Rel(cfg.InputDir, item)
	if err != nil {
		rel = item
	}
	// Snapshot and Residue are daemon-side (Linux) concerns; the Windows
	// parsers carry only Origin and Host.
	o, _, _ := r.manifest.Stamp(rel)
	return record.Stamp{Origin: o, Host: r.host}
}

// resolved is the per-input-root provenance, resolved once and reused across the
// many items of one image pass.
type resolved struct {
	manifest *record.Manifest
	host     *record.Host
}

var (
	mu        sync.Mutex
	cachedKey string
	cached    *resolved
)

// forInput resolves (and caches) the manifest and host identity for one input
// root. A single-entry cache keyed by the root bounds memory to one manifest
// while still sparing every item after the first a reparse, because the sweep
// runs every parser over one image's scratch tree before moving to the next.
func forInput(root string) *resolved {
	mu.Lock()
	defer mu.Unlock()
	if cached != nil && cachedKey == root {
		return cached
	}
	cachedKey = root
	cached = &resolved{
		manifest: record.LoadManifest(root),
		host:     readHost(root),
	}
	return cached
}

// readHost reads the MachineGuid from the staged SOFTWARE hive under root and
// returns it as host.id (record.Host.MachineID). nil when the hive is absent
// (not staged, a loose tree) or carries no value.
func readHost(root string) *record.Host {
	hive := resolveHive(root, "Windows", "System32", "config", "SOFTWARE")
	if hive == "" {
		return nil
	}
	guid := machineGUID(hive)
	if guid == "" {
		return nil
	}
	return &record.Host{MachineID: guid}
}

// machineGUID opens the SOFTWARE hive read-only and reads
// HKLM\SOFTWARE\Microsoft\Cryptography\MachineGuid (REG_SZ). The committed hive
// is read without .LOG replay: MachineGuid is written once at install and is
// never a pending transaction. "" when the key or value is absent.
func machineGUID(hivePath string) string {
	f, err := os.Open(hivePath)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	reg, err := regparser.NewRegistry(f)
	if err != nil {
		return ""
	}
	node := reg.OpenKey(`Microsoft\Cryptography`)
	if node == nil {
		return ""
	}
	for _, v := range node.Values() {
		if strings.EqualFold(v.ValueName(), "MachineGuid") {
			if vd := v.ValueData(); vd != nil {
				return cleanSZ(vd.String)
			}
		}
	}
	return ""
}

// cleanSZ trims a REG_SZ value at its first NUL (the string terminator the
// decoder keeps) and strips surrounding whitespace.
func cleanSZ(s string) string {
	if i := strings.IndexByte(s, 0); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// resolveHive walks root/parts... resolving each component case-insensitively
// (a staged NTFS tree keeps the volume's own casing, which varies) and returns
// the full path, or "" if any component is missing.
func resolveHive(root string, parts ...string) string {
	cur := root
	for _, want := range parts {
		if cand := filepath.Join(cur, want); fileExists(cand) {
			cur = cand
			continue
		}
		entries, err := os.ReadDir(cur)
		if err != nil {
			return ""
		}
		next := ""
		for _, e := range entries {
			if strings.EqualFold(e.Name(), want) {
				next = filepath.Join(cur, e.Name())
				break
			}
		}
		if next == "" {
			return ""
		}
		cur = next
	}
	return cur
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
