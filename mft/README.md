# `gowindowlicker gomft` — raw `$MFT` parser

Static Go binary on Velociraptor's `go-ntfs`. Parses a raw `$MFT` and emits one
record per entry — entry/sequence, parent reference, file name + extension,
size, the `$STANDARD_INFORMATION` (0x10) and `$FILE_NAME` (0x30) MACB
timestamps, flags and ADS — as JSONL or CSV. Fields go-ntfs does not expose
(ReparseTarget, SecurityId, ObjectId, ZoneId) are omitted, never faked. The
`$MFT` is found by its `FILE` record signature, so a raw-mount `$MFT` and
Plaso's `image_export` rename (`_MFT`) both parse.

A sub-tool of [`get-sybers/gowindowlicker`](../README.md), the Windows dozen
as one binary: run as `gowindowlicker gomft` with no further arguments it
reads its `GOMFT_*` environment block, batches over the input tree, and
prints one JSON summary line.

## Input

`GOMFT_INPUT_DIR` (default `/input`, mounted read-only) is walked recursively;
every file that begins with the `FILE` record signature is one item, whatever
its name.

## Env

| Variable | Default | Meaning |
|---|---|---|
| `GOMFT_INPUT_DIR` | `/input` | evidence tree, recursed |
| `GOMFT_OUT_DIR` | `/output` | output root, one folder per item |
| `GOMFT_WORK_DIR` | `/work` | scratch (writable tmpfs); gomft needs none but honours it |
| `GOMFT_FORCE` | `0` | `1/true/yes/on`: rerun items that already have valid output |
| `GOMFT_FORMAT` | `json` | record format: `json` (JSONL) or `csv` |
| `GOMFT_LOG_LEVEL` | `info` | `error|warn|info|debug`, stderr only |

## Output

`<OUT_DIR>/<item>/gomft.jsonl` (or `gomft.csv`), one record per MFT entry.
`<item>` is the input path relative to `GOMFT_INPUT_DIR` with path separators
and whitespace folded to `_`. The record file is written as `.part` and renamed
into place on success, so an item whose record file exists is skipped on the
next run unless `GOMFT_FORCE` is set.

stdout is exactly one JSON object: `tool`, `version`, `status`, `inputs`,
`processed`, `skipped`, `failed`, `records`, `outputs`, `exit`, `started`,
`duration_s`, plus `failures` (item + error) when something failed and `error`
on a config error. Progress and errors go to stderr.

## Exit codes

| Code | Status | Meaning |
|---|---|---|
| 0 | `ok` | every item processed, or already up to date |
| 1 | `nothing` | no `$MFT` found, or every item failed |
| 2 | `config_error` | bad variable, missing or unreadable input, unwritable output |
| 3 | `partial` | at least one item processed and at least one failed |

## Run

```sh
docker build -t get-sybers/gowindowlicker:latest -f gowindowlicker/Dockerfile gowindowlicker
docker run --rm --cap-drop ALL --security-opt no-new-privileges --network none \
  --read-only --tmpfs /work:rw,nosuid,nodev,uid=2000,gid=2000 \
  -v "$PWD/in:/input:ro" -v "$PWD/out:/output" \
  -e GOMFT_FORMAT=json -e GOMFT_FORCE=0 \
  get-sybers/gowindowlicker:latest gomft
```

The shared [`test/contract_test.sh`](../test/contract_test.sh) builds the image, runs the sweep over the packages' `testdata/`, and
asserts the summary line, the exit code, idempotency and the config-error exit.

## argv pass-through (debug only, `gowindowlicker gomft <args>`)

Any argument switches to the single-run argv mode; `--version` prints the
version and `--print-contract` prints `contract.yml`.

- `-f FILE` — parse a single `$MFT` file.
- `-d DIR` — scan a directory tree and parse each file carrying the `FILE` signature.
- `--tar` — read a tar archive on stdin (one entry per file, as `gomount stream` emits) and parse each entry carrying the `FILE` signature; each candidate is buffered whole before parsing.
- `--json DIR --jsonf NAME` / `--csv DIR --csvf NAME` — write to a file instead of stdout (defaults `MFTECmd_Output.jsonl` / `.csv`).
- `--record-size N` (1024), `--cluster-size N` (4096), `-q`.

```sh
gomount stream --filter '$MFT' evidence.E01 | gowindowlicker gomft --tar --json out --jsonf mft.json
```

argv exit codes: 0 every `$MFT` parsed, 1 usage or fatal error, 2 at least one
file failed to parse (the rest are still emitted).
