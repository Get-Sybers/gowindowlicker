package main

// flatten.go collapses the gopinfo runtime's per-artefact output tree into the
// flat lane convention DX_DFIR consumes: one record file per (subtool, evidence
// asset), <OUT_DIR>/<subtool>/<asset-id>.jsonl. The runtime still writes its
// <item>/<tool>.jsonl tree — into a private temp OUT_DIR — and the engine merges
// it, so gopinfo is untouched. Every row already carries its Origin.Path (the
// v0.3.0 stamp), so dropping the per-item directory level loses no provenance.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	batch "github.com/Get-Sybers/gopinfo/framework"

	"github.com/Get-Sybers/gowindowlicker/origin"
)

// runFlat runs one parser's batch runtime into a private temp OUT_DIR, then
// collapses the result into one <outDir>/<assetID>.<ext> per evidence asset.
// assetID comes from the stage manifest under scratch (disk_serial →
// volume_guid → image_sha256), else fallback. started is the sweep-wide set of
// final files already opened this run: the first touch truncates and a later
// same-asset pass appends, so two acquisitions of one disk merge into one file
// while a whole re-run rebuilds cleanly. The parser's summary line — its Outputs
// rewritten to the flat file(s) — is written to stdout.
func runFlat(t batch.Tool, o batch.Options, getenv func(string) string,
	vals map[string]string, outDir, work, scratch, fallback string,
	started map[string]bool, stdout io.Writer) int {

	if work == "" {
		work = os.TempDir()
	}
	_ = os.MkdirAll(work, 0o755)
	tmp, err := os.MkdirTemp(work, ".flat-"+batch.Prefix(t.Name)+"-")
	if err != nil {
		return configErrorSummary(t.Name, fmt.Errorf("flat scratch under %s: %w", work, err), stdout)
	}
	defer os.RemoveAll(tmp)

	// the runtime writes into the temp tree; every other reserved var is the
	// caller's, and OUT_DIR is ours.
	shimVals := make(map[string]string, len(vals)+1)
	for k, v := range vals {
		shimVals[k] = v
	}
	shimVals["OUT_DIR"] = tmp

	var buf bytes.Buffer
	code := batch.Run(t, o, shimEnv(t, getenv, shimVals), &buf)

	var sum batch.Summary
	if jerr := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &sum); jerr != nil {
		// the runtime failed before a parseable summary — pass its bytes through
		_, _ = stdout.Write(buf.Bytes())
		return code
	}

	outs, merr := mergeFlat(tmp, outDir, origin.AssetID(scratch, fallback), started)
	if merr != nil {
		sum.Status, sum.Exit, sum.Error = "config_error", 2, merr.Error()
		code = 2
	} else {
		sum.Outputs = outs
	}
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(&sum)
	return code
}

// mergeFlat concatenates every record file the runtime wrote under tmp into one
// <outDir>/<assetID>.<ext> per extension (jsonl in the pipeline), in path order
// for a deterministic result. .part files (a crashed item the runtime did not
// rename) are ignored.
func mergeFlat(tmp, outDir, assetID string, started map[string]bool) ([]string, error) {
	byExt := map[string][]string{}
	err := filepath.WalkDir(tmp, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return werr
		}
		if strings.HasSuffix(p, ".part") {
			return nil
		}
		ext := strings.TrimPrefix(filepath.Ext(p), ".")
		if ext == "" {
			return nil
		}
		byExt[ext] = append(byExt[ext], p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(byExt) == 0 {
		return nil, nil // the parser matched nothing; no flat file to write
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	exts := make([]string, 0, len(byExt))
	for ext := range byExt {
		exts = append(exts, ext)
	}
	sort.Strings(exts)
	var outs []string
	for _, ext := range exts {
		files := byExt[ext]
		sort.Strings(files)
		final := filepath.Join(outDir, assetID+"."+ext)
		if err := appendFiles(final, files, started); err != nil {
			return nil, err
		}
		outs = append(outs, final)
	}
	return outs, nil
}

// appendFiles writes the source files into final, truncating on the first touch
// of final this run (started) and appending after, so same-asset passes merge.
func appendFiles(final string, files []string, started map[string]bool) error {
	flag := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if !started[final] {
		flag = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
		started[final] = true
	}
	out, err := os.OpenFile(final, flag, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	for _, f := range files {
		in, oerr := os.Open(f)
		if oerr != nil {
			return oerr
		}
		_, cerr := io.Copy(out, in)
		_ = in.Close()
		if cerr != nil {
			return cerr
		}
	}
	return nil
}
