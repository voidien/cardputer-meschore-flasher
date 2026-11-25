"""make device-check: the facts Phase 3 starts from, read-only, on the T14 and on the device. Paste the summary.

On the T14: Python, Go (and whether GOTOOLCHAIN=go1.25.4, which the Makefile sets, will work), the git branch and
commit, and dist/meshflash (its size, sha256 and CPU). Then one ssh to the device (BatchMode: a key login, never a
password), which only reads: the system, the board's device-tree model (meshflash tells the CardputerZero by it), the
groups (dialout), serial ports, battery, backlight (whether void may write it: tested with access(), never written),
the framebuffer's size, the keyboards, the Terminus font, the home screen's folders, memory and disk, the deployed
meshflash, ~/.bashrc's PATH line, a running meshflash, and whether the device reaches the three sites meshflash
reads (a HEAD request each, as curl -I: nothing is changed there). No environment is printed, nothing secret.

The summary is printed; everything is written to dist/phase3/check.log.

    python3 scripts/device/check.py [--host CardPuter]
"""

import argparse
import os
import re
import shutil
import subprocess
import sys

sys.dont_write_bytecode = True  # no __pycache__ in the repository
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import devicelib as dl  # noqa: E402

GO_WANTED = "go1.25.4"

DEVICE = dl.DEVICE_HELPERS + r'''
import glob, grp, hashlib, os, pwd, stat, subprocess, sys

FONT, KEYBOARD = sys.argv[1], sys.argv[2]
SITES = sys.argv[3:]
r = {}

def run(cmd, timeout=15):
    try:
        p = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=timeout)
        return p.returncode, p.stdout.decode("utf-8", "replace").strip()
    except FileNotFoundError:
        return 127, "%s: not installed" % cmd[0]
    except subprocess.TimeoutExpired:
        return 124, "%s: no answer in %d s" % (cmd[0], timeout)

def read(path, limit=4096):
    try:
        with open(path, "rb") as f:
            return f.read(limit)
    except OSError as e:
        return None

def text(path):
    b = read(path)
    return None if b is None else b.decode("utf-8", "replace").strip()

def perms(path):
    try:
        st = os.stat(path)
    except OSError as e:
        return {"path": path, "error": e.strerror}
    def name(f, i):
        try:
            return f(i)[0]
        except KeyError:
            return str(i)
    return {"path": path, "mode": stat.filemode(st.st_mode), "owner": name(pwd.getpwuid, st.st_uid),
            "group": name(grp.getgrgid, st.st_gid), "readable": os.access(path, os.R_OK),
            "writable": os.access(path, os.W_OK)}

def sha256(path):
    try:
        h = hashlib.sha256()
        with open(path, "rb") as f:
            for b in iter(lambda: f.read(1 << 20), b""):
                h.update(b)
        return h.hexdigest()
    except OSError:
        return None

r["uname"] = run(["uname", "-a"])[1]
osr = text("/etc/os-release") or ""
r["pretty_name"] = next((l.split("=", 1)[1].strip('"') for l in osr.splitlines() if l.startswith("PRETTY_NAME=")), None)
model = read("/proc/device-tree/model")
r["model_bytes"] = None if model is None else model.hex()
r["model_text"] = None if model is None else model.decode("utf-8", "replace")

r["id"] = run(["id"])[1]
groups = []
for g in os.getgroups():
    try:
        groups.append(grp.getgrgid(g).gr_name)
    except KeyError:
        groups.append(str(g))
r["groups"] = groups
me = pwd.getpwuid(os.getuid()).pw_name
try:
    r["dialout_members"] = grp.getgrnam("dialout").gr_mem
except KeyError:
    r["dialout_members"] = None
r["user"] = me
# The launcher's own groups are what an app it starts gets (the CLI app and the home-screen entry): after usermod
# they change only once the launcher restarts (a reboot), whatever a fresh ssh login shows.
launcher = []
for pid in os.listdir("/proc"):
    if not pid.isdigit():
        continue
    comm = text("/proc/%s/comm" % pid) or ""
    if comm.startswith("M5CardputerZer") or "APPLaunch" in comm:
        st = text("/proc/%s/status" % pid) or ""
        gl = next((l.split(":", 1)[1].split() for l in st.splitlines() if l.startswith("Groups:")), [])
        names = []
        for g in gl:
            try:
                names.append(grp.getgrgid(int(g)).gr_name)
            except (KeyError, ValueError):
                names.append(g)
        launcher.append({"pid": int(pid), "comm": comm, "groups": names})
r["launcher"] = launcher

r["ports"] = [perms(p) for p in sorted(glob.glob("/dev/ttyACM*") + glob.glob("/dev/ttyUSB*"))]
byid = []
for p in sorted(glob.glob("/dev/serial/by-id/*")):
    byid.append({"name": os.path.basename(p), "target": os.path.realpath(p)})
r["by_id"] = byid

ps = []
for d in sorted(glob.glob("/sys/class/power_supply/*")):
    ps.append({"name": os.path.basename(d), "type": text(d + "/type"), "capacity": text(d + "/capacity"),
               "status": text(d + "/status")})
r["power"] = ps

bl = []
for d in sorted(glob.glob("/sys/class/backlight/*")):
    bl.append({"name": os.path.basename(d), "brightness": text(d + "/brightness"),
               "max_brightness": text(d + "/max_brightness"), "bl_power": text(d + "/bl_power"),
               "brightness_perms": perms(d + "/brightness"), "max_perms": perms(d + "/max_brightness"),
               "real": os.path.realpath(d)})
r["backlight"] = bl

fb = "/sys/class/graphics/fb0"
r["fb0"] = {k: text(fb + "/" + k) for k in ("name", "virtual_size", "bits_per_pixel", "stride")}
r["fb0_dev"] = perms("/dev/fb0")

devs, cur = [], {}
for line in (text("/proc/bus/input/devices") or "").splitlines() + [""]:
    if not line.strip():
        if cur:
            devs.append(cur)
        cur = {}
    elif line.startswith("N: Name="):
        cur["name"] = line[8:].strip('"')
    elif line.startswith("H: Handlers="):
        cur["handlers"] = line[12:].strip()
    elif line.startswith("P: Phys="):
        cur["phys"] = line[8:]
r["input"] = devs
r["keyboard"] = perms(KEYBOARD)
r["keyboard_target"] = os.path.realpath(KEYBOARD)

r["font"] = perms(FONT)
r["font_sha256"] = sha256(FONT)
r["font_size"] = os.path.getsize(FONT) if os.path.exists(FONT) else None

al = "/usr/share/APPLaunch"
r["applaunch"] = {d: os.path.isdir(os.path.join(al, d)) for d in ("", "applications", "share/images", "bin")}
r["entry"] = text(al + "/applications/meshflash.desktop")
r["icon"] = os.path.exists(al + "/share/images/meshflash_100.png")

r["free"] = run(["free", "-m"])[1]
home = os.path.expanduser("~")
r["df_home"] = run(["df", "-h", home])[1]
r["df_tmp"] = run(["df", "-h", "/tmp"])[1]

binary = os.path.join(home, ".local/bin/meshflash")
if os.path.exists(binary):
    r["deployed"] = {"version": run([binary, "--version"])[1], "sha256": sha256(binary),
                     "size": os.path.getsize(binary), "perms": perms(binary)}
else:
    r["deployed"] = None
bashrc = text(os.path.join(home, ".bashrc")) or ""
r["bashrc_path"] = "HOME/.local/bin" in bashrc
code, out = run(["pidof", "meshflash"])
r["running"] = []
for pid in out.split() if code == 0 else []:
    cmd = (read("/proc/%s/cmdline" % pid) or b"").replace(b"\0", b" ").decode("utf-8", "replace").strip()
    r["running"].append({"pid": int(pid), "cmdline": cmd})
code, out = run(["timedatectl", "show", "--property=NTPSynchronized", "--value"])
r["time_sync"] = out if code == 0 else "can't tell (timedatectl: %s)" % (out.splitlines() or [""])[0]

net = []
for url in SITES:
    code, out = run(["curl", "-sS", "-o", "/dev/null", "-w", "%{http_code}", "-I", "--max-time", "8", url], 15)
    net.append({"url": url, "exit": code, "out": out})
r["net"] = net
emit(r)
'''

