# `get-sybers/gowindowlicker` — the Windows artefact matrix as one structured binary

Every Windows Go parser is a package of this module and a sub-tool of this
single static binary — the multi-tool self-orchestration shape defined by
the get-sybers framework standard. It is
the **only** shipped shape: the parsers live here as packages, and there are
no standalone per-parser binaries or images. gowindowlicker *is* the
Windows tool.

**Each parser keeps its canonical tool name, its `<SUBTOOL>_*` env block,
its record shapes and its record-file name** (`goprefetch.jsonl`,
`gore.jsonl`, `goevtx.jsonl`, …) — the interface byakugan and the pipeline
consume. One binary, one image, one run, one structured output tree, one
JSON summary line.

## The sub-tool is the parameter

```
gowindowlicker                     every parser (the default sweep)
gowindowlicker <subtool>           one parser's env-driven batch mode, under
                                   its canonical <SUBTOOL>_* block
gowindowlicker <subtool> <args>    that parser's argv debug pass-through
gowindowlicker --version | --print-contract
```

`lick` stays accepted as the explicit word for the default run. The calling
vocabulary is the sub-tool names — there is **no stream or model-word
vocabulary** — and there is no layered knowledge store: the Windows
artefacts are self-contained, so every sub-run is independent and the sweep
has no layers.

Sub-tools — each one a package of this module with its own README
documenting what it parses, its `<SUBTOOL>_*` env block and its record
shapes:

| Sub-tool | Parses |
|---|---|
| [`goprefetch`](prefetch/README.md) | Windows Prefetch: XP→Win11 `.pf`, MAM decompression in pure Go |
| [`goese`](ese/README.md) | ESE databases: SRUM `SRUDB.dat` (IdMap/SID enrichment) and SUM `Current.mdb` |
| [`gorb`](rb/README.md) | Recycle Bin `$I` records, v1 + v2 |
| [`gomft`](mft/README.md) | raw `$MFT`: MACB from 0x10 + 0x30, ADS, full paths |
| [`goamcache`](amcache/README.md) | `Amcache.hve`, with `.LOG` replay |
| [`goappcompat`](appcompat/README.md) | ShimCache from SYSTEM hives, with `.LOG` replay |
| [`goevtx`](evtx/README.md) | `.evtx` event logs → the JSON record shape byakugan's evtx maps consume |
| [`gore`](re/README.md) | batch-driven registry key/value dumps (bundled `/batch/default.reb`) |
| [`gosbe`](sbe/README.md) | ShellBags (BagMRU) with reconstructed paths |
| [`gole`](le/README.md) | `.lnk` shell links |
| [`gojle`](jle/README.md) | AutomaticDestinations jump lists |
| [`gowxt`](wxt/README.md) | Windows Timeline ActivitiesCache.db |

All parsing is content-driven where the artefact allows it, so one evidence
tree — a mounted image root, a `gomount materialise` tree, or a staged
export — feeds every parser without routing.

## Input

The evidence tree at `GOWINDOWLICKER_INPUT_DIR` (default `/input`), mounted
read-only and shared by every sub-run.

## Env (the sweep)

| Variable | Default | Meaning |
|---|---|---|
| `GOWINDOWLICKER_INPUT_DIR` | `/input` | evidence tree, recursed read-only, shared by every sub-run |
| `GOWINDOWLICKER_OUT_DIR` | `/output` | output root: one `<subtool>/` tree per parser |
| `GOWINDOWLICKER_WORK_DIR` | `/work` | scratch (writable tmpfs), shared by every sub-run; a disk image's artefact sets are pulled here while its parsers run — bind a disk-backed directory for large images |
| `GOWINDOWLICKER_IMAGE` | *(empty)* | run **on a disk image**: its path relative to `INPUT_DIR` (E01/Ex01, raw, VMDK, VHDX, VHD, QCOW2, VDI, DMG (.dmg), sparseimage (.sparseimage)). The baked-in [gomount](../gomount) pulls every parser's artefact set (`windows-core`) out of the OS volume into `WORK_DIR`, the parsers run over that, records land under `<OUT_DIR>/<subtool>/<image>/…`, the scratch goes — nothing is exported. Empty = the loose tree itself plus every image directly under it. A sub-tool run reads `<SUBTOOL>_IMAGE` and writes `<OUT_DIR>/<image>/…` |
| `GOWINDOWLICKER_FORCE` | `0` | `1/true/yes/on`: rerun items that already have valid output, in every sub-run |
| `GOWINDOWLICKER_LOG_LEVEL` | `info` | `error\|warn\|info\|debug`, stderr only, applied to every sub-run |

A single-parser sub-run (`gowindowlicker gorb`) ignores the
`GOWINDOWLICKER_*` block and reads that tool's canonical `<SUBTOOL>_*`
variables instead (`GORB_INPUT_DIR`, `GORB_OUT_DIR`, …) — each parser
package's README documents its block. Under the sweep a sub-tool's own
non-reserved variables fall through untouched: **format stays per parser**
(`GORB_FORMAT=csv` beside the sweep is honoured; three of the twelve are
JSONL-only), and `GORE_BATCH` keeps selecting gore's `.reb` definition
(default: the bundled `/batch/default.reb`).

## Output

The sweep (bare or `lick`) writes one structured tree under
`GOWINDOWLICKER_OUT_DIR`:

```
<OUT_DIR>/<subtool>/<item>/<subtool>.<jsonl|csv>   per parser, one folder per discovered artefact
```

and prints **one** aggregate JSON summary line with every sub-tool's own
summary embedded under `subtools` (plus the roll-up counters, `status`,
`exit`). A single-parser sub-run prints that parser's ordinary summary line
and writes as its own README declares.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | success — every sub-run succeeded (or was already up-to-date) |
| 1 | nothing produced — no sub-run found anything to parse |
| 2 | config error — any sub-run hit a bad variable / missing input / unwritable output |
| 3 | partial — work was produced but at least one item or sub-run failed |

Roll-up order: any config error → 2; else any partial → 3; else any work → 0;
else 1.

## Run

```sh
go install github.com/Get-Sybers/gowindowlicker@latest   # -> $(go env GOPATH)/bin/gowindowlicker

# every parser (the default), env-driven — see Env (the sweep) above; point the
# *_DIR paths at local directories. gore resolves its batch from GORE_BATCH (the
# container default /batch/default.reb is absent for a plain binary):
GOWINDOWLICKER_INPUT_DIR=./in GOWINDOWLICKER_OUT_DIR=./out \
  GORE_BATCH=./re/batch/default.reb gowindowlicker
```

Scoped, the sub-tool is the only thing that changes (each reads its own
`<SUBTOOL>_*` block — see the per-parser READMEs):

```sh
GORB_INPUT_DIR=./in GORB_OUT_DIR=./out gowindowlicker gorb
```

The shared batch runtime is imported from the published
[`github.com/Get-Sybers/gopinfo`](https://github.com/Get-Sybers/gopinfo) module,
not vendored here. Container images are built and documented separately in
[GoDFIR-toolz](https://github.com/Get-Sybers/GoDFIR-toolz).

## argv pass-through (debug only)

`gowindowlicker <subtool> <args>` hands the rest of the command line to that
parser's own argv debug mode: `-f FILE | -d DIR | --tar` (a `gomount stream`
tar on stdin) and each parser's own flags; records stream to stdout — see
each package README for its exact flags. `--version` prints the version;
`--print-contract` prints [`contract.yml`](contract.yml).