"""make device-logs: meshflash's own files on the device, printed and copied to dist/phase3/logs-<time>/.

One ssh, read-only: ~/.cache/meshflash/screen.log (started afresh each run: what failed, and after a quit, an Esc
hold or a crash, what a board was left with), ~/.config/meshflash/config.json and drafts.json (the boards and radio
remembered, and what was typed on the Setup form and not sent), and keyrate.py's ~/meshflash-check/keyrate.txt if
there is one. None of them ever holds a password: meshflash sends the admin and guest passwords to the node and keeps
them nowhere (SPEC.md 8).

    python3 scripts/device/logs.py [--host CardPuter]
"""

import argparse
import os
import stat
import sys
import time

sys.dont_write_bytecode = True  # no __pycache__ in the repository
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import devicelib as dl  # noqa: E402

FILES = ["~/.cache/meshflash/screen.log", "~/.config/meshflash/config.json", "~/.config/meshflash/drafts.json",
         "~/meshflash-check/keyrate.txt"]

DEVICE = dl.DEVICE_HELPERS + r'''
import os, sys
out = []
for name in sys.argv[1:]:
    path = os.path.expanduser(name)
    f = {"name": name}
    try:
        st = os.stat(path)
        f.update(size=st.st_size, mode=st.st_mode, mtime=st.st_mtime)
        with open(path, "rb") as fh:
            f["text"] = fh.read(512 * 1024).decode("utf-8", "replace")
    except OSError as e:
        f["error"] = e.strerror
    out.append(f)
emit(out)
'''


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    dl.host_arg(ap)
    a = ap.parse_args()
    folder = dl.out_dir("logs-" + dl.stamp())
    rep = dl.Report(os.path.join(folder, "logs.txt"))
    try:
        files = dl.run_device_python(a.host, DEVICE, FILES, timeout=60)
    except dl.Failed as e:
        rep.say("FAILED: %s" % e)
        rep.save()
        sys.exit(1)
    for f in files:
        if "error" in f:
            rep.say("== %s: none (%s)" % (f["name"], f["error"]))
            continue
        rep.say("== %s (%d bytes, %s, %s)" % (f["name"], f["size"], stat.filemode(f["mode"]),
                                              time.strftime("%Y-%m-%d %H:%M", time.localtime(f["mtime"]))))
        with open(os.path.join(folder, os.path.basename(f["name"])), "w") as out:
            out.write(f["text"])
        for line in f["text"].rstrip("\n").splitlines():
            rep.say(line)
    rep.save()
    print("(copies in %s)" % os.path.relpath(folder, os.getcwd()))


if __name__ == "__main__":
    main()
