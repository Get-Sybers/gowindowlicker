# `gowindowlicker gowxt` — Windows Timeline (ActivitiesCache.db) parser

Static Go binary on `modernc.org/sqlite` (pure Go, no cgo). Reads the Windows
Timeline **ActivitiesCache.db** and emits one record per `Activity` row — the
executable (from the `AppId` JSON), DisplayText / ContentInfo (from the
`Payload` JSON), the Start/End/LastModified/Expiration timestamps (Unix
seconds or FILETIME, rendered RFC3339 UTC), Duration and ActivityType — as
JSONL or CSV. It covers the `Activity` table (the timeline core); columns a
given Windows build's schema does not carry are omitted, never faked.

SQLite needs a writable working area but the input is mounted read-only, so
gowxt copies the database (and any `-wal`/`-shm` sidecar) into the work dir
and opens the copy; when the work dir is not writable it opens the original
read-only and immutable, which cannot see an un-checkpointed `-wal`.

A sub-tool of [`get-sybers/gowindowlicker`](../README.md), the Windows dozen
as one binary: run as `gowindowlicker gowxt` with no further arguments it
reads its `GOWXT_*` environment block, batches over the input tree, and
prints one JSON summary line.

## Input

`GOWXT_INPUT_DIR` (default `/input`, mounted read-only) is walked recursively;
every SQLite database carrying an `Activity` table is one item, whatever its
name. Other SQLite files are not items.

## Env

| Variable | Default | Meaning |
|---|---|---|
| `GOWXT_INPUT_DIR` | `/input` | evidence tree, recursed |
| `GOWXT_OUT_DIR` | `/output` | output root, one folder per database |
| `GOWXT_WORK_DIR` | `/work` | writable tmpfs holding the SQLite working copy |
| `GOWXT_FORCE` | `0` | `1/true/yes/on`: rerun items that already have valid output |
| `GOWXT_FORMAT` | `json` | record format: `json` (JSONL) or `csv` |
| `GOWXT_LOG_LEVEL` | `info` | `error|warn|info|debug`, stderr only |

## Output

`<OUT_DIR>/<item>/gowxt.jsonl` (or `gowxt.csv`), one record per Activity
row. `<item>` is the database path relative to `GOWXT_INPUT_DIR` with path
separators and whitespace folded to `_`. The record file is written as `.part`
and renamed into place on success, so an item whose record file exists is
skipped on the next run unless `GOWXT_FORCE` is set.

stdout is exactly one JSON object: `tool`, `version`, `status`, `inputs`,
`processed`, `skipped`, `failed`, `records`, `outputs`, `exit`, `started`,
`duration_s`, plus `failures` (item + error) when something failed and `error`
on a config error. Progress and errors go to stderr.

## Exit codes

| Code | Status | Meaning |
|---|---|---|
| 0 | `ok` | every item processed, or already up to date |
| 1 | `nothing` | no ActivitiesCache database found, or every item failed |
| 2 | `config_error` | bad variable, missing or unreadable input, unwritable output |
| 3 | `partial` | at least one item processed and at least one failed |

## Run

```sh
docker build -t get-sybers/gowindowlicker:latest -f gowindowlicker/Dockerfile gowindowlicker
docker run --rm --cap-drop ALL --security-opt no-new-privileges --network none \
  --read-only --tmpfs /work:rw,nosuid,nodev,uid=2000,gid=2000 \
  -v "$PWD/in:/input:ro" -v "$PWD/out:/output" \
  -e GOWXT_FORMAT=csv \
  get-sybers/gowindowlicker:latest gowxt
```

The shared [`test/contract_test.sh`](../test/contract_test.sh) builds the image, runs the sweep over the packages' `testdata/` (a
generated one-row ActivitiesCache.db; `testdata/gen_fixtures.py` remakes it), and
asserts the summary line, the exit code, idempotency and the config-error
exit.

## argv pass-through (debug only, `gowindowlicker gowxt <args>`)

Any argument switches to the single-run argv mode; `--version` prints the
version and `--print-contract` prints `contract.yml`.

- `-f DB` — parse a single ActivitiesCache.db.
- `-d DIR` — scan a directory tree for SQLite databases with an `Activity` table.
- `--json DIR --jsonf NAME` / `--csv DIR --csvf NAME` — write to a file instead of stdout (defaults `Activity_WxTCmd_Output.jsonl` / `.csv`).
- `--work-dir DIR` (default `$TMPDIR`) — writable dir for the SQLite working copy; `-q`.

argv exit codes: 0 parsed, 1 usage or fatal error, 2 at least one database
failed (the rest are still emitted).
