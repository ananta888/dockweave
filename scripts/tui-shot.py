#!/usr/bin/env python3
"""tui-shot: render DockWeave TUI frames as plain ASCII.

Development helper: runs the TUI in a pseudo-terminal, injects keys or
mouse clicks and prints the rendered screen after each step. This makes
the TUI testable and reviewable as text (CI logs, code review, chat).

Usage:
  scripts/tui-shot.py                     # first frame, then quit
  scripts/tui-shot.py --step enter        # press enter, dump frame
  scripts/tui-shot.py --step t --wait 3   # press t, wait 3s, dump
  scripts/tui-shot.py --click 30 2        # left click at col 30 row 2
  scripts/tui-shot.py --binary ./bin/dockweave -- cmd args...

Steps run in order; a frame is printed after each step (and at start).
"""
import argparse
import codecs
import fcntl
import os
import re
import select
import struct
import sys
import termios
import time

CSI_RE = re.compile(rb"\x1b\[([0-9;?<>]*)([@-~])")
OSC_RE = re.compile(rb"\x1b\][^\x07\x1b]*(\x07|\x1b\\)")


class Screen:
    """Minimal VT100 subset: printable text, CUP, ED, EL, CR/LF."""

    def __init__(self, rows, cols):
        self.rows, self.cols = rows, cols
        self.clear()

    def clear(self):
        self.grid = [[" "] * self.cols for _ in range(self.rows)]
        self.cy = self.cx = 0
        self.dec = codecs.getincrementaldecoder("utf-8")("replace")

    def put(self, ch):
        if self.cy < self.rows and self.cx < self.cols:
            self.grid[self.cy][self.cx] = ch
        self.cx += 1

    def feed(self, data: bytes):
        i = 0
        while i < len(data):
            b = data[i]
            if b == 0x1B:
                m = CSI_RE.match(data, i)
                if m:
                    self._csi(m.group(1), m.group(2))
                    i = m.end()
                    continue
                m = OSC_RE.match(data, i)
                if m:
                    i = m.end()
                    continue
                i += 1  # skip other escapes
                continue
            if b == 0x0D:
                self.cx = 0
            elif b == 0x0A:
                self.cy += 1
                if self.cy >= self.rows:
                    self.grid.pop(0)
                    self.grid.append([" "] * self.cols)
                    self.cy = self.rows - 1
            elif b == 0x08:
                self.cx = max(0, self.cx - 1)
            elif b >= 0x20:
                ch = self.dec.decode(bytes([b]))
                if ch:
                    self.put(ch)
            i += 1

    def _csi(self, params, final):
        p = [int(x) for x in params.split(b";") if x.isdigit()]
        if final in (b"H", b"f"):
            self.cy = (p[0] - 1) if p else 0
            self.cx = (p[1] - 1) if len(p) > 1 else 0
            self.cy = max(0, min(self.rows - 1, self.cy))
            self.cx = max(0, min(self.cols - 1, self.cx))
        elif final == b"J":
            self.clear() if not p or p[0] in (2, 3) else None
        elif final == b"K":
            for x in range(self.cx, self.cols):
                self.grid[self.cy][x] = " "
        elif final == b"A":
            self.cy = max(0, self.cy - (p[0] if p else 1))
        elif final == b"B":
            self.cy = min(self.rows - 1, self.cy + (p[0] if p else 1))
        elif final == b"C":
            self.cx = min(self.cols - 1, self.cx + (p[0] if p else 1))
        elif final == b"D":
            self.cx = max(0, self.cx - (p[0] if p else 1))

    def render(self) -> str:
        return "\n".join("".join(r).rstrip() for r in self.grid)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--binary", default="/usr/local/bin/dockweave")
    ap.add_argument("--rows", type=int, default=30)
    ap.add_argument("--cols", type=int, default=120)
    ap.add_argument("--wait", type=float, default=1.2, help="seconds to settle before first dump")
    ap.add_argument("--step", action="append", default=[],
                    help="key to send (repeatable): enter, esc, t, q, down, up, tab, or a literal char")
    ap.add_argument("--click", action="append", default=[],
                    help="left click as COL,ROW (repeatable)")
    ap.add_argument("cmd", nargs="*", default=["tui"])
    args = ap.parse_args()

    keys = {"enter": b"\r", "esc": b"\x1b", "down": b"\x1b[B", "up": b"\x1b[A",
            "tab": b"\t", "q": b"q", "t": b"t", "r": b"r", "space": b" "}

    pid, fd = os.forkpty()
    if pid == 0:
        os.execv(args.binary, [args.binary] + args.cmd)
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", args.rows, args.cols, 0, 0))
    scr = Screen(args.rows, args.cols)

    def pump(seconds):
        end = time.time() + seconds
        while time.time() < end:
            r, _, _ = select.select([fd], [], [], 0.1)
            if r:
                try:
                    data = os.read(fd, 65536)
                except OSError:
                    return False
                if not data:
                    return False
                # Answer terminal queries like a real terminal would
                # (bubbletea waits for OSC 11 background and DSR cursor
                # position before its first render).
                if b"\x1b]11;?" in data:
                    os.write(fd, b"\x1b]11;rgb:0000/0000/0000\x1b\\")
                if b"\x1b[6n" in data:
                    os.write(fd, b"\x1b[1;1R")
                scr.feed(data)
        return True

    def dump(title):
        print(f"\n===== {title} =====")
        print(scr.render())
        print("=====")

    pump(args.wait)
    dump("initial")

    steps = []
    for c in args.click:
        col, row = c.split(",")
        steps.append(("click " + c, ("\x1b[<0;%s;%sM" % (col, row)).encode(), 0.6))
        steps.append(("release", ("\x1b[<0;%s;%sm" % (col, row)).encode(), 0.3))
    for s in args.step:
        steps.append(("key " + s, keys.get(s, s.encode()), 1.0))

    for title, payload, settle in steps:
        os.write(fd, payload)
        pump(settle)
        dump(title)

    os.write(fd, b"q")
    time.sleep(0.2)
    try:
        os.close(fd)
    except OSError:
        pass
    os.waitpid(pid, 0)


if __name__ == "__main__":
    main()
