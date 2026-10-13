// Package live collects a running Windows host's forensic artefacts the same
// shape diskimage materialises a disk image: the files each parser reads are
// pulled out of the live NTFS volume into a scratch tree under the work dir,
// their volume-relative path preserved, and the ordinary batch loop then runs
// over that tree exactly as it would over a mounted image or a loose evidence
// folder. The records land under <OUT_DIR>/<subtool>/live/, the scratch goes;
// nothing is written back to the host and the volume is opened read-only — the
// collection cannot modify the evidence.
//
// The files are read from their on-disk clusters through Velociraptor's go-ntfs
// over a raw volume handle, not through the file system, so the files the OS
// holds open — the registry hives, $MFT, the .evtx logs, SRUDB.dat, the
// per-user NTUSER.DAT — are read straight from the volume and the share locks
// never apply. It is the same go-ntfs that gomft already parses $MFT with and
// the same raw-device paged reader go-ntfs documents for \\.\c:, so live mode
// stays inside the one static binary: no VSS, no external helper, no cgo.
//
// Raw volume reading is a Windows capability (\\.\C:) and needs Administrator;
// on every other OS Collect returns ErrUnsupported. The live analogue of
// diskimage's gomount materialise step, keyed by the same artefact-set names
// (windows-core is the union the sweep pulls).
package live

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	ntfs "www.velocidex.com/golang/go-ntfs/parser"
)

// ErrUnsupported is returned by Collect on any OS but Windows: raw volume
// reading of a live host is a Windows-only capability.
var ErrUnsupported = errors.New("live mode is only supported on Windows (raw volume access)")

// HostDir is the item name a live run writes under — the live analogue of
// diskimage.ImageItemName(<image>). Records land at <OUT_DIR>/<subtool>/live/…
// and a sweep attributes each sub-run's summary to it, exactly as a disk image
// is attributed by its own name.
const HostDir = "live"

// pageSize / pageCache tune the raw-device paged reader go-ntfs wraps the
// volume handle in. The page size is a multiple of 4096 so every read stays
// aligned to both 512e and 4Kn sector geometries (a raw \\.\C: handle rejects
// unaligned reads); the LRU bounds resident pages (64 KiB * 4096 ≈ 256 MiB
// worst case) so pulling a large $MFT does not grow without bound.
const (
	pageSize  = int64(64 * 1024)
	pageCache = 4096
)

// artefactSets maps each parser's gomount-style set name to the volume-relative
// paths it collects, '/'-separated as go-ntfs resolves them. A component may be
// a shell-style glob (`*.pf`, `$I*`, a bare `*` for "every entry here"); every
// matching file is pulled, its full volume path preserved under the scratch
// root so each parser's by-content or by-name discovery finds it unchanged
// (goese needs the SUM/ parent, gosbe the NTUSER.DAT/UsrClass.dat base name).
// The .LOG1/.LOG2 transaction logs ride along for the hive parsers that replay
// them (gore, goamcache). Keyed to the subs[].sets names in main.go.
var artefactSets = map[string][]string{
	"prefetch":   {`Windows/Prefetch/*.pf`},
	"srum":       {`Windows/System32/sru/SRUDB.dat`},
	"sum":        {`Windows/System32/LogFiles/Sum/*.mdb`},
	"recyclebin": {`$Recycle.Bin/*/$I*`},
	"mft":        {`$MFT`},
	"amcache": {
		`Windows/AppCompat/Programs/Amcache.hve`,
		`Windows/AppCompat/Programs/Amcache.hve.LOG1`,
		`Windows/AppCompat/Programs/Amcache.hve.LOG2`,
	},
	"shimcache": {
		`Windows/System32/config/SYSTEM`,
		`Windows/System32/config/SYSTEM.LOG1`,
		`Windows/System32/config/SYSTEM.LOG2`,
	},
	"winevt": {`Windows/System32/winevt/Logs/*.evtx`},
	"registry-core": {
		`Windows/System32/config/SYSTEM`, `Windows/System32/config/SYSTEM.LOG1`, `Windows/System32/config/SYSTEM.LOG2`,
		`Windows/System32/config/SOFTWARE`, `Windows/System32/config/SOFTWARE.LOG1`, `Windows/System32/config/SOFTWARE.LOG2`,
		`Windows/System32/config/SAM`, `Windows/System32/config/SAM.LOG1`, `Windows/System32/config/SAM.LOG2`,
		`Windows/System32/config/SECURITY`, `Windows/System32/config/SECURITY.LOG1`, `Windows/System32/config/SECURITY.LOG2`,
		`Windows/System32/config/DEFAULT`, `Windows/System32/config/DEFAULT.LOG1`, `Windows/System32/config/DEFAULT.LOG2`,
	},
	"ntuser": {
		`Users/*/NTUSER.DAT`, `Users/*/NTUSER.DAT.LOG1`, `Users/*/NTUSER.DAT.LOG2`,
	},
	"usrclass": {
		`Users/*/AppData/Local/Microsoft/Windows/UsrClass.dat`,
		`Users/*/AppData/Local/Microsoft/Windows/UsrClass.dat.LOG1`,
		`Users/*/AppData/Local/Microsoft/Windows/UsrClass.dat.LOG2`,
	},
	"recent": {
		`Users/*/AppData/Roaming/Microsoft/Windows/Recent/*.lnk`,
		`Users/*/AppData/Roaming/Microsoft/Windows/Recent/AutomaticDestinations/*.automaticDestinations-ms`,
	},
	"timeline": {
		`Users/*/AppData/Local/ConnectedDevicesPlatform/*/ActivitiesCache.db`,
	},
}

