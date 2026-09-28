// gowindowlicker — the Windows artefact matrix as ONE structured binary:
// every Windows parser lives here as a package and a sub-tool of this
// single static binary. There are no standalone per-parser binaries or
// images: gowindowlicker is the Windows tool. Each parser keeps its
// canonical tool name, its `<SUBTOOL>_*` env block, its record shapes and
// its record-file name (`goprefetch.jsonl`, `gore.jsonl`, …) — the
// interface byakugan and the pipeline consume.
//
//	gowindowlicker                     every parser — the default sweep,
//	                                   GOWINDOWLICKER_* driven
//	gowindowlicker <subtool>           one parser's env-driven batch mode,
//	                                   under its canonical <SUBTOOL>_* block
//	gowindowlicker <subtool> <args>    that parser's argv debug pass-through
//	                                   (-f FILE | -d DIR | --tar, …)
//	gowindowlicker --version | --print-contract
//
// A disk image is an item too (imageitem.go): GOWINDOWLICKER_IMAGE (or a
// sub-tool's <SUBTOOL>_IMAGE) names one under the input tree — or every
// image directly under it is taken — and the parsers run ON the image: the
// baked-in gomount pulls their artefact sets out of the OS volume into the
// work dir, the batch loop runs over that, records land under
// <OUT_DIR>/<subtool>/<image>/, the scratch goes. Nothing is exported.
//
// (`lick` stays accepted as the explicit word for the default run.) The
// multi-tool dispatcher shape of docs/framework/04 §4.3. The calling
// vocabulary is the sub-tool names — there is no stream or model-word
// vocabulary — and there is no layered knowledge store: the Windows
// artefacts are self-contained, so every sub-run is independent and the
// sweep has no layers.
package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"github.com/get-sybers/gopinfo/diskimage"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	batch "github.com/get-sybers/gopinfo/framework"

	"github.com/get-sybers/gowindowlicker/amcache"
	"github.com/get-sybers/gowindowlicker/appcompat"
	"github.com/get-sybers/gowindowlicker/ese"
	"github.com/get-sybers/gowindowlicker/evtx"
	"github.com/get-sybers/gowindowlicker/jle"
	"github.com/get-sybers/gowindowlicker/le"
	"github.com/get-sybers/gowindowlicker/mft"
	"github.com/get-sybers/gowindowlicker/prefetch"
	"github.com/get-sybers/gowindowlicker/rb"
	"github.com/get-sybers/gowindowlicker/re"
	"github.com/get-sybers/gowindowlicker/sbe"
	"github.com/get-sybers/gowindowlicker/wxt"
)

//go:embed contract.yml
var contractYML string

// version is stamped at build time via -ldflags (-X main.version).
var version = "0.0.0-dev"

// argvMain is a parser package's Main: batch.Entry plus the tool's argv
// debug modes on the global flag set. It never returns on the batch,
// --version and --print-contract paths, and exits itself on argv errors.
type argvMain func(version, contractYML string)

// sub is one embedded parser. Order is the sweep execution order,
// deterministic: the artefact-class order of the repo README.
type sub struct {
	name string
	tool batch.Tool
	main argvMain
	// sets are the gomount artefact sets this parser reads off a disk
	// image (gomount/materialise-sets.yml); the sweep pulls windows-core,
	// the union of them all, once per image.
	sets []string
}

var subs = []sub{
	{"goprefetch", prefetch.Tool, prefetch.Main, []string{"prefetch"}},
	{"goese", ese.Tool, ese.Main, []string{"srum", "sum"}},
	{"gorb", rb.Tool, rb.Main, []string{"recyclebin"}},
	{"gomft", mft.Tool, mft.Main, []string{"mft"}},
	{"goamcache", amcache.Tool, amcache.Main, []string{"amcache"}},
	{"goappcompat", appcompat.Tool, appcompat.Main, []string{"shimcache"}},
	{"goevtx", evtx.Tool, evtx.Main, []string{"winevt"}},
	{"gore", re.Tool, re.Main, []string{"registry-core", "ntuser", "usrclass", "amcache"}},
	{"gosbe", sbe.Tool, sbe.Main, []string{"ntuser", "usrclass"}},
	{"gole", le.Tool, le.Main, []string{"recent"}},
	{"gojle", jle.Tool, jle.Main, []string{"recent"}},
	{"gowxt", wxt.Tool, wxt.Main, []string{"timeline"}},
}

// sweepSets is what the sweep pulls out of an image: every parser's sets in one.
var sweepSets = []string{"windows-core"}

func main() { os.Exit(run(os.Args[1:], os.Getenv, os.Stdout)) }

