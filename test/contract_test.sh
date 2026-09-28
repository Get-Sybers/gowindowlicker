#!/usr/bin/env bash
# gowindowlicker contract smoke: the default (bare) run over the packages'
# committed testdata asserts the sweep — every parser over one evidence tree,
# each into its own <OUT_DIR>/<subtool>/ tree, exactly one aggregate JSON
# line — plus idempotency, the config-error exit, the argv pass-through and a
# single-subtool run.
#
# Default: builds and runs the hardened image (docker, the REPO ROOT as
# context — the image bakes the sibling gomount/). IMAGE=<ref> reuses a built
# image instead of building;
# CONTRACT_LOCAL=1 builds the binary with the host Go toolchain instead.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
tool_dir="$(dirname "$here")"
work="$(mktemp -d)"
# the image writes its output as uid 2000: what the host user cannot remove
# is left for the OS's tmp reaper rather than failing a passed test
trap 'rm -rf "$work" 2>/dev/null || true' EXIT

# One evidence tree from the parser packages' committed testdata; goese and
# gojle have none (their formats are exercised in-package), so the sweep also
# proves that an absent artefact class reports "nothing" without failing.
in="$work/in" out="$work/out" scratch="$work/scratch"
mkdir -p "$in/Windows/Prefetch" "$in/Windows/System32/config" \
    "$in/Windows/System32/winevt/Logs" "$in/Windows/appcompat/Programs" \
    "$in/\$Recycle.Bin/S-1-5-21-1" "$in/Users/analyst/Desktop" \
    "$in/Users/analyst/AppData/Local/ConnectedDevicesPlatform/L.analyst" \
    "$out" "$scratch"
cp "$tool_dir/prefetch/testdata/NOTEPAD.EXE-5C7A6B9D.pf" "$in/Windows/Prefetch/"
cp "$tool_dir/rb/testdata/_IQ3K5N7.docx" "$in/\$Recycle.Bin/S-1-5-21-1/"
cp "$tool_dir/mft/testdata/_MFT" "$in/"
cp "$tool_dir/amcache/testdata/Amcache.hve" "$in/Windows/appcompat/Programs/"
cp "$tool_dir/appcompat/testdata/SYSTEM" "$in/Windows/System32/config/"
cp "$tool_dir/re/testdata/SOFTWARE" "$in/Windows/System32/config/"
cp "$tool_dir/evtx/testdata/Empty.evtx" "$in/Windows/System32/winevt/Logs/"
cp "$tool_dir/sbe/testdata/NTUSER.DAT" "$in/Users/analyst/"
cp "$tool_dir/le/testdata/sample.lnk" "$in/Users/analyst/Desktop/"
cp "$tool_dir/wxt/testdata/ActivitiesCache.db" "$in/Users/analyst/AppData/Local/ConnectedDevicesPlatform/L.analyst/"
chmod -R a+rX "$in"; chmod 777 "$out" "$scratch"

run() {
    local code=0
    if [[ "${CONTRACT_LOCAL:-0}" == "1" ]]; then
        env GOWINDOWLICKER_INPUT_DIR="$in" GOWINDOWLICKER_OUT_DIR="$out" \
            GOWINDOWLICKER_WORK_DIR="$scratch" GORE_BATCH="$tool_dir/re/batch/default.reb" "$@" \
            "$work/gowindowlicker" >"$work/stdout" 2>"$work/stderr" || code=$?
    else
        docker run --rm --network none --read-only --cap-drop ALL \
            --security-opt no-new-privileges \
            --tmpfs /work:rw,nosuid,nodev,uid=2000,gid=2000 --tmpfs /tmp:rw,uid=2000,gid=2000 \
            -v "$in:/input:ro" -v "$out:/output" \
            "$IMAGE" >"$work/stdout" 2>"$work/stderr" || code=$?
    fi
    return $code
}

if [[ "${CONTRACT_LOCAL:-0}" == "1" ]]; then
    (cd "$tool_dir" && CGO_ENABLED=0 go build -o "$work/gowindowlicker" .)
elif [[ -z "${IMAGE:-}" ]]; then
    IMAGE=get-sybers/gowindowlicker:latest
    (cd "$(dirname "$tool_dir")" && docker build -q -t "$IMAGE" -f gowindowlicker/Dockerfile .)
fi

fail() { echo "FAIL: $*" >&2; cat "$work/stderr" >&2 || true; exit 1; }
field() { python3 -c "import json;print(json.load(open('$work/stdout'))['$1'])"; }

code=0; run || code=$?
[[ $code -eq 0 ]] || fail "default run exit $code, want 0"
[[ "$(wc -l <"$work/stdout")" == "1" ]] || fail "stdout is not one line"
[[ "$(field status)" == "ok" ]] || fail "status $(field status)"
python3 -c "
import json; s=json.load(open('$work/stdout'))
assert len(s['subtools']) == 12, [t['tool'] for t in s['subtools']]
assert s['failed'] == 0, s" || fail "sweep did not run all 12 parsers cleanly"
ls "$out"/goprefetch/*/goprefetch.jsonl >/dev/null 2>&1 || fail "goprefetch output missing"
ls "$out"/gorb/*/gorb.jsonl >/dev/null 2>&1 || fail "gorb output missing"
ls "$out"/gosbe/*/gosbe.jsonl >/dev/null 2>&1 || fail "gosbe output missing"
grep -q '"FileName":' "$out"/gorb/*/gorb.jsonl || fail "gorb record shape missing"

code=0; run || code=$?
[[ $code -eq 0 ]] || fail "rerun exit $code"
python3 -c "
import json; s=json.load(open('$work/stdout'))
assert s['processed']==0 and s['skipped']>0, s" || fail "rerun not idempotent"

if [[ "${CONTRACT_LOCAL:-0}" == "1" ]]; then
    code=0
    env GOWINDOWLICKER_INPUT_DIR="$in/missing" GOWINDOWLICKER_OUT_DIR="$out" \
        GOWINDOWLICKER_WORK_DIR="$scratch" \
        "$work/gowindowlicker" lick >"$work/stdout" 2>"$work/stderr" || code=$?
    [[ $code -eq 2 ]] || fail "config-error exit $code, want 2"

    # argv debug pass-through (the modes ride the dispatcher): gorb -f on the
    # fixture streams the record to stdout.
    "$work/gowindowlicker" gorb -f "$in/\$Recycle.Bin/S-1-5-21-1/_IQ3K5N7.docx" -q \
        >"$work/argv-out" 2>"$work/stderr" || fail "argv pass-through exit $?"
    grep -q '"FileType":"\$I"' "$work/argv-out" || fail "argv pass-through emitted no gorb record"

    # single-subtool run: its canonical env block, its ordinary summary line.
    out2="$work/out2"; mkdir -p "$out2"
    env GOSBE_INPUT_DIR="$in" GOSBE_OUT_DIR="$out2" GOSBE_WORK_DIR="$scratch" \
        "$work/gowindowlicker" gosbe >"$work/stdout" 2>"$work/stderr" || fail "subtool run exit $?"
    [[ "$(field tool)" == "gosbe" ]] || fail "subtool summary names $(field tool)"
    ls "$out2"/*/gosbe.jsonl >/dev/null 2>&1 || fail "subtool run wrote nothing"
fi

echo "gowindowlicker contract smoke: PASS"
