# `gowindowlicker goese` — ESE database dumper (SRUM / SUM)

Static Go binary on Velociraptor's `go-ese` (pure-Go ESE). Dumps every table
of a SRUM `SRUDB.dat` or a SUM `Current.mdb` — or any ESE database — one file
per table. On SRUM databases the `SruDbIdMapTable` is decoded automatically:
`AppId`/`UserId` columns gain `AppIdName`/`UserIdName` (UTF-16 strings, or the
SID for IdType 3 entries), and the well-known provider GUID tables get
friendly file names (`ApplicationResourceUsage`, `NetworkDataUsage`,
`NetworkConnectivityUsage`, `EnergyUsage[LT]`, `AppTimelineProvider`,
`PushNotifications`); the raw table name stays in every row. ESE DateTime
columns arrive as RFC3339.

A sub-tool of [`get-sybers/gowindowlicker`](../README.md), the Windows dozen
as one binary: run as `gowindowlicker goese` with no further arguments it
reads its `GOESE_*` environment block, batches over the input tree, and
prints one JSON summary line.

## Input

`GOESE_INPUT_DIR` (default `/input`, mounted read-only) is walked recursively;
every `SRUDB.dat` and every `*.mdb` directly under a `SUM/` directory
(case-insensitive) is one item.

## Env

| Variable | Default | Meaning |
|---|---|---|
| `GOESE_INPUT_DIR` | `/input` | evidence tree, recursed |
| `GOESE_OUT_DIR` | `/output` | output root, one folder per database |
| `GOESE_WORK_DIR` | `/work` | scratch (writable tmpfs); goese needs none but honours it |
| `GOESE_FORCE` | `0` | `1/true/yes/on`: rerun items that already have valid output |
| `GOESE_FORMAT` | `json` | format of the per-table files and the index: `json` (JSONL) or `csv` |
| `GOESE_TABLES` | *(empty)* | comma-separated tables to dump (name, SRUM alias or GUID); empty = every non-`MSys` table; a table absent from a database is noted and skipped |
| `GOESE_LOG_LEVEL` | `info` | `error|warn|info|debug`, stderr only |

## Output

Per database, `<OUT_DIR>/<item>/<table>.jsonl` (or `.csv`) — one file per
dumped table, rows carrying `SourceDb`, `Table` and `TableAlias` — plus
`<OUT_DIR>/<item>/goese.jsonl` (or `goese.csv`), the index of dumped tables
(`Table`, `TableAlias`, `File`, `Rows`). `<item>` is the database path
relative to `GOESE_INPUT_DIR` with path separators and whitespace folded to
`_`. The index is written last and marks the item done: an item whose index
exists is skipped on the next run unless `GOESE_FORCE` is set; a database with
a failed table has no index and is redumped next run.

stdout is exactly one JSON object: `tool`, `version`, `status`, `inputs`,
`processed`, `skipped`, `failed`, `records` (rows across all tables),
`outputs`, `exit`, `started`, `duration_s`, plus `failures` (item + error)
when something failed and `error` on a config error. Progress and errors go to
stderr.

## Exit codes

| Code | Status | Meaning |
|---|---|---|
| 0 | `ok` | every item processed, or already up to date |
| 1 | `nothing` | no SRUM or SUM database found, or every item failed |
| 2 | `config_error` | bad variable, missing or unreadable input, unwritable output |
| 3 | `partial` | at least one item processed and at least one failed |

## Run

```sh
docker build -t get-sybers/gowindowlicker:latest -f gowindowlicker/Dockerfile gowindowlicker
docker run --rm --cap-drop ALL --security-opt no-new-privileges --network none \
  --read-only --tmpfs /work:rw,nosuid,nodev,uid=2000,gid=2000 \
  -v /mnt/image:/input:ro -v "$PWD/out:/output" \
  -e GOESE_TABLES= \
  get-sybers/gowindowlicker:latest goese
```

The shared [`test/contract_test.sh`](../test/contract_test.sh) builds the image and runs the sweep over the packages' `testdata/`.
That directory holds no ESE database (a valid one cannot be generated from the
standard library), so the test asserts the nothing-to-do exit `1`, its
idempotency, and the config-error exit `2`.

## argv pass-through (debug only, `gowindowlicker goese <args>`)

Any argument switches to the single-run argv mode; `--version` prints the
version and `--print-contract` prints `contract.yml`.

- `-f DB` — dump one extracted database exactly as requested; an unknown `-t` table is fatal.
- `-d ROOT` — a mounted image root: every SRUM/SUM database is dumped into its own sub-directory (`SRUM_SRUDB/`, `SUM_Current/`, …) with a `SourceDb` field on every row.
- `-t TABLES` — comma-separated tables (name, alias or GUID); `--list` prints tables and columns and exits.
- `--json DIR` / `--csv DIR` — per-table files under DIR (default: a JSONL stream on stdout); `-q`.

argv exit codes: 0 all requested tables dumped, 1 usage or fatal error, 2 at
least one table or database failed (the rest are still written).
