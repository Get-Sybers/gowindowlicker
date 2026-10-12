//go:build !linux && !windows

package le

import (
	"os"
	"time"
)

// sourceAccessTime has no portable source for the .lnk file's last-access time
// on platforms other than Linux and Windows (the stat payload's shape differs
// per OS), so the SourceAccessed column is simply left unset there. gowindowlicker
// targets Linux and Windows; this keeps the module compiling everywhere else.
func sourceAccessTime(st os.FileInfo) (time.Time, bool) {
	return time.Time{}, false
}
