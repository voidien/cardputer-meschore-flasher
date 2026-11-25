"""make device-shot: a picture of the real LCD while you use MeshFlash on it, with its text read back.

One ssh, read-only: it reads /dev/fb0 (void may, being in the video group), fb0's size from /sys and the device's
Terminus font, and writes nothing on the device. The picture goes to dist/phase3/shot-<time>.png (2x) with .raw and
.txt beside it; the font's copy to dist/phase3/font/. An asleep screen, or meshflash's lock gone dark, reads all
black: press a key on the device first. The launcher's home screen isn't a text grid, so its text reads as "¤".

    python3 scripts/device/shot.py [--host CardPuter]
"""

import argparse
import base64
import gzip
import os
import sys
import zlib

sys.dont_write_bytecode = True  # no __pycache__ in the repository
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import devicelib as dl  # noqa: E402
import fb2png  # noqa: E402
import ocr  # noqa: E402

DEVICE = dl.DEVICE_HELPERS + r'''
import base64, os, sys, zlib
fb, sysdir, font = sys.argv[1:4]
r = {}
def text(p):
    try:
        with open(p) as f:
            return f.read().strip()
    except OSError:
        return None
r["virtual_size"], r["bits_per_pixel"], r["stride"] = (text(os.path.join(sysdir, k))
                                                       for k in ("virtual_size", "bits_per_pixel", "stride"))
try:
    w, h = (int(x) for x in (r["virtual_size"] or "320,170").split(","))
    stride = int(r["stride"] or w * 2)
    with open(fb, "rb") as f:
        r["fb"] = base64.b64encode(zlib.compress(f.read(stride * h), 9)).decode()
except (OSError, ValueError) as e:
    r["fb_error"] = str(e)
try:
    with open(font, "rb") as f:
        r["font"] = f.read(1 << 20).hex()
except OSError as e:
    r["font_error"] = str(e)
emit(r)
'''


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    dl.host_arg(ap)
    a = ap.parse_args()
    name = "shot-" + dl.stamp()
    base = os.path.join(dl.out_dir(), name)
    rep = dl.Report(base + ".txt")
    try:
        r = dl.run_device_python(a.host, DEVICE, ["/dev/fb0", "/sys/class/graphics/fb0", dl.DEVICE_FONT], 60)
    except dl.Failed as e:
        dl.die("FAILED: %s" % e)
    if "fb_error" in r:
        dl.die("FAILED: can't read the LCD's picture: %s" % r["fb_error"])
    w, h = (int(x) for x in (r["virtual_size"] or "320,170").split(","))
    stride = int(r["stride"] or w * 2)
    raw = zlib.decompress(base64.b64decode(r["fb"]))
    with open(base + ".raw", "wb") as f:
        f.write(raw)
    if r["bits_per_pixel"] not in (None, "16"):
        dl.die("fb0 has %s bits a pixel, not 16: saved %s.raw only" % (r["bits_per_pixel"], base))
    fb2png.write_png(raw, base + ".png", 2, w, h, stride)
    rep.say("%s.png: %dx%d, stride %d" % (os.path.relpath(base, os.getcwd()), w, h, stride))
    if ocr.is_black(raw):
        rep.say("All black: the screen is asleep, or meshflash's lock has gone dark. Press a key on the device "
                "(it wakes without unlocking) and run make device-shot again.")
        rep.save()
        return
    if "font" not in r:
        rep.say("(no text: can't read the device's font: %s)" % r.get("font_error"))
        rep.save()
        return
    data = bytes.fromhex(r["font"])
    fdir = dl.out_dir("font")
    fpath = os.path.join(fdir, os.path.basename(dl.DEVICE_FONT))
    if os.path.exists(fpath):
        os.chmod(fpath, 0o644)
    with open(fpath, "wb") as f:
        f.write(data)
    os.chmod(fpath, 0o444)
    font = ocr.parse_font(gzip.decompress(data) if data[:2] == b"\x1f\x8b" else data)
    lines = ocr.read_lines(raw, font, w, h, stride)
    unknown = sum(l.count(ocr.UNKNOWN) for l in lines)
    if unknown > 100:
        rep.say("This isn't meshflash's text grid (%d cells match no character): the home screen, or another "
                "app. The picture is in the PNG." % unknown)
    else:
        rep.say("Its text:")
        for l in lines:
            rep.say("  |" + l)
    rep.save()


if __name__ == "__main__":
    main()
