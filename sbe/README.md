# `gowindowlicker gosbe` — ShellBags (BagMRU) parser

Static Go binary on Velociraptor's `regparser`. Walks the **BagMRU** tree in
`NTUSER.DAT` / `UsrClass.dat` (all the Shell / ShellNoRoam roots), decodes the
shell items, reconstructs each shellbag's `AbsolutePath`, and emits one record
per shellbag — `HivePath`, `BagPath`, `Slot`, `NodeSlot`, `MRUPosition`,
`ShellType`, `Value`, `AbsolutePath`, `LastWriteTime` — as JSONL.

Shell-item decoding covers the common types: `0x1F` root/GUID folders (mapped
to known-folder names), `0x2F` volumes (drive letters), and `0x30-0x3F`
file/directory entries (the `BEEF0004` extension's Unicode long name, with the
ANSI short name as fallback). Other shell-item types (property/delegate
`0x00`, network `0x40-0x4F`, URI `0x61`, …) are emitted with their `ShellType`
and hex value but no reconstructed name, never an invented path. Dirty-hive
`.LOG1`/`.LOG2` transaction logs are replayed into a recovered copy under the
work dir; without a writable work dir, replay falls back to the committed hive
with a stderr note.

A sub-tool of [`get-sybers/gowindowlicker`](../README.md), the Windows dozen
as one binary: run as `gowindowlicker gosbe` with no further arguments it
reads its `GOSBE_*` environment block, batches over the input tree, and
prints one JSON summary line.

## Input

`GOSBE_INPUT_DIR` (default `/input`, mounted read-only) is walked recursively;
every `NTUSER.DAT` or `UsrClass.dat` (case-insensitive) carrying the `regf`
signature is one item.

## Env

| Variable | Default | Meaning |
|---|---|---|
| `GOSBE_INPUT_DIR` | `/input` | evidence tree, recursed |
| `GOSBE_OUT_DIR` | `/output` | output root, one folder per hive |
| `GOSBE_WORK_DIR` | `/work` | writable tmpfs for the recovered copy during `.LOG` replay |
| `GOSBE_FORCE` | `0` | `1/true/yes/on`: rerun items that already have valid output |
| `GOSBE_FORMAT` | `json` | record format; `json` (JSONL) is the only format |
| `GOSBE_REPLAY` | `1` | `1/true/yes/on`: replay `.LOG1`/`.LOG2` found beside a hive |
| `GOSBE_LOG_LEVEL` | `info` | `error|warn|info|debug`, stderr only |

## Output

`<OUT_DIR>/<item>/gosbe.jsonl`, one record per shellbag. `<item>` is the hive
path relative to `GOSBE_INPUT_DIR` with path separators and whitespace folded
to `_`. The record file is written as `.part` and renamed into place on
success, so an item whose record file exists is skipped on the next run unless
`GOSBE_FORCE` is set.

stdout is exactly one JSON object: `tool`, `version`, `status`, `inputs`,
`processed`, `skipped`, `failed`, `records`, `outputs`, `exit`, `started`,
`duration_s`, plus `failures` (item + error) when something failed and `error`
on a config error. Progress and errors go to stderr.

## Exit codes

| Code | Status | Meaning |
|---|---|---|
| 0 | `ok` | every item processed, or already up to date |
| 1 | `nothing` | no per-user hive found, or every item failed |
| 2 | `config_error` | bad variable, missing or unreadable input, unwritable output |
| 3 | `partial` | at least one item processed and at least one failed |

## Run

```sh
docker build -t get-sybers/gowindowlicker:latest -f gowindowlicker/Dockerfile gowindowlicker
docker run --rm --cap-drop ALL --security-opt no-new-privileges --network none \
  --read-only --tmpfs /work:rw,nosuid,nodev,uid=2000,gid=2000 \
  -v "$PWD/in:/input:ro" -v "$PWD/out:/output" \
  get-sybers/gowindowlicker:latest gosbe
```

The shared [`test/contract_test.sh`](../test/contract_test.sh) builds the image, runs the sweep over the packages' `testdata/` (a
generated NTUSER.DAT with a two-level BagMRU; `testdata/gen_fixtures.py` remakes
it), and asserts the summary line, the exit code, idempotency and the
config-error exit.

## argv pass-through (debug only, `gowindowlicker gosbe <args>`)

Any argument switches to the single-run argv mode; `--version` prints the
version and `--print-contract` prints `contract.yml`.

- `-f HIVE` — parse a single `NTUSER.DAT` / `UsrClass.dat`.
- `-d DIR` — scan a directory tree for hives by their `regf` header.
- `--json DIR --jsonf NAME` — write JSONL to a file instead of stdout (default `SBECmd_Output.json`).
- `--work-dir DIR` (default `$TMPDIR`) — writable dir for the recovered hive; `--nl` skips `.LOG` replay; `-q`.

argv exit codes: 0 ok, 1 usage or fatal error, 2 at least one hive failed to
parse (the rest are still emitted).
