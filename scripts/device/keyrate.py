"""How the CLI app sends a held key, against meshflash's half-second rules. Run it IN THE CLI APP on the device:

    python3 ~/meshflash-check/keyrate.py        (make device-tools copies it there)

In a terminal a held key arrives again and again and its release never does, so meshflash (internal/ui) takes a
key less than 0.5 s after the same key as that key held down: lockTabGap for Tab (a held Tab locks once its run has
lasted 3 s), escRepeatGap for Esc (a held Esc counts once) and heldYGap for y (a held y answers nothing more). Those
rules assume the terminal repeats a held key every 33 ms after 250 ms, Linux's default, which nobody has measured in
the CLI app yet (docs/phase2-design.md 18.9). This asks you to hold Tab, then Esc, then y, notes when each byte comes,
and says what meshflash makes of it.

Standard library only. It reads the keyboard in raw mode, changes nothing else, and puts the terminal back as it
was however it ends (Ctrl+C stops it). It writes one file, ~/meshflash-check/keyrate.txt, for make device-logs.
"""

import os
import select
import signal
import sys
import termios
import time
import tty

GAP = 0.5   # internal/ui: lockTabGap, escRepeatGap, heldYGap
LOCK = 3.0  # internal/ui: lockHold
KEYS = [
    ("Tab", b"\t", "Hold Tab about 4 s, then let go."),
    ("Esc", b"\x1b", "Hold Esc about 2 s, then let go.",),
    ("y", b"y", "Hold y about 2 s, then let go."),
]
OUT = os.path.expanduser("~/meshflash-check/keyrate.txt")


def say(s=""):
    sys.stdout.write(s + "\r\n")  # raw mode: no newline translation
    sys.stdout.flush()


def collect(fd, want):
    """Waits up to 60 s for the key, then takes every byte until 1.5 s pass with none: (times of the key, others)."""
    times, other = [], 0
    deadline = time.monotonic() + 60
    while True:
        wait = 1.5 if times else deadline - time.monotonic()
        if wait <= 0 or not select.select([fd], [], [], wait)[0]:
            return times, other
        now = time.monotonic()
        for b in os.read(fd, 256):
            if b == 3:
                raise KeyboardInterrupt
            if bytes([b]) == want:
                times.append(now)
            elif times:
                other += 1


def acts(times):
    """How many of the bytes meshflash takes as presses: the first, and each that comes GAP or more after the one
    before. For Tab, also when a held Tab would lock, from the first byte (None: it wouldn't)."""
    presses, first, lock = 0, None, None
    for i, t in enumerate(times):
        if i == 0 or t - times[i - 1] >= GAP:
            presses, first = presses + 1, t
        elif lock is None and t - first >= LOCK:
            lock = t - times[0]
    return presses, lock


def report(name, times, other):
    lines = []
    if not times:
        return ["%s: nothing came (60 s)" % name]
    gaps = [b - a for a, b in zip(times, times[1:])]
    lines.append("%s: %d byte%s over %.2f s" % (name, len(times), "" if len(times) == 1 else "s",
                                                times[-1] - times[0]))
    if gaps:
        rest = sorted(gaps[1:])
        lines.append("  first repeat %.0f ms after the press" % (gaps[0] * 1000))
        if rest:
            lines.append("  then every %.0f ms (%.0f-%.0f)" % (rest[len(rest) // 2] * 1000, rest[0] * 1000,
                                                              rest[-1] * 1000))
        big = [g for g in gaps if g >= GAP]
        lines.append("  gaps of 0.5 s or more: %s" % (len(big) if big else "none"))
    else:
        lines.append("  no repeats: one byte")
    if other:
        lines.append("  and %d other bytes" % other)
    presses, lock = acts(times)
    if name == "Tab":
        lines.append("  meshflash: %d Tab press%s, %s" % (presses, "" if presses == 1 else "es",
                                                         "locks at %.1f s" % lock if lock else "NO lock"))
    else:
        lines.append("  meshflash: acts as %d %s%s" % (presses, name, " (right)" if presses == 1 else ""))
    return lines


def main():
    fd = sys.stdin.fileno()
    if not os.isatty(fd):
        sys.exit("keyrate.py reads the keyboard: run it in the CLI app, not over ssh or a pipe.")
    old = termios.tcgetattr(fd)
    signal.signal(signal.SIGTERM, lambda *_: sys.exit(1))  # the launcher's Esc hold: put the terminal back
    signal.signal(signal.SIGHUP, lambda *_: sys.exit(1))
    out, raw = [], []
    try:
        tty.setraw(fd)
        say("keyrate: held keys, vs meshflash's 0.5 s")
        say("(Esc under 3 s: at 3 s the launcher ends it)")
        for name, want, prompt in KEYS:
            say()
            say(prompt)
            times, other = collect(fd, want)
            raw.append("%s: %s" % (name, " ".join("%.3f" % (t - times[0]) for t in times)))
            lines = report(name, times, other)
            out += lines
            for l in lines:
                say(l)
    except KeyboardInterrupt:
        say("stopped (Ctrl+C)")
    finally:
        termios.tcsetattr(fd, termios.TCSADRAIN, old)
        try:
            os.makedirs(os.path.dirname(OUT), exist_ok=True)
            with open(OUT, "w") as f:
                f.write("keyrate.py %s, TERM=%s\n" % (time.strftime("%Y-%m-%d %H:%M:%S"), os.environ.get("TERM")))
                f.write("\n".join(out + ["", "seconds after the first byte:"] + raw) + "\n")
            print("Saved in ~/meshflash-check/keyrate.txt (make device-logs copies it).")
        except OSError as e:
            print("couldn't save %s: %s" % (OUT, e))


if __name__ == "__main__":
    main()
