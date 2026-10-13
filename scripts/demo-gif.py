#!/usr/bin/env python3
"""Records the README animation: rtpengine-tui against the emulator of
make demo-rtpengine, driven by the keys below, saved as an asciinema cast.

    make demo-gif VERSION=v0.1.0   # this script, then agg: cast -> docs/demo.gif

Usage: demo-gif.py <out.cast>. Needs make build first; standard library only.
The demo configs go to /tmp/voip-tui-demo, so no local path shows up.
"""

import codecs
import fcntl
import json
import os
import pty
import struct
import subprocess
import sys
import termios
import threading
import time

COLS, ROWS = 140, 38
TYPE_DELAY = 0.06  # between typed characters
ENTER, ESC, TAB, CTRL_D, SPACE = "\r", "\x1b", "\t", "\x04", " "

# (keys, pause after): typed text appears character by character
SCRIPT = [
    # the hosts with their session counts; space makes the newest one current
    ("", 2.5), ("G", 0.6), (SPACE, 1.5),
    # its config, checked against the release; one option in detail
    ("c", 2.5), ("/silent-timeout", 0.4), (ENTER, 0.8), (ENTER, 3.0), (ESC, 0.8),
    # runtime limits: set the RTP timeout
    (":limits", 0.3), (ENTER, 2.0), ("/^timeout", 0.4), (ENTER, 0.8), (ENTER, 0.6),
    ("120", 0.3), (ENTER, 1.2), (TAB, 0.5), (ENTER, 2.5),
    # the host card, then raise the log level of one subsystem
    (":info", 0.3), (ENTER, 3.0), ("e", 1.5), ("jjjjjj", 0.6), ("+", 2.5),
    # calls, one call in detail, its log
    (":sessions", 0.3), (ENTER, 2.5), (ENTER, 3.5), ("L", 2.0), ("w", 2.5), (ESC, 0.6),
    # statistics
    (":stats", 0.3), (ENTER, 3.0), (CTRL_D, 3.0),
]


def main(out):
    etc = "/tmp/voip-tui-demo"
    subprocess.run(["rm", "-rf", etc], check=True)
    # UTC, so the recording shows no local time zone
    mock = subprocess.Popen(["./bin/rtpengine-tui-mock", "-fleet", "-seed", "7", "-confdir", etc],
                            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, env=dict(os.environ, TZ="UTC"))
    try:
        conf = os.path.join(etc, "rtpengine-tui.yaml")
        for _ in range(50):
            if os.path.exists(conf):
                break
            time.sleep(0.1)
        record(["./bin/rtpengine-tui", "-config", conf], out)
    finally:
        mock.terminate()
        mock.wait()


def record(argv, out):
    pid, fd = pty.fork()
    if pid == 0:
        os.environ.update(TERM="xterm-256color", COLORTERM="truecolor", LANG="en_US.UTF-8", TZ="UTC")
        os.execvp(argv[0], argv)
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", ROWS, COLS, 0, 0))
    events, start = [], time.monotonic()

    def read():
        dec = codecs.getincrementaldecoder("utf-8")("replace")
        while True:
            try:
                data = os.read(fd, 65536)
            except OSError:
                return
            if not data:
                return
            events.append([round(time.monotonic() - start, 4), "o", dec.decode(data)])

    reader = threading.Thread(target=read, daemon=True)
    reader.start()
    for keys, pause in SCRIPT:
        for ch in keys:
            os.write(fd, ch.encode())
            if len(keys) > 1:
                time.sleep(TYPE_DELAY)
        time.sleep(pause)
    end = round(time.monotonic() - start, 4)
    os.write(fd, b":q\r")
    os.waitpid(pid, 0)
    reader.join(timeout=2)
    with open(out, "w") as f:
        f.write(json.dumps({"version": 2, "width": COLS, "height": ROWS,
                            "env": {"TERM": "xterm-256color"}}) + "\n")
        for e in events:
            if e[0] <= end:  # leave out the quit
                f.write(json.dumps(e) + "\n")


if __name__ == "__main__":
    main(sys.argv[1])
