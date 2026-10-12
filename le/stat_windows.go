//go:build windows

package le

import (
	"os"
	"syscall"
	"time"
)

// sourceAccessTime returns the .lnk file's own last-access time (the
// SourceAccessed column) from the Win32 file-attribute payload os.Stat exposes
// on Windows. The birth time (CreationTime) is also available there but is kept
// off the schema for parity with the other platforms.
func sourceAccessTime(st os.FileInfo) (time.Time, bool) {
	sys, ok := st.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return time.Time{}, false
	}
	return time.Unix(0, sys.LastAccessTime.Nanoseconds()), true
}