SITES = ["https://flasher.meshcore.io/", "https://zephcore.meshcore.dev/", "https://github.com/"]


def run(cmd, cwd=None, env=None):
    try:
        p = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, cwd=cwd, env=env, timeout=60)
        return p.returncode, p.stdout.decode("utf-8", "replace").strip()
    except FileNotFoundError:
        return 127, ""
    except subprocess.TimeoutExpired:
        return 124, ""


def go_version_tuple(v):
    m = re.match(r"go(\d+)\.(\d+)(?:\.(\d+))?", v or "")
    return tuple(int(x or 0) for x in m.groups()) if m else None


def local_facts(rep):
    rep.say("== T14 ==")
    rep.say("python %s (%s)" % (sys.version.split()[0], sys.executable))
    # Ask the go that is installed (GOTOOLCHAIN=local), outside any module, whose go.mod could ask for another.
    env = dict(os.environ, GOTOOLCHAIN="local")
    code, out = run(["go", "env", "GOVERSION", "GOMODCACHE", "GOPROXY", "GOOS", "GOARCH"], cwd="/", env=env)
    if code == 127:
        rep.say("go: not installed: install Go 1.21 or newer (the Makefile then fetches %s)" % GO_WANTED)
    elif code != 0:
        rep.say("go: `go env` failed: %s" % out)
    else:
        ver, modcache, proxy, goos, goarch = (out.splitlines() + [""] * 5)[:5]
        have = go_version_tuple(ver)
        cached = os.path.isdir(os.path.join(modcache, "golang.org", "toolchain@v0.0.1-%s.%s-%s" %
                                            (GO_WANTED, goos, goarch)))
        on_path = shutil.which(GO_WANTED) is not None
        if have is None:
            verdict = "can't read its version"
        elif have < (1, 21, 0):
            verdict = "too old for GOTOOLCHAIN: install Go 1.21 or newer"
        elif ver == GO_WANTED:
            verdict = "is %s: nothing to fetch" % GO_WANTED
        elif cached or on_path:
            verdict = "switches to %s, already here (%s)" % (GO_WANTED, "on PATH" if on_path else "module cache")
        elif proxy.strip() in ("off", ""):
            verdict = "would need %s but GOPROXY=%s: it can't fetch it" % (GO_WANTED, proxy or '""')
        else:
            verdict = "fetches %s once (about 70 MB, from %s) on the first make" % (GO_WANTED, proxy.split(",")[0])
        rep.say("go %s: GOTOOLCHAIN=%s %s" % (ver, GO_WANTED, verdict))
        rep.log("  GOMODCACHE=%s GOPROXY=%s" % (modcache, proxy))
    code, branch = run(["git", "rev-parse", "--abbrev-ref", "HEAD"], cwd=dl.REPO)
    _, head = run(["git", "log", "-1", "--format=%h %s"], cwd=dl.REPO)
    _, dirty = run(["git", "--no-optional-locks", "status", "--porcelain"], cwd=dl.REPO)
    _, track = run(["git", "--no-optional-locks", "status", "-sb"], cwd=dl.REPO)
    if code != 0:
        rep.say("git: not a git checkout here (%s)" % dl.REPO)
    else:
        changed = len([l for l in dirty.splitlines() if l.strip()])
        rep.say("git %s @ %s (%s)" % (branch, head, "clean" if not changed else
                                      "%d changed file%s" % (changed, "" if changed == 1 else "s")))
        rep.log("  " + (track.splitlines() or [""])[0])
    binary = os.path.join(dl.REPO, "dist", "meshflash")
    if os.path.exists(binary):
        with open(binary, "rb") as f:
            head = f.read(20)
        arch = "arm64" if head[:4] == b"\x7fELF" and head[18:20] == b"\xb7\x00" else "NOT arm64"
        rep.say("dist/meshflash %s, %s, sha256 %s" % (dl.mb(os.path.getsize(binary)), arch, dl.sha256_file(binary)))
    else:
        rep.say("dist/meshflash: not built yet (make build-arm64; make deploy builds it too)")


