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


# One v2 (Win8.1+) $I record, stored under Plaso's renamed form (_I...) to show
# that content detection, not the name, finds it.
path = r"C:\Users\analyst\Documents\quarterly-report.docx"
p = utf16(path + "\0")
rec = struct.pack("<qqQ", 2, 48213, FILETIME) + struct.pack("<I", len(p) // 2) + p
out = os.path.join(HERE, "_IQ3K5N7.docx")
open(out, "wb").write(rec)
print("wrote", out)
