//go:build !windows

package live

import "io"

// Supported is false off Windows: there is no \\.\C: raw volume to read, so
// live mode is unavailable and Collect returns ErrUnsupported.
func Supported() bool { return false }

// defaultSystemDrive has no meaning off Windows; a placeholder keeps the
// shared resolveDrive logic compiling.
func defaultSystemDrive(getenv func(string) string) string { return "C" }

// openVolume never succeeds off Windows; Collect rejects live mode before this
// is reached, and it stays here only so the shared code compiles on every OS.
func openVolume(drive string) (io.ReaderAt, func() error, error) {
	return nil, nil, ErrUnsupported
}
