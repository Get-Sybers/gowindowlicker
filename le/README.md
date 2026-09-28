# `gowindowlicker gole` — shell link (`.lnk`) parser

Static Go binary on `parsiya/golnk`. Parses Windows Shell Link (`.lnk`) files
and emits one record per shortcut — the source file's mtime/atime, the target
`Created`/`Modified`/`Accessed` times, `FileSize`, `LocalPath`,
`RelativePath`, `WorkingDirectory`, `Arguments`, `IconLocation`, `CommonPath`,
and the decoded `HeaderFlags`/`FileAttributes` sets — as JSONL or CSV. A source
birth time is not exposed by the Go standard library on Linux, so there is no
`SourceCreated` column; fields golnk does not resolve (a walked
TargetIDAbsolutePath, MFT references, tracker MAC) are omitted, never faked.
Shortcuts are found by the `.lnk` extension or the `0x4C` ShellLinkHeader, so a
renamed file still parses.

A sub-tool of [`get-sybers/gowindowlicker`](../README.md), the Windows dozen
as one binary: run as `gowindowlicker gole` with no further arguments it
reads its `GOLE_*` environment block, batches over the input tree, and
prints one JSON summary line.

## Input

`GOLE_INPUT_DIR` (default `/input`, mounted read-only) is walked recursively;
every file with the `.lnk` extension or the `0x4C` shell-link header is one
item.

## Env

| Variable | Default | Meaning |
|---|---|---|
| `GOLE_INPUT_DIR` | `/input` | evidence tree, recursed |
| `GOLE_OUT_DIR` | `/output` | output root, one folder per shortcut |
| `GOLE_WORK_DIR` | `/work` | scratch (writable tmpfs); gole needs none but honours it |
| `GOLE_FORCE` | `0` | `1/true/yes/on`: rerun items that already have valid output |
| `GOLE_FORMAT` | `json` | record format: `json` (JSONL) or `csv` |
| `GOLE_LOG_LEVEL` | `info` | `error|warn|info|debug`, stderr only |

## Output

`<OUT_DIR>/<item>/gole.jsonl` (or `gole.csv`), one record per shortcut.
`<item>` is the file path relative to `GOLE_INPUT_DIR` with path separators
and whitespace folded to `_`. The record file is written as `.part` and
renamed into place on success, so an item whose record file exists is skipped
on the next run unless `GOLE_FORCE` is set.

stdout is exactly one JSON object: `tool`, `version`, `status`, `inputs`,
`processed`, `skipped`, `failed`, `records`, `outputs`, `exit`, `started`,
`duration_s`, plus `failures` (item + error) when something failed and `error`
on a config error. Progress and errors go to stderr.

## Exit codes

| Code | Status | Meaning |
|---|---|---|
| 0 | `ok` | every item processed, or already up to date |
| 1 | `nothing` | no `.lnk` found, or every item failed |
| 2 | `config_error` | bad variable, missing or unreadable input, unwritable output |
| 3 | `partial` | at least one item processed and at least one failed |

## Run

```sh
docker build -t get-sybers/gowindowlicker:latest -f gowindowlicker/Dockerfile gowindowlicker
docker run --rm --cap-drop ALL --security-opt no-new-privileges --network none \
  --read-only --tmpfs /work:rw,nosuid,nodev,uid=2000,gid=2000 \
  -v "$PWD/in:/input:ro" -v "$PWD/out:/output" \
  -e GOLE_FORMAT=csv \
  get-sybers/gowindowlicker:latest gole
```

The shared [`test/contract_test.sh`](../test/contract_test.sh) builds the image, runs the sweep over the packages' `testdata/` (the
sample shortcut also used by the unit tests), and asserts the summary line,
the exit code, idempotency and the config-error exit.

## argv pass-through (debug only, `gowindowlicker gole <args>`)

Any argument switches to the single-run argv mode; `--version` prints the
version and `--print-contract` prints `contract.yml`.

- `-f FILE` — parse a single `.lnk`.
- `-d DIR` — walk a directory recursively, selecting `.lnk` by extension or the `0x4C` header.
- `--tar` — read a tar archive on stdin, as `gomount stream` emits (one entry per file, entry name = the file's volume path, body = its bytes), and parse each `.lnk` entry selected by extension or header. `SourceModified` comes from the tar entry's mtime; a tar header carries no atime, so `SourceAccessed` is empty in this mode.
- `--json DIR --jsonf NAME` / `--csv DIR --csvf NAME` — write to a file instead of stdout (defaults `LECmd_Output.json` / `.csv`); `-q`.

```sh
gomount stream --filter '*.lnk' disk.E01 | gowindowlicker gole --tar --csv /output --csvf lnk.csv
```

argv exit codes: 0 every file parsed, 1 usage or fatal error, 2 at least one
file failed (the rest are still emitted).