// run is main's testable body: no arguments is every parser (the default
// sweep), a subtool name is one parser.
func run(args []string, getenv func(string) string, stdout io.Writer) int {
	if len(args) == 1 {
		switch strings.TrimLeft(args[0], "-") {
		case "version":
			fmt.Fprintf(stdout, "gowindowlicker %s\n", version)
			return 0
		case "print-contract":
			io.WriteString(stdout, contractYML)
			return 0
		}
	}
	if len(args) == 0 || (len(args) == 1 && args[0] == "lick") {
		return runLick(getenv, stdout)
	}
	for _, s := range subs {
		if s.name != args[0] {
			continue
		}
		if len(args) == 1 {
			if img := getenv(batch.Prefix(s.tool.Name) + "_IMAGE"); img != "" {
				return runSubOnImage(s, img, getenv, stdout)
			}
			return batch.Run(s.tool, batch.Options{Version: version, Contract: contractYML}, getenv, stdout)
		}
		// argv debug pass-through: hand the rest of the command line to
		// the parser's own Main, busybox-style. It exits the process.
		os.Args = append([]string{"gowindowlicker " + s.name}, args[1:]...)
		s.main(version, contractYML)
		return 0
	}
	fmt.Fprintf(os.Stderr, "gowindowlicker: unknown sub-tool %q\n", args[0])
	usage()
	return 2
}

func usage() {
	names := make([]string, len(subs))
	for i, s := range subs {
		names[i] = s.name
	}
	fmt.Fprintln(os.Stderr, "usage: gowindowlicker                      (every parser — the default sweep, GOWINDOWLICKER_* driven)\n"+
		"       gowindowlicker <subtool>            (one parser's env-driven batch: "+strings.Join(names, " ")+")\n"+
		"       gowindowlicker <subtool> <args>     (that parser's argv debug pass-through: -f FILE | -d DIR | --tar, …)\n"+
		"       gowindowlicker --version | --print-contract")
}

// lickSummary is the sweep's single stdout JSON line: the aggregate roll-up
// with every subtool's own summary embedded.
type lickSummary struct {
	Tool      string          `json:"tool"`
	Version   string          `json:"version"`
	Status    string          `json:"status"`
	Inputs    int             `json:"inputs"`
	Processed int             `json:"processed"`
	Skipped   int             `json:"skipped"`
	Failed    int             `json:"failed"`
	Records   int             `json:"records"`
	Images    []string        `json:"images,omitempty"` // the disk images run on, by item name
	Subtools  []batch.Summary `json:"subtools"`
	Failures  []batch.Failure `json:"failures,omitempty"` // an image that could not be read
	Exit      int             `json:"exit"`
	Started   string          `json:"started"`
	DurationS float64         `json:"duration_s"`
}

// runSubOnImage is one parser's batch mode over ONE disk image: its sets are
// pulled into scratch, the batch loop runs over that tree into
// <OUT_DIR>/<image>/, and the parser's ordinary summary line is printed.
func runSubOnImage(s sub, selected string, getenv func(string) string, stdout io.Writer) int {
	pfx := batch.Prefix(s.tool.Name) + "_"
	get := func(suffix, def string) string {
		if v := getenv(pfx + suffix); v != "" {
			return v
		}
		return def
	}
	in, out, work := get("INPUT_DIR", "/input"), get("OUT_DIR", "/output"), get("WORK_DIR", "/work")
	images, err := diskimage.SelectedImages(in, selected)
	if err != nil {
		return configErrorSummary(s.name, err, stdout)
	}
	scratch, err := diskimage.MaterialiseImage(getenv, work, images[0], s.sets)
	if err != nil {
		return configErrorSummary(s.name, err, stdout)
	}
	defer os.RemoveAll(scratch)
	shim := shimEnv(s.tool, getenv, map[string]string{
		"INPUT_DIR": scratch, "OUT_DIR": filepath.Join(out, diskimage.ImageItemName(in, images[0])), "IMAGE": "",
	})
	return batch.Run(s.tool, batch.Options{Version: version, Contract: contractYML}, shim, stdout)
}

// configErrorSummary prints a parser-shaped config_error summary line.
func configErrorSummary(tool string, err error, stdout io.Writer) int {
	fmt.Fprintf(os.Stderr, "gowindowlicker %s: %v\n", tool, err)
	sum := batch.Summary{Tool: tool, Version: version, Status: "config_error", Outputs: []string{},
		Error: err.Error(), Exit: 2, Started: time.Now().UTC().Format(time.RFC3339)}
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	enc.Encode(sum)
	return 2
}

