# `gowindowlicker gorb` — Recycle Bin `$I` parser

Static Go binary. Parses the modern Recycle Bin `$I` records — v1 (Vista–8.0,
fixed 260-wchar path) and v2 (Win8.1/10/11, length-prefixed path) — and emits
`SourceName`, `FileType`, `FileName`, `FileSize` and `DeletedOn` (RFC3339 UTC)
as JSONL or CSV. The legacy XP `INFO2` container is not handled and is reported
as a parse failure rather than mis-read. Records are found by their header,
not their file name, so a raw-mount `$IXXXX` and Plaso's `image_export` rename
(`$` → `_`, i.e. `_IXXXX`) both parse, and `$R` payloads and `desktop.ini` are
left alone.

A sub-tool of [`get-sybers/gowindowlicker`](../README.md), the Windows dozen
as one binary: run as `gowindowlicker gorb` with no further arguments it
reads its `GORB_*` environment block, batches over the input tree, and
prints one JSON summary line.

## Input

`GORB_INPUT_DIR` (default `/input`, mounted read-only) is walked recursively;
every file whose 24-byte header is a v1 or v2 `$I` record is one item,
whatever its name.

## Env

| Variable | Default | Meaning |
|---|---|---|
| `GORB_INPUT_DIR` | `/input` | evidence tree, recursed |
| `GORB_OUT_DIR` | `/output` | output root, one folder per `$I` record |
| `GORB_WORK_DIR` | `/work` | scratch (writable tmpfs); gorb needs none but honours it |
| `GORB_FORCE` | `0` | `1/true/yes/on`: rerun items that already have valid output |
| `GORB_FORMAT` | `json` | record format: `json` (JSONL) or `csv` |
| `GORB_LOG_LEVEL` | `info` | `error|warn|info|debug`, stderr only |

## Output

`<OUT_DIR>/<item>/gorb.jsonl` (or `gorb.csv`), one record per `$I` file.
`<item>` is the file path relative to `GORB_INPUT_DIR` with path separators
and whitespace folded to `_`. The record file is written as `.part` and
renamed into place on success, so an item whose record file exists is skipped
on the next run unless `GORB_FORCE` is set.

stdout is exactly one JSON object: `tool`, `version`, `status`, `inputs`,
`processed`, `skipped`, `failed`, `records`, `outputs`, `exit`, `started`,
`duration_s`, plus `failures` (item + error) when something failed and `error`
on a config error. Progress and errors go to stderr.

## Exit codes

| Code | Status | Meaning |
|---|---|---|
| 0 | `ok` | every item processed, or already up to date |
| 1 | `nothing` | no `$I` record found, or every item failed |
| 2 | `config_error` | bad variable, missing or unreadable input, unwritable output |
| 3 | `partial` | at least one item processed and at least one failed |

## Run

```sh
docker build -t get-sybers/gowindowlicker:latest -f gowindowlicker/Dockerfile gowindowlicker
docker run --rm --cap-drop ALL --security-opt no-new-privileges --network none \
  --read-only --tmpfs /work:rw,nosuid,nodev,uid=2000,gid=2000 \
  -v "$PWD/in:/input:ro" -v "$PWD/out:/output" \
  -e GORB_FORMAT=csv \
  get-sybers/gowindowlicker:latest gorb
```

The shared [`test/contract_test.sh`](../test/contract_test.sh) builds the image, runs the sweep over the packages' `testdata/` (a
generated v2 `$I` record under its Plaso-renamed name; `testdata/gen_fixtures.py`
remakes it), and asserts the summary line, the exit code, idempotency and the
config-error exit.

## argv pass-through (debug only, `gowindowlicker gorb <args>`)

Any argument switches to the single-run argv mode; `--version` prints the
version and `--print-contract` prints `contract.yml`.

- `-f FILE` — parse a single `$I` file.
- `-d DIR` — scan a directory tree, picking `$I` records out by header.
- `--tar` — read a tar archive on stdin, as `gomount stream` emits (one entry per file, entry name = the file's volume path), and parse the `$I` records out of it by header; `SourceName` is the entry's volume path.
- `--json DIR --jsonf NAME` / `--csv DIR --csvf NAME` — write to a file instead of stdout (defaults `RBCmd_Output.jsonl` / `.csv`); `-q`.

```sh
gomount stream --filter '$Recycle.Bin/*' disk.E01 | gowindowlicker gorb --tar --csv /output --csvf gorb.csv
```

argv exit codes: 0 every file parsed, 1 usage or fatal error or no records
found, 2 at least one record failed to parse (the rest are still emitted).
