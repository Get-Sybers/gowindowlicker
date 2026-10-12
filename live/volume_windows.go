//go:build windows

package live

import (
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// Supported is true on Windows: a live host's volumes can be opened raw.
func Supported() bool { return true }

// defaultSystemDrive is the drive holding Windows — %SystemDrive% (C: on a
// stock install) — as a bare upper-case letter.
func defaultSystemDrive(getenv func(string) string) string {
	d := getenv("SystemDrive")
	if d == "" {
		return "C"
	}
	d = strings.ToUpper(strings.TrimSuffix(strings.TrimSpace(d), ":"))
	if d == "" {
		return "C"
	}
	return d[:1]
}

// openVolume opens the raw NTFS volume \\.\<drive>: read-only and returns it as
// an io.ReaderAt. The volume is shared for read AND write so the open succeeds
// while Windows itself holds it open; GENERIC_READ keeps our handle read-only,
// so the evidence volume is never written. Opening a raw volume needs
// Administrator — access-denied is reported as exactly that.
func openVolume(drive string) (io.ReaderAt, func() error, error) {
	name := `\\.\` + drive + `:`
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, nil, fmt.Errorf("open %s: %w", name, err)
	}
	h, err := windows.CreateFile(p,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		if err == windows.ERROR_ACCESS_DENIED {
			return nil, nil, fmt.Errorf("open %s: access denied — live mode must run as Administrator", name)
		}
		return nil, nil, fmt.Errorf("open %s: %w", name, err)
	}
	f := os.NewFile(uintptr(h), name)
	return f, f.Close, nil
}