// coreOrder is the deterministic order windows-core (the sweep's union) pulls
// its sets in — the artefact-class order of the repo README, matching the
// sweep's own execution order.
var coreOrder = []string{
	"prefetch", "srum", "sum", "recyclebin", "mft", "amcache", "shimcache",
	"winevt", "registry-core", "ntuser", "usrclass", "recent", "timeline",
}

// expand resolves the requested set names to the ordered, de-duplicated list of
// volume paths to pull. "windows-core" expands to every set in coreOrder; an
// unknown set contributes nothing (the parser simply finds its artefact
// missing, exactly as over a loose tree without it).
func expand(sets []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(name string) {
		for _, p := range artefactSets[name] {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	for _, s := range sets {
		if s == "windows-core" {
			for _, name := range coreOrder {
				add(name)
			}
			continue
		}
		add(s)
	}
	return out
}

// Collect pulls the named artefact sets out of the live volume into the scratch
// tree <work>/live and returns it, the live analogue of
// diskimage.MaterialiseImage. drive selects the volume: "", "auto" or a truthy
// word ("1"/"true"/"on"/"live") take the system drive, a letter ("C" or "C:")
// names one. A path that matches nothing is not an error — the tree is simply
// missing that artefact, like a loose folder without it — and a single file
// that cannot be copied is noted on stderr and skipped so one unreadable
// artefact never sinks the rest. The only hard errors are being unable to open
// or parse the volume (not elevated, bad drive, not NTFS) and an unwritable
// scratch dir; on any of them the half-built scratch tree is removed.
func Collect(getenv func(string) string, work, drive string, sets []string) (string, error) {
	if !Supported() {
		return "", ErrUnsupported
	}

	scratch := filepath.Join(work, HostDir)
	if err := os.RemoveAll(scratch); err != nil {
		return "", fmt.Errorf("clear scratch %s: %w", scratch, err)
	}
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return "", fmt.Errorf("scratch under %s: %w", work, err)
	}

	reader, closer, err := openVolume(resolveDrive(getenv, drive))
	if err != nil {
		os.RemoveAll(scratch)
		return "", err
	}
	defer func() { _ = closer() }()

	paged, err := ntfs.NewPagedReader(reader, pageSize, pageCache)
	if err != nil {
		os.RemoveAll(scratch)
		return "", fmt.Errorf("page reader: %w", err)
	}
	ctx, err := ntfs.GetNTFSContext(paged, 0)
	if err != nil {
		os.RemoveAll(scratch)
		return "", fmt.Errorf("parse NTFS volume: %w", err)
	}
	defer ctx.Close()

	root, err := ctx.GetMFT(5)
	if err != nil {
		os.RemoveAll(scratch)
		return "", fmt.Errorf("open volume root: %w", err)
	}

	for _, spec := range expand(sets) {
		for _, m := range resolve(ctx, root, spec) {
			if err := copyOut(ctx, m, scratch); err != nil {
				fmt.Fprintf(os.Stderr, "gowindowlicker live: skip %s: %v\n", m.volPath, err)
			}
		}
	}
	return scratch, nil
}

// resolveDrive maps a drive setting to a bare upper-case drive letter. A truthy
// word, "auto" or empty take the system drive; anything else is read as a drive
// letter, tolerating a trailing colon.
func resolveDrive(getenv func(string) string, drive string) string {
	d := strings.TrimSpace(drive)
	if d == "" || isTruthy(d) || strings.EqualFold(d, "auto") {
		return defaultSystemDrive(getenv)
	}
	d = strings.ToUpper(strings.TrimSuffix(d, ":"))
	if d == "" {
		return defaultSystemDrive(getenv)
	}
	return d[:1]
}

// isTruthy reports whether a value just means "on" rather than naming a drive.
// "live" is included so the `live` positional word maps to the system drive.
func isTruthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on", "live":
		return true
	}
	return false
}

