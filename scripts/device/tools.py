"""make device-tools: copies keyrate.py to ~/meshflash-check/ on the device, for the CLI app (docs/phase3.md, 8).

One ssh, with the other scripts' options (devicelib.SSH_OPTS: BatchMode, an 8 s connect timeout, keepalives) and a
time limit, so a device asleep or off the Wi-Fi fails in seconds, in plain words. What runs there writes that one
file and nothing else: ~/meshflash-check/keyrate.py, as keyrate.py.new first and then renamed over any older copy,
and says its sha256 back, which this side checks against scripts/device/keyrate.py. keyrate.py itself runs in the
CLI app on the device, not over ssh: it measures how the CLI app sends a held key.

    python3 scripts/device/tools.py [--host CardPuter]
"""

import argparse
import hashlib
import os
import sys

sys.dont_write_bytecode = True  # no __pycache__ in the repository
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import devicelib as dl  # noqa: E402

FOLDER = "~/meshflash-check"
NAME = "keyrate.py"

DEVICE = r'''
import hashlib, os
folder = os.path.expanduser(FOLDER)
os.makedirs(folder, exist_ok=True)
path = os.path.join(folder, NAME)
with open(path + ".new", "wb") as f:
    f.write(DATA)
os.chmod(path + ".new", 0o644)
os.replace(path + ".new", path)
with open(path, "rb") as f:
    emit({"path": path, "sha256": hashlib.sha256(f.read()).hexdigest()})
'''


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    dl.host_arg(ap)
    a = ap.parse_args()
    with open(os.path.join(dl.HERE, NAME), "rb") as f:
        data = f.read()
    want = hashlib.sha256(data).hexdigest()
    rep = dl.Report(os.path.join(dl.out_dir(), "tools.log"))
    source = "FOLDER = %r\nNAME = %r\nDATA = %r\n" % (FOLDER, NAME, data) + dl.DEVICE_HELPERS + DEVICE
    try:
        r = dl.run_device_python(a.host, source, timeout=30)
    except dl.Failed as e:
        rep.say("FAILED: %s" % e)
        rep.save()
        sys.exit(1)
    if r["sha256"] != want:
        rep.say("FAILED: %s on %s has sha256 %s, not %s (scripts/device/%s): run make device-tools again" % (
            r["path"], a.host, r["sha256"][:16], want[:16], NAME))
        rep.save()
        sys.exit(1)
    rep.say("copied %s to %s on %s (%d bytes, sha256 %s, the same as scripts/device/%s)" % (
        NAME, r["path"], a.host, len(data), want[:16], NAME))
    rep.say("In the CLI app on the device, type:  python3 %s/%s" % (FOLDER, NAME))
    rep.save()


if __name__ == "__main__":
    main()
