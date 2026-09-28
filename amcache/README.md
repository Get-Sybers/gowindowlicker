# `gowindowlicker goamcache` — `Amcache.hve` parser

Static Go binary on Velociraptor's `regparser`. Parses an `Amcache.hve` and
emits one record per program-execution file entry
(`Root\InventoryApplicationFile`) — the key's last-write time, ProgramId, the
SHA-1 (the `0000`-prefixed `FileId` stripped to the bare 40-hex hash), full
path, name, publisher/product/version and size — as JSONL or CSV. Dirty-hive
`.LOG1`/`.LOG2` transaction logs are replayed (`regparser.RecoverHive`) when
they sit beside the hive; the recovered copy is written under the work dir. If
the logs are absent, replay fails, or the work dir is not writable, the
committed hive is parsed with a stderr note, never a hard fail.

A sub-tool of [`get-sybers/gowindowlicker`](../README.md), the Windows dozen
as one binary: run as `gowindowlicker goamcache` with no further arguments it
reads its `GOAMCACHE_*` environment block, batches over the input tree, and
prints one JSON summary line.

## Input

`GOAMCACHE_INPUT_DIR` (default `/input`, mounted read-only) is walked
recursively; every registry hive (`regf` signature) that carries
`Root\InventoryApplicationFile` is one item, whatever its name. Other hives
(SYSTEM, SOFTWARE, NTUSER.DAT, …) and `.LOG*` files are not items.

## Env

| Variable | Default | Meaning |
|---|---|---|
| `GOAMCACHE_INPUT_DIR` | `/input` | evidence tree, recursed |
| `GOAMCACHE_OUT_DIR` | `/output` | output root, one folder per hive |
| `GOAMCACHE_WORK_DIR` | `/work` | writable tmpfs for the recovered copy during `.LOG` replay |
| `GOAMCACHE_FORCE` | `0` | `1/true/yes/on`: rerun items that already have valid output |
| `GOAMCACHE_FORMAT` | `json` | record format: `json` (JSONL) or `csv` |
| `GOAMCACHE_LOG_LEVEL` | `info` | `error|warn|info|debug`, stderr only |

## Output

`<OUT_DIR>/<item>/goamcache.jsonl` (or `goamcache.csv`), one record per
InventoryApplicationFile entry. `<item>` is the hive path relative to
`GOAMCACHE_INPUT_DIR` with path separators and whitespace folded to `_`. The
record file is written as `.part` and renamed into place on success, so an item
whose record file exists is skipped on the next run unless `GOAMCACHE_FORCE` is
set.

stdout is exactly one JSON object: `tool`, `version`, `status`, `inputs`,
`processed`, `skipped`, `failed`, `records`, `outputs`, `exit`, `started`,
`duration_s`, plus `failures` (item + error) when something failed and `error`
on a config error. Progress and errors go to stderr.

## Exit codes

| Code | Status | Meaning |
|---|---|---|
| 0 | `ok` | every item processed, or already up to date |
| 1 | `nothing` | no Amcache hive found, or every item failed |
| 2 | `config_error` | bad variable, missing or unreadable input, unwritable output |
| 3 | `partial` | at least one item processed and at least one failed |

## Run

```sh
docker build -t get-sybers/gowindowlicker:latest -f gowindowlicker/Dockerfile gowindowlicker
docker run --rm --cap-drop ALL --security-opt no-new-privileges --network none \
  --read-only --tmpfs /work:rw,nosuid,nodev,uid=2000,gid=2000 \
  -v "$PWD/in:/input:ro" -v "$PWD/out:/output" \
  -e GOAMCACHE_FORMAT=csv \
  get-sybers/gowindowlicker:latest goamcache
```

The shared [`test/contract_test.sh`](../test/contract_test.sh) builds the image, runs the sweep over the packages' `testdata/` (a
generated Amcache hive with one entry; `testdata/gen_fixtures.py` remakes it), and
asserts the summary line, the exit code, idempotency and the config-error exit.

## argv pass-through (debug only, `gowindowlicker goamcache <args>`)

Any argument switches to the single-run argv mode; `--version` prints the
version and `--print-contract` prints `contract.yml`.

- `-f FILE` — parse a single `Amcache.hve`.
- `-d DIR` — scan a directory tree and parse every hive carrying the Amcache key.
- `--json DIR --jsonf NAME` / `--csv DIR --csvf NAME` — write to a file instead of stdout (defaults `Amcache_Output.jsonl` / `.csv`).
- `--work-dir DIR` (default `$TMPDIR`) — writable dir for the recovered hive during `.LOG` replay.
- `-i` — accepted for compatibility (file entries are always emitted); `-q`.

argv exit codes: 0 every hive parsed, 1 usage, fatal error or no records
emitted, 2 at least one file failed to parse (the rest are still emitted).
