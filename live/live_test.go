package live

// live_test covers the platform-independent parts of the live collector: the
// set→path expansion the sweep and sub-runs drive it with, the drive
// resolution, and the off-Windows contract (Collect must refuse rather than
// pretend). The raw NTFS walk itself is exercised on a real Windows volume,
// which a portable unit test cannot stand up.

import (
	"runtime"
	"strings"
	"testing"
)

func TestExpandWindowsCore(t *testing.T) {
	got := expand([]string{"windows-core"})
	if len(got) == 0 {
		t.Fatal("windows-core expanded to nothing")
	}
	// Every concrete set's paths must be present.
	for name, paths := range artefactSets {
		for _, p := range paths {
			if !contains(got, p) {
				t.Errorf("windows-core missing %s path %q", name, p)
			}
		}
	}
	// SYSTEM is named by both shimcache and registry-core; the union must list
	// it once.
	if n := count(got, `Windows/System32/config/SYSTEM`); n != 1 {
		t.Errorf("SYSTEM appears %d times in windows-core, want 1 (deduped)", n)
	}
	// The order follows coreOrder: the first path is prefetch's.
	if got[0] != `Windows/Prefetch/*.pf` {
		t.Errorf("windows-core[0] = %q, want the prefetch path", got[0])
	}
}

func TestExpandNamedSets(t *testing.T) {
	got := expand([]string{"prefetch", "recyclebin"})
	want := []string{`Windows/Prefetch/*.pf`, `$Recycle.Bin/*/$I*`}
	if len(got) != len(want) {
		t.Fatalf("expand(prefetch,recyclebin) = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expand(prefetch,recyclebin) = %v, want %v", got, want)
		}
	}
	// An unknown set contributes nothing rather than erroring.
	if n := expand([]string{"nosuchset"}); len(n) != 0 {
		t.Errorf("unknown set expanded to %v, want empty", n)
	}
}

func TestResolveDrive(t *testing.T) {
	// defaultSystemDrive is "C" off Windows and %SystemDrive% on it; either way
	// the truthy/auto/empty forms resolve to a single bare letter, and an
	// explicit letter is honoured verbatim.
	for _, in := range []string{"", "auto", "AUTO", "1", "true", "on", "live"} {
		if d := resolveDrive(env(nil), in); len(d) != 1 {
			t.Errorf("resolveDrive(%q) = %q, want a single drive letter", in, d)
		}
	}
	for in, want := range map[string]string{"D": "D", "d": "D", "E:": "E", "e:": "E"} {
		if d := resolveDrive(env(nil), in); d != want {
			t.Errorf("resolveDrive(%q) = %q, want %q", in, d, want)
		}
	}
}

func TestSupportedMatchesGOOS(t *testing.T) {
	if Supported() != (runtime.GOOS == "windows") {
		t.Errorf("Supported() = %v on %s", Supported(), runtime.GOOS)
	}
}

func TestCollectRefusedOffWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("off-Windows contract test")
	}
	scratch, err := Collect(env(nil), t.TempDir(), "C", []string{"windows-core"})
	if err != ErrUnsupported {
		t.Fatalf("Collect off Windows: err = %v, want ErrUnsupported", err)
	}
	if scratch != "" {
		t.Errorf("Collect off Windows returned scratch %q, want empty", scratch)
	}
	if !strings.Contains(ErrUnsupported.Error(), "Windows") {
		t.Errorf("ErrUnsupported should name Windows: %q", ErrUnsupported.Error())
	}
}

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func contains(xs []string, x string) bool { return count(xs, x) > 0 }

func count(xs []string, x string) int {
	n := 0
	for _, v := range xs {
		if v == x {
			n++
		}
	}
	return n
}
