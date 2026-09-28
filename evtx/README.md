# `gowindowlicker goevtx` — Windows event log (`.evtx`) parser

Static Go binary on Velociraptor's `go-evtx`. Parses `.evtx` and emits one JSON
record per event in the evtx JSON record shape that the DX_DFIR evtx lane and
byakugan's winevt/evtx maps consume — `EventId`, `Level`, `Provider`,
`Channel`, `Computer`, `EventRecordId`, `TimeCreated`, `UserId`, and `Payload`
(the event's EventData rendered as the classic
`{"EventData":{"Data":[{"@Name","#text"}...]}}` form, or `{"UserData":...}`),
plus `SourceFile` and a null `MapDescription`. Per-provider derived columns
(`PayloadData1-6`) are not produced: byakugan reads the raw EventData, so
absent fields are omitted, never faked. A torn chunk or an unrenderable record
is dropped and counted on stderr; the rest of the log still parses.

A sub-tool of [`get-sybers/gowindowlicker`](../README.md), the Windows dozen
as one binary: run as `gowindowlicker goevtx` with no further arguments it
reads its `GOEVTX_*` environment block, batches over the input tree, and
prints one JSON summary line.

## Input

`GOEVTX_INPUT_DIR` (default `/input`, mounted read-only) is walked
recursively; every file with the `.evtx` extension or the `ElfFile` signature
is one item.

## Env

| Variable | Default | Meaning |
|---|---|---|
| `GOEVTX_INPUT_DIR` | `/input` | evidence tree, recursed |
| `GOEVTX_OUT_DIR` | `/output` | output root, one folder per log |
| `GOEVTX_WORK_DIR` | `/work` | scratch (writable tmpfs); goevtx needs none but honours it |
| `GOEVTX_FORCE` | `0` | `1/true/yes/on`: rerun items that already have valid output |
| `GOEVTX_FORMAT` | `json` | record format; `json` (JSONL) is the only format |
| `GOEVTX_LOG_LEVEL` | `info` | `error|warn|info|debug`, stderr only |

## Output

`<OUT_DIR>/<item>/goevtx.jsonl`, one record per event. `<item>` is the log
path relative to `GOEVTX_INPUT_DIR` with path separators and whitespace folded
to `_`. The record file is written as `.part` and renamed into place on
success, so an item whose record file exists is skipped on the next run unless
`GOEVTX_FORCE` is set.

stdout is exactly one JSON object: `tool`, `version`, `status`, `inputs`,
`processed`, `skipped`, `failed`, `records`, `outputs`, `exit`, `started`,
`duration_s`, plus `failures` (item + error) when something failed and `error`
on a config error. Progress and errors go to stderr.

## Exit codes

| Code | Status | Meaning |
|---|---|---|
| 0 | `ok` | every item processed, or already up to date |
| 1 | `nothing` | no event log found, or every item failed |
| 2 | `config_error` | bad variable, missing or unreadable input, unwritable output |
| 3 | `partial` | at least one item processed and at least one failed |

## Run

```sh
docker build -t get-sybers/gowindowlicker:latest -f gowindowlicker/Dockerfile gowindowlicker
docker run --rm --cap-drop ALL --security-opt no-new-privileges --network none \
  --read-only --tmpfs /work:rw,nosuid,nodev,uid=2000,gid=2000 \
  -v "$PWD/in:/input:ro" -v "$PWD/out:/output" \
  get-sybers/gowindowlicker:latest goevtx
```

The shared [`test/contract_test.sh`](../test/contract_test.sh) builds the image, runs the sweep over the packages' `testdata/` (a
generated, structurally valid empty log; `testdata/gen_fixtures.py` remakes it),
and asserts the summary line, the exit code, idempotency and the config-error
exit.

## argv pass-through (debug only, `gowindowlicker goevtx <args>`)

Any argument switches to the single-run argv mode; `--version` prints the
version and `--print-contract` prints `contract.yml`.

- `-f FILE` — parse a single `.evtx`.
- `-d DIR` — scan a directory tree for `.evtx` (by extension or signature).
- `--json DIR --jsonf NAME` — write JSONL to a file instead of stdout (default `EvtxECmd_Output.json`).
- `--xml DIR --xmlf NAME` — also write a best-effort XML sidecar reconstructed per record for manual review (not the original binary XML, not ingested).
- `-q`.

argv exit codes: 0 every log parsed, 1 usage or fatal error, 2 at least one
file failed to parse (the rest are still emitted).
