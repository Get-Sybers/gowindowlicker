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


# ---- minimal registry hive (regf) writer -------------------------------------
# One base block (4 KiB) plus one hbin holding every cell. Offsets in nk/vk/lf
# cells are relative to the hbin start, as the format requires. Only the fields
# a reader walks (root cell, subkey lists, value lists, inline/short data) are
# populated; everything else is zero.
REG_SZ, REG_BINARY, REG_DWORD = 1, 3, 4


class Hive:
    def __init__(self):
        self.cells = bytearray()

    def alloc(self, data):
        size = (len(data) + 4 + 7) // 8 * 8
        off = 0x20 + len(self.cells)
        self.cells += struct.pack("<i", -size) + bytes(data) + b"\0" * (size - 4 - len(data))
        return off

    def value(self, name, vtype, data):
        n = name.encode("ascii")
        if len(data) <= 4:
            dlen, doff = len(data) | 0x80000000, struct.unpack("<I", bytes(data).ljust(4, b"\0"))[0]
        else:
            dlen, doff = len(data), self.alloc(data)
        vk = b"vk" + struct.pack("<HIIIHH", len(n), dlen, doff, vtype, 1, 0) + n
        return self.alloc(vk)

    def key(self, name, values=(), subkeys=(), root=False):
        """values: [(name, type, bytes)]; subkeys: [(name, values, subkeys)] built
        depth-first so every child offset exists before its parent nk."""
        child_offs = [self.key(sn, sv, ss) for sn, sv, ss in subkeys]
        val_offs = [self.value(vn, vt, vd) for vn, vt, vd in values]
        sub_list = 0xFFFFFFFF
        if child_offs:
            lf = b"lf" + struct.pack("<H", len(child_offs)) + b"".join(struct.pack("<I4s", o, b"\0\0\0\0") for o in child_offs)
            sub_list = self.alloc(lf)
        val_list = 0xFFFFFFFF
        if val_offs:
            val_list = self.alloc(b"".join(struct.pack("<I", o) for o in val_offs))
        n = name.encode("ascii")
        nk = (b"nk" + struct.pack("<H", 0x2C if root else 0x20) + struct.pack("<Q", FILETIME)
              + struct.pack("<IIIIII", 0, 0, len(child_offs), 0, sub_list, 0xFFFFFFFF)
              + struct.pack("<II", len(val_offs), val_list)
              + struct.pack("<IIIIIII", 0xFFFFFFFF, 0xFFFFFFFF, 0, 0, 0, 0, 0)
              + struct.pack("<HH", len(n), 0) + n)
        return self.alloc(nk)

    def write(self, path, root_off, filename):
        hbin_size = (0x20 + len(self.cells) + 4095) // 4096 * 4096
        body = bytearray(hbin_size)
        body[0:4] = b"hbin"
        struct.pack_into("<II", body, 4, 0, hbin_size)
        struct.pack_into("<Q", body, 20, FILETIME)
        body[0x20:0x20 + len(self.cells)] = self.cells
        free = hbin_size - 0x20 - len(self.cells)
        if free >= 8:
            struct.pack_into("<i", body, 0x20 + len(self.cells), free)
        base = bytearray(4096)
        base[0:4] = b"regf"
        struct.pack_into("<IIQIIIIIII", base, 4, 1, 1, FILETIME, 1, 5, 0, 1, root_off, hbin_size, 1)
        fn = utf16(filename)
        base[48:48 + len(fn)] = fn
        x = 0
        for i in range(0, 508, 4):
            x ^= struct.unpack_from("<I", base, i)[0]
        struct.pack_into("<I", base, 508, x)
        with open(path, "wb") as f:
            f.write(base + body)


def sz(s):
    return utf16(s + "\0")


def dword(n):
    return struct.pack("<I", n)


# A SOFTWARE hive holding the per-machine Run key the bundled batch targets, with
# two autostart values -> gore emits two records.
h = Hive()
root = h.key("ROOT", subkeys=[
    ("Microsoft", [], [
        ("Windows", [], [
            ("CurrentVersion", [], [
                ("Run", [
                    ("OneDrive", REG_SZ, sz(r"C:\Users\analyst\AppData\Local\Microsoft\OneDrive\OneDrive.exe /background")),
                    ("SecurityHealth", REG_SZ, sz(r"C:\Windows\system32\SecurityHealthSystray.exe")),
                ], []),
            ]),
        ]),
    ]),
], root=True)
h.write(os.path.join(HERE, "SOFTWARE"), root, "SOFTWARE")
print("wrote", os.path.join(HERE, "SOFTWARE"))
