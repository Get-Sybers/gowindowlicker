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


# A WinXP-format (version 17, uncompressed) prefetch file for NOTEPAD.EXE with
# one file-metrics entry and a run count of 3.
exe = "NOTEPAD.EXE"
name = utf16(r"\DEVICE\HARDDISKVOLUME1\WINDOWS\SYSTEM32\NOTEPAD.EXE") + b"\0\0"
metrics_off = 84 + 68            # one 20-byte FileMetricsEntryV17
names_off = metrics_off + 20
total = names_off + len(name)
buf = bytearray(total)
struct.pack_into("<I4sII", buf, 0, 17, b"SCCA", 0x0F, total)
buf[16:16 + len(utf16(exe))] = utf16(exe)
struct.pack_into("<I", buf, 76, 0x5C7A6B9D)
# FileInformationXP @84
struct.pack_into("<IIIIII", buf, 84, metrics_off, 1, 0, 0, names_off, len(name))
struct.pack_into("<Q", buf, 84 + 36, FILETIME)
struct.pack_into("<I", buf, 84 + 60, 3)
# FileMetricsEntryV17: FilenameOffset @8, FilenameLength @12 (in characters)
struct.pack_into("<II", buf, metrics_off + 8, 0, (len(name) - 2) // 2)
buf[names_off:names_off + len(name)] = name
out = os.path.join(HERE, "NOTEPAD.EXE-5C7A6B9D.pf")
open(out, "wb").write(buf)
print("wrote", out)
