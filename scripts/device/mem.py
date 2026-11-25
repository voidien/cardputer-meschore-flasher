"""make device-mem: the memory of every meshflash running on the device, for S9 (VmHWM under 40 MB).

Run it just before quitting meshflash after a real flash and setup, while it is still open (on the LCD or in the CLI
app): VmHWM is the most it has held at once in this run, so it is read before it ends. One ssh, read-only: pidof
meshflash and /proc/<pid>/status. The summary is printed and saved as dist/phase3/mem-<time>.txt.

    python3 scripts/device/mem.py [--host CardPuter]
"""

import argparse
import os
import sys

sys.dont_write_bytecode = True  # no __pycache__ in the repository
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import devicelib as dl  # noqa: E402

DEVICE = dl.DEVICE_HELPERS + r'''
import os, subprocess, sys
name = sys.argv[1]
try:
    pids = subprocess.run(["pidof", name], stdout=subprocess.PIPE).stdout.decode().split()
except FileNotFoundError:
    pids = [p for p in os.listdir("/proc") if p.isdigit() and open("/proc/%s/comm" % p).read().strip() == name]
with open("/proc/uptime") as f:
    uptime = float(f.read().split()[0])
hz = os.sysconf("SC_CLK_TCK")
out = []
for pid in pids:
    p = {"pid": int(pid)}
    try:
        with open("/proc/%s/status" % pid) as f:
            for line in f:
                k, _, v = line.partition(":")
                if k in ("VmHWM", "VmRSS", "VmPeak", "VmSwap", "Threads"):
                    p[k] = v.strip()
        with open("/proc/%s/cmdline" % pid, "rb") as f:
            p["cmdline"] = f.read().replace(b"\0", b" ").decode("utf-8", "replace").strip()
        with open("/proc/%s/stat" % pid) as f:
            start = int(f.read().rsplit(")", 1)[1].split()[19])
        p["running_secs"] = round(uptime - start / hz)
    except (OSError, ValueError, IndexError) as e:
        p["error"] = str(e)
    out.append(p)
emit({"name": name, "procs": out})
'''


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    dl.host_arg(ap)
    ap.add_argument("--name", default="meshflash", help="the program's name (default meshflash)")
    a = ap.parse_args()
    rep = dl.Report(os.path.join(dl.OUT, "mem-%s.txt" % dl.stamp()))
    try:
        r = dl.run_device_python(a.host, DEVICE, [a.name], timeout=60)
    except dl.Failed as e:
        rep.say("FAILED: %s" % e)
        rep.save()
        sys.exit(1)
    if not r["procs"]:
        rep.say("No %s is running on %s. Run this while it is still open, just before you quit it." %
                (a.name, a.host))
    for p in r["procs"]:
        if "error" in p:
            rep.say("%s pid %d: %s (it may have just quit)" % (a.name, p["pid"], p["error"]))
            continue
        where = "on the LCD (--screen)" if "--screen" in p["cmdline"].split() else "in a terminal"
        rep.say("%s pid %d, %s, running %d min %02d s:" % (a.name, p["pid"], where, p["running_secs"] // 60,
                                                         p["running_secs"] % 60))
        rep.say("  VmHWM %s: %s" % (p.get("VmHWM"), dl.s9(p.get("VmHWM"))))
        rep.say("  VmRSS %s, VmSwap %s, threads %s" % (p.get("VmRSS"), p.get("VmSwap", "?"), p.get("Threads")))
        rep.log("  VmPeak %s (address space, not memory used); %s" % (p.get("VmPeak"), p["cmdline"]))
    rep.save()
    print("(saved as %s)" % os.path.relpath(rep.log_path, os.getcwd()))


if __name__ == "__main__":
    main()
