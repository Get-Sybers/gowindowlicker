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

import sqlite3

# A minimal ActivitiesCache.db: the Activity table with one row shaped like a
# Windows 10 timeline record (epoch-second timestamps, JSON AppId/Payload).
out = os.path.join(HERE, "ActivitiesCache.db")
if os.path.exists(out):
    os.remove(out)
db = sqlite3.connect(out)
db.execute("PRAGMA page_size=1024")
db.execute("""CREATE TABLE Activity (
    Id BLOB PRIMARY KEY, AppId TEXT, PackageIdHash TEXT, AppActivityId TEXT,
    ActivityType INT, ActivityStatus INT, ParentActivityId BLOB, Tag TEXT, "Group" TEXT,
    MatchId TEXT, LastActiveTime INT, ExpirationTime INT, Payload BLOB, Priority INT,
    IsLocalOnly INT, PlatformDeviceId TEXT, CreatedInCloud INT, StartTime INT, EndTime INT,
    LastModifiedTime INT, LastModifiedOnClient INT, GroupAppActivityId TEXT, ClipboardPayload BLOB,
    EnterpriseId TEXT, OriginalPayload BLOB, OriginalLastModifiedOnClient INT, ETag INT)""")
db.execute("INSERT INTO Activity (Id, AppId, ActivityType, StartTime, EndTime, LastModifiedTime, ExpirationTime, IsLocalOnly, PlatformDeviceId, PackageIdHash, ETag, Payload) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)",
           (bytes(range(16)),
            '[{"application":"C:\\\\Windows\\\\System32\\\\notepad.exe","platform":"windows_win32"}]',
            5, FIXED_UNIX, FIXED_UNIX + 90, FIXED_UNIX + 90, FIXED_UNIX + 86400 * 30, 1,
            "d3v1c3", "pkghash", 7,
            '{"displayText":"notes.txt","description":"C:\\\\Users\\\\analyst\\\\notes.txt"}'))
db.commit()
db.close()
print("wrote", out)
