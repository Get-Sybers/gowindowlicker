# `gowindowlicker goappcompat` — ShimCache (AppCompatCache) parser

Static Go binary on Velociraptor's `regparser` (and its `appcompatcache`
subpackage). Reads the AppCompatCache (ShimCache) value from a SYSTEM hive and
emits one record per entry — ControlSet, CacheEntryPosition, Path,
LastModifiedTimeUTC, SourceFile — as JSONL or CSV. Executed/Duplicate state is
not emitted (regparser's shimcache parser does not expose it, so it is never
faked). The parser targets the Win8.1/Win10+ cache layout; an older hive yields
no entries rather than a mis-parse.

Dirty-hive `.LOG` replay: when `SYSTEM.LOG1`/`.LOG2` sit beside the hive, a
recovered copy is written into the work dir and parsed. No logs, an unwritable
work dir or a recovery error fall back to the committed hive with a one-line
note, never a hard fail.

A sub-tool of [`get-sybers/gowindowlicker`](../README.md), the Windows dozen
as one binary: run as `gowindowlicker goappcompat` with no further arguments it
reads its `GOAPPCOMPAT_*` environment block, batches over the input tree, and
prints one JSON summary line.

## Input

`GOAPPCOMPAT_INPUT_DIR` (default `/input`, mounted read-only) is walked
recursively; every registry hive (`regf` signature) whose current control set
carries an `AppCompatCache` value is one item, whatever its name. Other hives
and `.LOG*` files are not items.

## Env

| Variable | Default | Meaning |
|---|---|---|
| `GOAPPCOMPAT_INPUT_DIR` | `/input` | evidence tree, recursed |
| `GOAPPCOMPAT_OUT_DIR` | `/output` | output root, one folder per hive |
| `GOAPPCOMPAT_WORK_DIR` | `/work` | writable tmpfs for the recovered copy during `.LOG` replay |
| `GOAPPCOMPAT_FORCE` | `0` | `1/true/yes/on`: rerun items that already have valid output |
| `GOAPPCOMPAT_FORMAT` | `json` | record format: `json` (JSONL) or `csv` |
| `GOAPPCOMPAT_LOG_LEVEL` | `info` | `error|warn|info|debug`, stderr only |

## Output

`<OUT_DIR>/<item>/goappcompat.jsonl` (or `goappcompat.csv`), one record per
shimcache entry. `<item>` is the hive path relative to
`GOAPPCOMPAT_INPUT_DIR` with path separators and whitespace folded to `_`. The
record file is written as `.part` and renamed into place on success, so an item
whose record file exists is skipped on the next run unless `GOAPPCOMPAT_FORCE`
is set.

stdout is exactly one JSON object: `tool`, `version`, `status`, `inputs`,
`processed`, `skipped`, `failed`, `records`, `outputs`, `exit`, `started`,
`duration_s`, plus `failures` (item + error) when something failed and `error`
on a config error. Progress and errors go to stderr.

## Exit codes

| Code | Status | Meaning |
|---|---|---|
| 0 | `ok` | every item processed, or already up to date |
| 1 | `nothing` | no SYSTEM hive with an AppCompatCache found, or every item failed |
| 2 | `config_error` | bad variable, missing or unreadable input, unwritable output |
| 3 | `partial` | at least one item processed and at least one failed |

## Run

```sh
docker build -t get-sybers/gowindowlicker:latest -f gowindowlicker/Dockerfile gowindowlicker
docker run --rm --cap-drop ALL --security-opt no-new-privileges --network none \
  --read-only --tmpfs /work:rw,nosuid,nodev,uid=2000,gid=2000 \
  -v "$PWD/in:/input:ro" -v "$PWD/out:/output" \
  -e GOAPPCOMPAT_FORMAT=csv \
  get-sybers/gowindowlicker:latest goappcompat
```

The shared [`test/contract_test.sh`](../test/contract_test.sh) builds the image, runs the sweep over the packages' `testdata/` (a
generated SYSTEM hive with a two-entry cache; `testdata/gen_fixtures.py` remakes
it), and asserts the summary line, the exit code, idempotency and the
config-error exit.

## argv pass-through (debug only, `gowindowlicker goappcompat <args>`)

Any argument switches to the single-run argv mode; `--version` prints the
version and `--print-contract` prints `contract.yml`.

- `-f FILE` — parse a single SYSTEM hive.
- `-d DIR` — scan a directory tree; hives are found by their `regf` signature, and a hive without an AppCompatCache is skipped silently.
- `--json DIR --jsonf NAME` / `--csv DIR --csvf NAME` — write to a file instead of stdout (defaults `AppCompatCacheParser_Output.jsonl` / `.csv`).
- `--work-dir DIR` (default `$TMPDIR`) — writable dir for the recovered hive during `.LOG` replay; `-q`.

argv exit codes: 0 parsed, 1 usage or fatal error, 2 at least one hive failed
(the rest are still emitted).
