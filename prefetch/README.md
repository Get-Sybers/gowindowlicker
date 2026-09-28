# `gowindowlicker goprefetch` — Windows prefetch (`.pf`) parser

Static Go binary on Velociraptor's `go-prefetch`, whose pure-Go
LZXpress-Huffman implementation decompresses Win8+/Win10/Win11 MAM prefetch on
any OS, so XP-era through Win11 `.pf` files parse natively on Linux. Each file
yields one record: `SourceFilename`, `SourceModified`, `Executable`, `Path`,
`Hash`, `Version`, `FileSize`, `RunCount`, `LastRun`, `PreviousRuns`,
`FilesAccessed`. Volume information blocks are not emitted (not exposed by
the library).

A sub-tool of [`get-sybers/gowindowlicker`](../README.md), the Windows dozen
as one binary: run as `gowindowlicker goprefetch` with no further arguments it
reads its `GOPREFETCH_*` environment block, batches over the input tree, and
prints one JSON summary line.

## Input

`GOPREFETCH_INPUT_DIR` (default `/input`, mounted read-only) is walked
recursively; every `*.pf` file (case-insensitive) is one item.

## Env

| Variable | Default | Meaning |
|---|---|---|
| `GOPREFETCH_INPUT_DIR` | `/input` | evidence tree, recursed |
| `GOPREFETCH_OUT_DIR` | `/output` | output root, one folder per prefetch file |
| `GOPREFETCH_WORK_DIR` | `/work` | scratch (writable tmpfs); goprefetch needs none but honours it |
| `GOPREFETCH_FORCE` | `0` | `1/true/yes/on`: rerun items that already have valid output |
| `GOPREFETCH_FORMAT` | `json` | record format: `json` (JSONL) or `csv` |
| `GOPREFETCH_LOG_LEVEL` | `info` | `error|warn|info|debug`, stderr only |

## Output

`<OUT_DIR>/<item>/goprefetch.jsonl` (or `goprefetch.csv`), one record per
prefetch file. `<item>` is the file path relative to `GOPREFETCH_INPUT_DIR`
with path separators and whitespace folded to `_`. The record file is written
as `.part` and renamed into place on success, so an item whose record file
exists is skipped on the next run unless `GOPREFETCH_FORCE` is set.

stdout is exactly one JSON object: `tool`, `version`, `status`, `inputs`,
`processed`, `skipped`, `failed`, `records`, `outputs`, `exit`, `started`,
`duration_s`, plus `failures` (item + error) when something failed and `error`
on a config error. Progress and errors go to stderr.

## Exit codes

| Code | Status | Meaning |
|---|---|---|
| 0 | `ok` | every item processed, or already up to date |
| 1 | `nothing` | no `.pf` found, or every item failed |
| 2 | `config_error` | bad variable, missing or unreadable input, unwritable output |
| 3 | `partial` | at least one item processed and at least one failed |

## Run

```sh
docker build -t get-sybers/gowindowlicker:latest -f gowindowlicker/Dockerfile gowindowlicker
docker run --rm --cap-drop ALL --security-opt no-new-privileges --network none \
  --read-only --tmpfs /work:rw,nosuid,nodev,uid=2000,gid=2000 \
  -v "$PWD/in:/input:ro" -v "$PWD/out:/output" \
  get-sybers/gowindowlicker:latest goprefetch
```

The shared [`test/contract_test.sh`](../test/contract_test.sh) builds the image, runs the sweep over the packages' `testdata/` (a
generated WinXP-format prefetch file; `testdata/gen_fixtures.py` remakes it), and
asserts the summary line, the exit code, idempotency and the config-error
exit.

## argv pass-through (debug only, `gowindowlicker goprefetch <args>`)

Any argument switches to the single-run argv mode; `--version` prints the
version and `--print-contract` prints `contract.yml`.

- `-f FILE` — parse a single prefetch file.
- `-d DIR` — walk a directory tree for `*.pf`.
- `--tar` — read a tar archive on stdin, as `gomount stream` emits (one entry per file, entry name = the file's volume path, body = its bytes), and parse every `*.pf` entry; each is buffered in memory (prefetch files are small), one at a time. A read or parse failure on one entry is counted and the stream continues.
- `--json DIR --jsonf NAME` / `--csv DIR --csvf NAME` — write to a file instead of stdout (defaults `PrefetchDump_Output.jsonl` / `.csv`); `-q`.

```sh
gomount stream --filter '*.pf' disk.E01 | gowindowlicker goprefetch --tar --json /output
```

argv exit codes: 0 every file parsed, 1 usage or fatal error, 2 at least one
file failed to parse (the rest are still emitted).