// runLick executes every parser over the same input tree, each through the
// ordinary batch runtime under a shimmed environment into its own
// <OUT_DIR>/<subtool>/ tree, and prints one aggregate summary line. Format
// stays per parser (three of the twelve are JSONL-only), so a sub-tool's own
// <SUBTOOL>_FORMAT falls through the shim untouched.
func runLick(getenv func(string) string, stdout io.Writer) int {
	get := func(suffix, def string) string {
		if v := getenv("GOWINDOWLICKER_" + suffix); v != "" {
			return v
		}
		return def
	}
	in := get("INPUT_DIR", "/input")
	out := get("OUT_DIR", "/output")
	work := get("WORK_DIR", "/work")
	force := get("FORCE", "0")
	level := get("LOG_LEVEL", "info")

	started := time.Now()
	sum := &lickSummary{
		Tool: "gowindowlicker", Version: version, Subtools: []batch.Summary{},
		Started: started.UTC().Format(time.RFC3339),
	}

	// the passes: the loose tree itself (only when no image is selected),
	// then every disk image — each pulled into scratch by gomount and run
	// as its own host under <OUT_DIR>/<subtool>/<image>/
	type pass struct{ in, host, scratch string }
	var passes []pass
	selected := get("IMAGE", "")
	images, err := diskimage.SelectedImages(in, selected)
	if err != nil {
		sum.Status, sum.Exit = "config_error", 2
		sum.Failures = []batch.Failure{{Item: selected, Error: err.Error()}}
		fmt.Fprintf(os.Stderr, "gowindowlicker: %v\n", err)
		return writeLick(sum, started, stdout)
	}
	if selected == "" {
		passes = append(passes, pass{in: in})
	}
	sawOK, sawPartial, sawConfig := false, false, false
	for _, img := range images {
		host := diskimage.ImageItemName(in, img)
		scratch, err := diskimage.MaterialiseImage(getenv, work, img, sweepSets)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gowindowlicker: %s: %v\n", host, err)
			sum.Failed++
			sum.Failures = append(sum.Failures, batch.Failure{Item: img, Error: err.Error()})
			sawPartial = true
			continue
		}
		sum.Images = append(sum.Images, host)
		passes = append(passes, pass{in: scratch, host: host, scratch: scratch})
	}

	for _, p := range passes {
		for _, s := range subs {
			shim := shimEnv(s.tool, getenv, map[string]string{
				"INPUT_DIR": p.in, "OUT_DIR": filepath.Join(out, s.name, p.host), "WORK_DIR": work,
				"FORCE": force, "LOG_LEVEL": level, "IMAGE": "",
			})
			var buf bytes.Buffer
			code := batch.Run(s.tool, batch.Options{Version: version, Contract: contractYML}, shim, &buf)
			var ss batch.Summary
			if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &ss); err != nil {
				ss = batch.Summary{Tool: s.name, Status: "config_error", Exit: code}
			}
			if p.host != "" {
				ss.Subtool = p.host // which image this sub-run was
			}
			sum.Subtools = append(sum.Subtools, ss)
			sum.Inputs += ss.Inputs
			sum.Processed += ss.Processed
			sum.Skipped += ss.Skipped
			sum.Failed += ss.Failed
			sum.Records += ss.Records
			switch code {
			case 0:
				sawOK = true
			case 2:
				sawConfig = true
			case 3:
				sawPartial = true
				sawOK = true
			}
		}
		if p.scratch != "" {
			os.RemoveAll(p.scratch)
		}
	}
	switch {
	case sawConfig:
		sum.Status, sum.Exit = "config_error", 2
	case sawPartial && sawOK:
		sum.Status, sum.Exit = "partial", 3
	case sawPartial:
		sum.Status, sum.Exit = "partial", 3
	case sawOK:
		sum.Status, sum.Exit = "ok", 0
	default:
		sum.Status, sum.Exit = "nothing", 1
	}
	return writeLick(sum, started, stdout)
}

// writeLick stamps the duration and prints the one aggregate line.
func writeLick(sum *lickSummary, started time.Time, stdout io.Writer) int {
	sum.DurationS = float64(int64(time.Since(started).Seconds()*1000)) / 1000
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(sum); err != nil {
		fmt.Fprintf(os.Stderr, "gowindowlicker: write summary: %v\n", err)
		return 2
	}
	return sum.Exit
}

// shimEnv maps a subtool's reserved variables onto the sweep's values while
// letting every other variable fall through to the real environment.
func shimEnv(t batch.Tool, getenv func(string) string, vals map[string]string) func(string) string {
	pfx := batch.Prefix(t.Name) + "_"
	return func(k string) string {
		if suffix, ok := strings.CutPrefix(k, pfx); ok {
			if v, set := vals[suffix]; set {
				return v
			}
		}
		return getenv(k)
	}
}
