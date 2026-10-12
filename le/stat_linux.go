//go:build linux

package le

import (
	"os"
	"syscall"
	"time"
)

// sourceAccessTime returns the .lnk file's own last-access time (the
// SourceAccessed column) from the Linux stat payload. The birth time is not
// exposed by the Go stdlib on Linux, so it is not part of the schema.
func sourceAccessTime(st os.FileInfo) (time.Time, bool) {
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return time.Time{}, false
	}
	return time.Unix(sys.Atim.Unix()), true
}
