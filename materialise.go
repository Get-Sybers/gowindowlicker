package main

// materialise.go pulls a disk image's artefact sets into a scratch tree, the
// same job as gopinfo diskimage.MaterialiseImage but with the two additions the
// evidence-asset identity work needs. diskimage.MaterialiseImage runs gomount
// WITHOUT --manifest and discards its stdout, so the Origin join has nothing to
// read; this wrapper runs the baked-in gomount WITH --manifest (so gomount
// writes <scratch>/materialise.jsonl, the manifest gopinfo record.LoadManifest
// — via origin.Hook — joins onto every row) and --hash (so that manifest's
// metadata row carries image_sha256). It also always selects the SOFTWARE hive
// so the host MachineGuid (host.id) is stageable even on a per-sub-tool image
// run whose own sets do not include it. See issue tracker: diskimage could grow
// an opt-in manifest mode so this wrapper is unnecessary.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/Get-Sybers/gopinfo/diskimage"

	batch "github.com/Get-Sybers/gopinfo/framework"
)

// hostHive is the SOFTWARE hive the host-identity read needs. It is selected in
// addition to the parser's own sets so host.id (MachineGuid) is available even
// on a per-sub-tool image run whose sets do not include it; gomount de-dupes a
// selector against the sets, so naming the hive again just stages it once, and
// every parser already filters it out by content or name (only gore reads all
// hives, and its own registry set already stages it).
const hostHive = `\Windows\System32\config\SOFTWARE`

// materialise pulls image's sets into the scratch tree <work>/<image item> and
// returns it — the diskimage.MaterialiseImage contract, plus --manifest, --hash
// and the SOFTWARE selector. gomount's exit 1 (nothing on the volume matched) is
// not an error: the tree is empty and the parsers find nothing, exactly as a
// loose folder without their artefact; the manifest's metadata row still lands.
func materialise(getenv func(string) string, work, image string, sets []string) (string, error) {
	scratch := filepath.Join(work, diskimage.ImageItemName(filepath.Dir(image), image))
	if err := os.RemoveAll(scratch); err != nil {
		return "", fmt.Errorf("clear scratch %s: %w", scratch, err)
	}
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return "", fmt.Errorf("scratch under %s: %w", work, err)
	}
	args := []string{"materialise", "--out", scratch, "--manifest"}
	if imageHash(getenv) {
		args = append(args, "--hash")
	}
	for _, s := range sets {
		args = append(args, "--set", s)
	}
	args = append(args, "--select", hostHive, image)
	cmd := exec.Command(diskimage.GomountPath(getenv), args...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = nil // its summary line is not ours to print
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			return scratch, nil // nothing pulled
		}
		os.RemoveAll(scratch)
		return "", fmt.Errorf("gomount materialise %s: %w", filepath.Base(image), err)
	}
	return scratch, nil
}

// imageHash reports whether gomount streams the container sha256 into the
// manifest (image_sha256). It is on by default — the frozen Origin contract
// carries ImageSHA256 — and opt-out through GOWINDOWLICKER_IMAGE_HASH for a very
// large image where the extra full-container read is not worth it. A malformed
// value never silently disables provenance.
func imageHash(getenv func(string) string) bool {
	v := getenv("GOWINDOWLICKER_IMAGE_HASH")
	if v == "" {
		return true
	}
	on, err := batch.ParseBool(v)
	if err != nil {
		return true
	}
	return on
}
