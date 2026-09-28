# `gowindowlicker gojle` — jump list (AutomaticDestinations) parser

Static Go binary reading AutomaticDestinations (`*.automaticDestinations-ms`,
an OLE compound file, via `richardlehane/mscfb`) and their `DestList` stream.
It emits one record per jump-list file in the AutomaticDestinations record
shape that byakugan's `jlecmd_dest` map consumes: `AppId` (with the
well-known friendly name; unknown ids get an empty description, never an
invented one), `SourceFile`, and the per-target `DestListEntries` — `Path`,
`EntryNumber`, `CreatedOn` (from each entry's embedded LNK stream),
`LastModified`, `Hostname`, `InteractionCount`, `MRUPosition`, `Pinned`,
`MacAddress` (from a v1 FileDroid GUID node) and `VolumeDroid`. DestList
versions 1/3/4 are handled; CustomDestinations files are skipped, never
mis-parsed.

A sub-tool of [`get-sybers/gowindowlicker`](../README.md), the Windows dozen
as one binary: run as `gowindowlicker gojle` with no further arguments it
reads its `GOJLE_*` environment block, batches over the input tree, and
prints one JSON summary line.

## Input

`GOJLE_INPUT_DIR` (default `/input`, mounted read-only) is walked recursively;
every file whose path contains `automaticdestinations` (case-insensitive) is
one item.

## Env

| Variable | Default | Meaning |
|---|---|---|
| `GOJLE_INPUT_DIR` | `/input` | evidence tree, recursed |
| `GOJLE_OUT_DIR` | `/output` | output root, one folder per jump list |
| `GOJLE_WORK_DIR` | `/work` | scratch (writable tmpfs); gojle needs none but honours it |
| `GOJLE_FORCE` | `0` | `1/true/yes/on`: rerun items that already have valid output |
| `GOJLE_FORMAT` | `json` | record format; `json` (JSONL) is the only format |
| `GOJLE_LOG_LEVEL` | `info` | `error|warn|info|debug`, stderr only |

## Output

`<OUT_DIR>/<item>/gojle.jsonl`, one record per jump list carrying every
DestList entry. `<item>` is the file path relative to `GOJLE_INPUT_DIR` with
path separators and whitespace folded to `_`. The record file is written as
`.part` and renamed into place on success, so an item whose record file exists
is skipped on the next run unless `GOJLE_FORCE` is set.

stdout is exactly one JSON object: `tool`, `version`, `status`, `inputs`,
`processed`, `skipped`, `failed`, `records`, `outputs`, `exit`, `started`,
`duration_s`, plus `failures` (item + error) when something failed and `error`
on a config error. Progress and errors go to stderr.

## Exit codes

| Code | Status | Meaning |
|---|---|---|
| 0 | `ok` | every item processed, or already up to date |
| 1 | `nothing` | no AutomaticDestinations file found, or every item failed |
| 2 | `config_error` | bad variable, missing or unreadable input, unwritable output |
| 3 | `partial` | at least one item processed and at least one failed |

## Run

```sh
docker build -t get-sybers/gowindowlicker:latest -f gowindowlicker/Dockerfile gowindowlicker
docker run --rm --cap-drop ALL --security-opt no-new-privileges --network none \
  --read-only --tmpfs /work:rw,nosuid,nodev,uid=2000,gid=2000 \
  -v "$PWD/in:/input:ro" -v "$PWD/out:/output" \
  get-sybers/gowindowlicker:latest gojle
```

The shared [`test/contract_test.sh`](../test/contract_test.sh) builds the image and runs the sweep over the packages' `testdata/`.
That directory holds no jump list (a valid OLE compound file cannot be
generated from the standard library), so the test asserts the nothing-to-do
exit `1`, its idempotency, and the config-error exit `2`.

## argv pass-through (debug only, `gowindowlicker gojle <args>`)

Any argument switches to the single-run argv mode; `--version` prints the
version and `--print-contract` prints `contract.yml`.

- `-f FILE` — parse a single `*.automaticDestinations-ms`.
- `-d DIR` — scan a directory tree for AutomaticDestinations files.
- `--tar` — read a tar archive on stdin, as `gomount stream` emits (one entry per file, entry name = the file's volume path), and parse each AutomaticDestinations entry; each is buffered whole (the OLE reader needs random access), one file in memory at a time.
- `--json DIR --jsonf NAME` — write JSONL to a file instead of stdout (default `JLECmd_Output.json`); `-q`.

```sh
gomount stream --filter '*.automaticDestinations-ms' disk.E01 | gowindowlicker gojle --tar
```

argv exit codes: 0 every file parsed, 1 usage or fatal error, 2 at least one
file failed to parse (the rest are still emitted).
