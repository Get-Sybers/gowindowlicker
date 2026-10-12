package main

// live_test proves the parsers run ON the live host the same way imageitem_test
// proves they run on a disk image: a stub collector stands in for the real raw
// NTFS pull (the live analogue of the gomount stub), staging one Recycle Bin $I
// record where a collected recyclebin set would put it. The sweep, the `live`
// word and a sub-tool run then find it through the ordinary batch loop, write
// the records under <OUT_DIR>/…/live/, and leave no scratch behind. The real
// collector's Windows-only refusal is checked on the host OS.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Get-Sybers/gowindowlicker/live"
)

// stubLiveCollect swaps liveCollect for a collector that stages a $I record
// under <work>/live and records the sets it was asked for, restoring the real
// collector when the test ends. It mirrors imageitem_test's gomount stub.
func stubLiveCollect(t *testing.T, gotSets *[]string) {
	t.Helper()
	saved := liveCollect
	t.Cleanup(func() { liveCollect = saved })
	liveCollect = func(getenv func(string) string, work, drive string, sets []string) (string, error) {
		if gotSets != nil {
			*gotSets = sets
		}
		scratch := filepath.Join(work, live.HostDir)
		rbDir := filepath.Join(scratch, "$Recycle.Bin", "S-1-5-21-1")
		if err := os.MkdirAll(rbDir, 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(rbDir, "$I3XKCd7.pdf"), v2Record(`C:\Users\x\y.pdf`), 0o644); err != nil {
			return "", err
		}
		return scratch, nil
	}
}

func TestLiveWordSweep(t *testing.T) {
	var sets []string
	stubLiveCollect(t, &sets)
	out, work := t.TempDir(), t.TempDir()
	var buf bytes.Buffer
	code := run([]string{"live"}, env(map[string]string{
		"GOWINDOWLICKER_OUT_DIR": out, "GOWINDOWLICKER_WORK_DIR": work,
		"GORE_BATCH": goreBatch(t),
	}), &buf)
	var sum lickSummary
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &sum); err != nil {
		t.Fatalf("summary: %v: %s", err, buf.String())
	}
	if code != 0 {
		t.Fatalf("exit %d: %s", code, buf.String())
	}
	if !sum.Live {
		t.Fatalf("summary not marked live: %+v", sum)
	}
	// the sweep asks for the one union set
	if len(sets) != 1 || sets[0] != "windows-core" {
		t.Fatalf("sweep collected sets = %v, want [windows-core]", sets)
	}
	// the record landed under the live host: <out>/gorb/live/<item>/gorb.jsonl
	matches, _ := filepath.Glob(filepath.Join(out, "gorb", "live", "*", "gorb.jsonl"))
	if len(matches) != 1 {
		t.Fatalf("gorb output under the live host: %v", matches)
	}
	// nothing at the loose level — the live host is the only source
	if strays, _ := filepath.Glob(filepath.Join(out, "gorb", "*", "gorb.jsonl")); len(strays) != 0 {
		t.Fatalf("records at the loose level though live was selected: %v", strays)
	}
	// every sub-run is attributed to the live host
	for _, ss := range sum.Subtools {
		if ss.Subtool != live.HostDir {
			t.Fatalf("sub-run not attributed to the live host: %+v", ss)
		}
	}
	// the scratch tree is gone
	if entries, _ := os.ReadDir(work); len(entries) != 0 {
		t.Fatalf("scratch left behind: %v", entries)
	}
}

func TestLiveEnvSweep(t *testing.T) {
	stubLiveCollect(t, nil)
	out := t.TempDir()
	var buf bytes.Buffer
	code := run(nil, env(map[string]string{
		"GOWINDOWLICKER_LIVE": "1", "GOWINDOWLICKER_OUT_DIR": out,
		"GOWINDOWLICKER_WORK_DIR": t.TempDir(), "GORE_BATCH": goreBatch(t),
	}), &buf)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, buf.String())
	}
	matches, _ := filepath.Glob(filepath.Join(out, "gorb", "live", "*", "gorb.jsonl"))
	if len(matches) != 1 {
		t.Fatalf("GOWINDOWLICKER_LIVE=1 sweep output: %v", matches)
	}
}

func TestSubtoolLive(t *testing.T) {
	var sets []string
	stubLiveCollect(t, &sets)
	out, work := t.TempDir(), t.TempDir()
	var buf bytes.Buffer
	code := run([]string{"gorb"}, env(map[string]string{
		"GORB_LIVE": "1", "GORB_OUT_DIR": out, "GORB_WORK_DIR": work,
	}), &buf)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, buf.String())
	}
	// a scoped run collects only that parser's sets
	if len(sets) != 1 || sets[0] != "recyclebin" {
		t.Fatalf("gorb live collected sets = %v, want [recyclebin]", sets)
	}
	// records land under <out>/live/<item>/gorb.jsonl (no <subtool> level for a
	// scoped run, exactly like the image sub-run)
	matches, _ := filepath.Glob(filepath.Join(out, "live", "*", "gorb.jsonl"))
	if len(matches) != 1 {
		t.Fatalf("gorb live output under <out>/live/: %v", matches)
	}
	if entries, _ := os.ReadDir(work); len(entries) != 0 {
		t.Fatalf("scratch left behind: %v", entries)
	}
}

// TestLiveUnsupportedOffWindows: with the REAL collector, selecting live off
// Windows is a clean config_error that names Windows, not a crash or a silent
// empty run.
func TestLiveUnsupportedOffWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("off-Windows contract test")
	}
	var buf bytes.Buffer
	code := run([]string{"live"}, env(map[string]string{
		"GOWINDOWLICKER_OUT_DIR": t.TempDir(), "GOWINDOWLICKER_WORK_DIR": t.TempDir(),
		"GORE_BATCH": goreBatch(t),
	}), &buf)
	if code != 2 {
		t.Fatalf("live off Windows: exit %d, want 2: %s", code, buf.String())
	}
	if !strings.Contains(buf.String(), "config_error") || !strings.Contains(buf.String(), "Windows") {
		t.Fatalf("live off Windows should fail naming Windows: %s", buf.String())
	}
}
