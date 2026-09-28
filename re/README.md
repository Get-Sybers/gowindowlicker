# `gowindowlicker gore` — batch-driven registry dumper

Static Go binary on Velociraptor's `regparser`. Reads a **batch file** (`.reb`
YAML: a list of keys with `HiveType`, `Category`, `KeyPath`, `ValueName`,
`Recursive`, `Comment`), walks each requested key in each hive (hives
content-detected by their `regf` header, `HiveType` inferred from the file
name, `.LOG*` files skipped), and emits one record per value — `HivePath`,
`HiveType`, `Category`, `Description`, `Comment`, `KeyPath`, `ValueName`,
`ValueType`, `ValueData`, `LastWriteTimestamp`, `Recursive`, `Deleted` — as
JSONL or CSV, the exact shape byakugan's `recmd_batch` map consumes.

The bundled `/batch/default.reb` is a curated forensic-key set (Run/RunOnce,
TypedPaths, ComputerName/TimeZone, …). gore runs the batch's key/value
extraction only: no derived-value plugin transforms and no deleted-cell
recovery (`Deleted` is always false). Dirty-hive `.LOG1`/`.LOG2` transaction
logs are replayed into a recovered copy under the work dir; without a writable
work dir, replay falls back to the committed hive with a stderr note.

A sub-tool of [`get-sybers/gowindowlicker`](../README.md), the Windows dozen
as one binary: run as `gowindowlicker gore` with no further arguments it
reads its `GORE_*` environment block, batches over the input tree, and
prints one JSON summary line.

## Input

`GORE_INPUT_DIR` (default `/input`, mounted read-only) is walked recursively;
every registry hive (`regf` signature) that is not a `.LOG*` file is one item.
A batch key whose `HiveType` does not match the hive's file name is skipped
for that hive.

## Env

| Variable | Default | Meaning |
|---|---|---|
| `GORE_INPUT_DIR` | `/input` | evidence tree, recursed |
| `GORE_OUT_DIR` | `/output` | output root, one folder per hive |
| `GORE_WORK_DIR` | `/work` | writable tmpfs for the recovered copy during `.LOG` replay |
| `GORE_FORCE` | `0` | `1/true/yes/on`: rerun items that already have valid output |
| `GORE_FORMAT` | `json` | record format: `json` (JSONL) or `csv` |
| `GORE_BATCH` | `/batch/default.reb` | the `.reb` batch definition to extract |
| `GORE_REPLAY` | `1` | `1/true/yes/on`: replay `.LOG1`/`.LOG2` found beside a hive |
| `GORE_LOG_LEVEL` | `info` | `error|warn|info|debug`, stderr only |

## Output

`<OUT_DIR>/<item>/gore.jsonl` (or `gore.csv`), one record per extracted value.
`<item>` is the hive path relative to `GORE_INPUT_DIR` with path separators
and whitespace folded to `_`. The record file is written as `.part` and
renamed into place on success, so an item whose record file exists is skipped
on the next run unless `GORE_FORCE` is set.

stdout is exactly one JSON object: `tool`, `version`, `status`, `inputs`,
`processed`, `skipped`, `failed`, `records`, `outputs`, `exit`, `started`,
`duration_s`, plus `failures` (item + error) when something failed and `error`
on a config error. Progress and errors go to stderr.

## Exit codes

| Code | Status | Meaning |
|---|---|---|
| 0 | `ok` | every item processed, or already up to date |
| 1 | `nothing` | no registry hive found, or every item failed |
| 2 | `config_error` | bad variable, unreadable batch file, missing or unreadable input, unwritable output |
| 3 | `partial` | at least one item processed and at least one failed |

## Run

```sh
docker build -t get-sybers/gowindowlicker:latest -f gowindowlicker/Dockerfile gowindowlicker
docker run --rm --cap-drop ALL --security-opt no-new-privileges --network none \
  --read-only --tmpfs /work:rw,nosuid,nodev,uid=2000,gid=2000 \
  -v "$PWD/in:/input:ro" -v "$PWD/out:/output" \
  -e GORE_BATCH=/batch/default.reb \
  get-sybers/gowindowlicker:latest gore
```

The shared [`test/contract_test.sh`](../test/contract_test.sh) builds the image, runs the sweep over the packages' `testdata/` (a
generated SOFTWARE hive with two Run-key values; `testdata/gen_fixtures.py`
remakes it), and asserts the summary line, the exit code, idempotency and the
config-error exit.

## argv pass-through (debug only, `gowindowlicker gore <args>`)

Any argument switches to the single-run argv mode; `--version` prints the
version and `--print-contract` prints `contract.yml`.

- `-f HIVE` — process a single hive.
- `-d DIR` — scan a directory tree for hives by their `regf` header.
- `--bn FILE` — batch definition (default `/batch/default.reb`).
- `--json DIR --jsonf NAME` / `--csv DIR --csvf NAME` — write to a file instead of stdout (defaults `RECmd_Batch_Output.json` / `.csv`).
- `--work-dir DIR` (default `$TMPDIR`) — writable dir for the recovered hive; `--nl` skips `.LOG` replay; `-q`.

argv exit codes: 0 ok, 1 usage or fatal error, 2 at least one hive failed to
parse (the rest are still emitted).