// match is one resolved leaf: the file's volume-relative path (the path it is
// written to under the scratch root) and its MFT entry.
type match struct {
	volPath string
	entry   *ntfs.MFT_ENTRY
}

// resolve walks the volume from root along a '/'-separated spec, expanding each
// glob component against the directory index, and returns every file leaf the
// spec matches. Directories that survive to the leaf (a trailing `*` that hit a
// folder, say) are dropped: only files are collected.
func resolve(ctx *ntfs.NTFSContext, root *ntfs.MFT_ENTRY, spec string) []match {
	cur := []match{{volPath: "", entry: root}}
	for _, comp := range strings.Split(spec, "/") {
		if comp == "" {
			continue
		}
		var next []match
		if isGlob(comp) {
			for _, c := range cur {
				for _, child := range childrenMatching(ctx, c.entry, comp) {
					next = append(next, match{volPath: join(c.volPath, child.name), entry: child.entry})
				}
			}
		} else {
			for _, c := range cur {
				e, err := c.entry.Open(ctx, comp)
				if err != nil {
					continue
				}
				next = append(next, match{volPath: join(c.volPath, comp), entry: e})
			}
		}
		cur = next
		if len(cur) == 0 {
			return nil
		}
	}
	var out []match
	for _, c := range cur {
		if c.entry == root || c.entry.IsDir(ctx) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// named is a directory child the glob matched: its chosen name and entry.
type named struct {
	name  string
	entry *ntfs.MFT_ENTRY
}

// childrenMatching returns the entries of dir whose name matches the glob,
// de-duplicated by MFT reference (an entry appears once per file name — long
// and 8.3 — in the index). When several names of one entry match, the longest
// is kept so the human-readable long name is what lands in the scratch path.
func childrenMatching(ctx *ntfs.NTFSContext, dir *ntfs.MFT_ENTRY, glob string) []named {
	glob = strings.ToLower(glob)
	order := []uint64{}
	names := map[uint64][]string{}
	for _, idx := range dir.Dir(ctx) {
		ref := idx.MftReference()
		name := idx.File().Name()
		if name == "" {
			continue
		}
		if _, ok := names[ref]; !ok {
			order = append(order, ref)
		}
		names[ref] = append(names[ref], name)
	}

	var out []named
	for _, ref := range order {
		best := ""
		for _, name := range names[ref] {
			if ok, _ := path.Match(glob, strings.ToLower(name)); ok && len(name) > len(best) {
				best = name
			}
		}
		if best == "" {
			continue
		}
		entry, err := ctx.GetMFT(int64(ref))
		if err != nil {
			continue
		}
		out = append(out, named{name: best, entry: entry})
	}
	return out
}

// copyOut writes one matched file's default $DATA stream to its volume-relative
// path under scratch. The stream is read from the volume's clusters, so a file
// the OS holds open is copied all the same.
func copyOut(ctx *ntfs.NTFSContext, m match, scratch string) error {
	reader, err := ntfs.OpenStream(ctx, m.entry,
		ntfs.ATTR_TYPE_DATA, ntfs.WILDCARD_STREAM_ID, ntfs.WILDCARD_STREAM_NAME)
	if err != nil {
		return err
	}
	dst := filepath.Join(scratch, filepath.FromSlash(m.volPath))
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	size := ntfs.RangeSize(reader)
	if size > 0 {
		_, err = io.Copy(f, io.NewSectionReader(reader, 0, size))
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(dst)
	}
	return err
}

func isGlob(s string) bool { return strings.ContainsAny(s, "*?[") }

func join(base, name string) string {
	if base == "" {
		return name
	}
	return base + "/" + name
}
