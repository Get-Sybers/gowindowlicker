#!/usr/bin/env python3
"""testdata/gen_fixtures.py — deterministic generator for this testdata/ (stdlib only).

Run it from anywhere; it rewrites the fixture files beside it. The fixtures are
committed so the contract test needs no generation step; this script is how
they were made and how to remake them.
"""
import os, struct, sys

HERE = os.path.dirname(os.path.abspath(__file__))
os.makedirs(HERE, exist_ok=True)

# FILETIME (100 ns ticks since 1601) for a fixed instant: 2024-03-01T12:00:00Z.
EPOCH_GAP = 11644473600
FIXED_UNIX = 1709294400
FILETIME = (FIXED_UNIX + EPOCH_GAP) * 10_000_000


def utf16(s):
    return s.encode("utf-16-le")


# A structurally valid but empty .evtx: the 4 KiB file header (ElfFile magic,
# format 3.1) and no chunks -> the log parses with zero events.
buf = bytearray(4096)
buf[0:8] = b"ElfFile\0"
struct.pack_into("<QQQIHHH", buf, 8, 0, 0, 1, 128, 1, 3, 4096)
out = os.path.join(HERE, "Empty.evtx")
open(out, "wb").write(buf)
print("wrote", out)