def device_facts(rep, host):
    rep.say("== ssh %s ==" % host)
    try:
        r = dl.run_device_python(host, DEVICE, [dl.DEVICE_FONT, "/dev/input/by-path/platform-3f804000.i2c-event"] +
                                 SITES, timeout=120)
    except dl.Failed as e:
        rep.say("FAILED: %s" % e)
        return
    rep.say("ok, as %s" % r["user"])
    rep.say("== device ==")
    rep.say("system: %s" % (r["pretty_name"] or "?"))
    rep.say("uname: %s" % r["uname"])
    if r["model_bytes"] is None:
        rep.say("device-tree model: none (/proc/device-tree/model can't be read)")
    else:
        t = r["model_text"]
        zero = "Raspberry Pi Compute Module 0" in t.rstrip("\0\n")
        rep.say("device-tree model: %r (%d bytes: %s)" % (t, len(r["model_bytes"]) // 2, r["model_bytes"]))
        rep.say("  meshflash reads this as the CardputerZero: %s" % ("yes" if zero else "NO"))
    groups = r["groups"]
    rep.say("groups (this ssh login): %s" % " ".join(groups))
    rep.log("id: %s" % r["id"])
    members = r["dialout_members"]
    in_file = members is not None and r["user"] in members
    rep.say("dialout: this login %s; /etc/group %s" % ("yes" if "dialout" in groups else "no",
                                                        "lists %s" % r["user"] if in_file else "doesn't list it"))
    for l in r["launcher"]:
        rep.say("  launcher %s (pid %d) has dialout: %s (apps it starts get its groups)" %
                (l["comm"], l["pid"], "yes" if "dialout" in l["groups"] else "no: reboot after usermod"))
        rep.log("  launcher groups: %s" % " ".join(l["groups"]))
    if not r["launcher"]:
        rep.say("  launcher: not found running")
    if r["ports"]:
        for p in r["ports"]:
            rep.say("serial port %s %s %s:%s" % (p["path"], p.get("mode", p.get("error")), p.get("owner", ""),
                                                 p.get("group", "")))
    else:
        rep.say("serial ports: none (no /dev/ttyACM* or /dev/ttyUSB*)")
    for b in r["by_id"]:
        rep.say("  by-id %s -> %s" % (b["name"], b["target"]))
    for p in r["power"]:
        rep.say("power supply %s: type %s, capacity %s, status %s" % (p["name"], p["type"], p["capacity"],
                                                                       p["status"]))
    if not r["power"]:
        rep.say("power supplies: none listed")
    for b in r["backlight"]:
        bp = b["brightness_perms"]
        rep.say("backlight %s: brightness %s of %s, bl_power %s; brightness %s %s:%s, void may write it: %s" %
                (b["name"], b["brightness"], b["max_brightness"], b["bl_power"], bp.get("mode", bp.get("error")),
                 bp.get("owner", ""), bp.get("group", ""), "yes" if bp.get("writable") else "no"))
        mp = b["max_perms"]
        rep.log("  %s; max_brightness %s %s:%s, writable %s" % (b["real"], mp.get("mode", mp.get("error")),
                                                              mp.get("owner", ""), mp.get("group", ""),
                                                              mp.get("writable")))
    if not r["backlight"]:
        rep.say("backlight: none in /sys/class/backlight (the lock draws the screen black instead)")
    fb = r["fb0"]
    fd = r["fb0_dev"]
    if fb["virtual_size"] is None:
        rep.say("fb0: nothing in /sys/class/graphics/fb0")
    else:
        rep.say("fb0: %s, virtual_size %s, bits_per_pixel %s, stride %s" % (
            fb["name"], fb["virtual_size"], fb["bits_per_pixel"], fb["stride"]))
    if "error" in fd:
        rep.say("/dev/fb0: %s" % fd["error"])
    else:
        rep.say("/dev/fb0: %s %s:%s, void may read it: %s (make device-shot reads it)" % (
            fd["mode"], fd["owner"], fd["group"], "yes" if fd["readable"] else "no"))
    kb = r["keyboard"]
    rep.say("keyboard %s -> %s: %s" % (kb["path"], r["keyboard_target"],
                                       "readable" if kb.get("readable") else kb.get("error", "NOT readable")))
    for d in r["input"]:
        rep.say("  input %r: %s" % (d.get("name"), d.get("handlers")))
        rep.log("    phys %s" % d.get("phys"))
    f = r["font"]
    if "error" in f:
        rep.say("Terminus font %s: missing (%s)" % (f["path"], f["error"]))
    else:
        rep.say("Terminus font: present, %d bytes, sha256 %s" % (r["font_size"], r["font_sha256"]))
    al = r["applaunch"]
    rep.say("/usr/share/APPLaunch: %s; meshflash entry %s, icon %s" % (
        ", ".join("%s %s" % (k or ".", "yes" if v else "no") for k, v in al.items()),
        "installed" if r["entry"] else "not installed", "installed" if r["icon"] else "not installed"))
    if r["entry"]:
        for line in r["entry"].splitlines():
            rep.log("  entry: %s" % line)
    rep.say("memory (free -m):")
    for line in r["free"].splitlines():
        rep.say("  " + line)
    rep.say("disk: " + " | ".join(r["df_home"].splitlines()[1:] + r["df_tmp"].splitlines()[1:]))
    d = r["deployed"]
    if d:
        local = os.path.join(dl.REPO, "dist", "meshflash")
        same = os.path.exists(local) and dl.sha256_file(local) == d["sha256"]
        rep.say("~/.local/bin/meshflash: %s, %s, sha256 %s (%s dist/meshflash)" % (
            d["version"], dl.mb(d["size"]), d["sha256"], "same as" if same else "NOT the same as"))
    else:
        rep.say("~/.local/bin/meshflash: not deployed yet (make deploy)")
    rep.say("~/.bashrc puts ~/.local/bin on PATH: %s" % ("yes" if r["bashrc_path"] else "no (make deploy adds it)"))
    if r["running"]:
        for p in r["running"]:
            rep.say("running: meshflash pid %d: %s" % (p["pid"], p["cmdline"]))
    else:
        rep.say("running: no meshflash")
    rep.say("clock NTP-synchronised: %s" % (r["time_sync"] or "?"))
    for n in r["net"]:
        ok = n["exit"] == 0 and n["out"].isdigit() and n["out"] != "000"
        why = n["out"][3:].strip() if n["out"].startswith("000") else n["out"]
        rep.say("reaches %s: %s" % (n["url"], "yes, HTTP %s" % n["out"] if ok else "NO (%s)" % why))


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    dl.host_arg(ap)
    a = ap.parse_args()
    rep = dl.Report(os.path.join(dl.OUT, "check.log"))
    rep.log("meshflash device check, %s" % dl.stamp())
    local_facts(rep)
    device_facts(rep, a.host)
    rep.save()
    print("(all of it in %s)" % os.path.relpath(rep.log_path, os.getcwd()))


if __name__ == "__main__":
    main()
