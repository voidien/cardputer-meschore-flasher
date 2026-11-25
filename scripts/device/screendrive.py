"""make device-run: the scripted run on the device into a file (the kit's S30), with no board plugged in.

It runs the DEPLOYED ~/.local/bin/meshflash (which must be dist/meshflash: the same sha256, else run make deploy) on
the device over ssh, never on the LCD: --screen draws into a file in a throwaway folder (/tmp/meshflash-s30), keys
come from a FIFO there, and HOME is a throwaway folder there too, so the real ~/.config/meshflash and
~/.cache/meshflash are never read or written. The screen's size comes from a throwaway --framebuffer-sys folder
holding the real fb0's virtual_size, bits_per_pixel and stride (read from /sys, read-only); the font is the device's
own Terminus. It never passes --backlight-sys, never opens /dev/fb0, a backlight or a serial port, and it refuses to
start while any /dev/ttyACM* or /dev/ttyUSB* exists (the Board screen opens every port to ask what it runs). While
meshflash runs it looks again every 0.1 s or so, and on every repeat of a held key, and stops the run if one has
appeared. That is a backstop, not a promise: meshflash looks every second and asks a new port what it runs straight
away, so a board plugged in mid-run may have been opened before the run stops. Keep every board unplugged until the
run has ended.

The keys: the Board; u, which updates the catalog over Wi-Fi from flasher.meshcore.io and zephcore.meshcore.dev (the
app's own fetch, the one network use; into the throwaway folder); the Board again; c, Saved files; ? Help and a
scroll; Tab held 3.4 s to lock; the lock page; the dark screen; a key to wake it; Tab, Enter; the memory (VmHWM,
VmRSS); ? again and Esc held there to quit (on the Board an Esc tap already quits, so the hold starts on Help, whose
Esc closes it, and meshflash should quit once Esc has been down 3 s): the exit code, how long it took, whether the
picture was left blank, and screen.log. Then --screen-test: its page (every character meshflash's screens use, in
Terminus, checked cell by cell), a few keys, Esc held. About a minute and a half, more if the update is slow.

The device side kills the meshflash it started if anything fails and always removes its folder; after Ctrl+C, or a
run that brought nothing back, this side also runs CLEANUP there (here, with --local). The frames come back to
dist/phase3/run-<time>/ as .raw, .png (2x) and .txt (read back cell by cell against a read-only copy of the device's
font, also there), with frames.png holding them all. The summary is printed, one line a frame, and kept as
summary.txt (the one to paste: it leaves out the progress lines), its last line naming it; run.log there has every
frame's text and the progress lines too.

    python3 scripts/device/screendrive.py [--host CardPuter]
    python3 scripts/device/screendrive.py --local --binary dist/meshflash-demo --font <a 6x12 PSF>   (a PC, no ssh)

--local runs the same device program on this PC with the demo build (it refuses the release binary, which on a PC
would open the PC's own serial ports and reach the real sites); the demo's Board shows its two fake boards.
"""

import argparse
import base64
import gzip
import os
import re
import subprocess
import sys
import threading
import time
import zlib

sys.dont_write_bytecode = True  # no __pycache__ in the repository
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import devicelib as dl  # noqa: E402
import fb2png  # noqa: E402
import ocr  # noqa: E402

WORK = "/tmp/meshflash-s30"

# The device program. Its OCR is ocr.py's (OCR_SOURCE, put in front of it), to wait for each screen and to know
# where it is: Esc on the Board quits, so Esc is pressed only once a screen that isn't the Board shows.
DEVICE = r'''
import argparse, errno, fcntl, glob, hashlib, os, shutil, signal, struct, subprocess, sys, time, types

ocr = types.ModuleType("ocr")
exec(OCR_SOURCE, ocr.__dict__)

MARK = ".meshflash-s30"  # in the work folder: only a folder holding it is ever removed
LETTERS = dict(zip("qwertyuiopasdfghjklzxcvbnm", [16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 30, 31, 32, 33, 34, 35,
                                                   36, 37, 38, 44, 45, 46, 47, 48, 49, 50]))
# Linux key codes, as internal/screen/keys.go reads them: ? and - are on the Sym layer, which the kernel applies.
KEYS = {"esc": 1, "bs": 14, "tab": 15, "enter": 28, "shift": 42, "space": 57, "up": 103, "down": 108, "?": 93,
        "-": 71, "2": 3}
KEYS.update(LETTERS)


class Stop(Exception):
    """The run must end now (a signal, the deadline, a serial port appearing)."""


class Lost(Exception):
    """A screen didn't come as expected: skip to the quit."""


def on_signal(sig, frame):
    raise Stop("signal %d" % sig)


GONE = []


def say(msg):
    """A progress line for the T14. Once the T14 has gone (ssh closed: the write fails), the run stops (once)."""
    if GONE:
        return
    try:
        print("  device: " + msg, flush=True)
    except BrokenPipeError:
        GONE.append(True)
        raise Stop("the T14 went away (ssh closed)")


def deathsig():
    # Should this program die without its finally (SIGKILL), the kernel kills the meshflash it started.
    try:
        import ctypes
        ctypes.CDLL(None).prctl(1, signal.SIGKILL, 0, 0, 0)  # PR_SET_PDEATHSIG
    except Exception:
        pass


def ports():
    # One listing of /dev, cheap enough to run every 0.1 s and on every repeat of a held key.
    try:
        names = os.listdir("/dev")
    except OSError:
        return []
    return sorted("/dev/" + n for n in names if n.startswith(("ttyACM", "ttyUSB")))


def sha256(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for b in iter(lambda: f.read(1 << 20), b""):
            h.update(b)
    return h.hexdigest()


class App:
    """One meshflash started into the work folder's framebuffer file, with keys from its FIFO."""

    def __init__(self, run, name, flags):
        self.r, self.name = run, name
        w = run.work
        self.fb = os.path.join(w, "fb")
        with open(self.fb, "wb") as f:
            f.write(b"\xff" * run.size)  # not black, as the launcher's picture isn't: S8's blank is then seen
        kbd = os.path.join(w, "kbd")
        if os.path.exists(kbd):
            os.unlink(kbd)
        os.mkfifo(kbd, 0o600)
        env = {"HOME": run.home, "PATH": os.environ.get("PATH", "/usr/bin:/bin"),
               "LANG": os.environ.get("LANG", "C.UTF-8")}  # no XDG_*: the folders follow HOME
        # The font named even when it is meshflash's own default (the device's Terminus), so the run reads the
        # same file this program reads back with.
        args = [run.a.binary] + flags + ["--framebuffer", self.fb, "--framebuffer-sys", run.sys,
                                         "--keyboard", kbd, "--font", run.a.font]
        self.out = open(os.path.join(w, name + ".out"), "wb")
        self.started = time.time()
        self.p = subprocess.Popen(args, stdin=subprocess.DEVNULL, stdout=self.out, stderr=subprocess.STDOUT,
                                  env=env, cwd=w, preexec_fn=deathsig)
        run.apps.append(self)
        with open(os.path.join(w, "pids"), "a") as f:
            f.write("%d\n" % self.p.pid)
        say("%s: %s started, pid %d" % (name, " ".join(flags), self.p.pid))
        self.k = self.open_keys(kbd, 15)
        self.closed = False

    def open_keys(self, path, timeout):
        end = time.time() + timeout
        while True:
            try:
                fd = os.open(path, os.O_WRONLY | os.O_NONBLOCK)
                fcntl.fcntl(fd, fcntl.F_SETFL, fcntl.fcntl(fd, fcntl.F_GETFL) & ~os.O_NONBLOCK)
                return fd
            except OSError as e:
                if e.errno != errno.ENXIO:
                    raise
            if self.p.poll() is not None:
                raise Lost("%s ended before it opened the keyboard (exit %s)" % (self.name, self.p.returncode))
            if time.time() > end:
                raise Lost("%s didn't open the keyboard in %d s" % (self.name, timeout))
            self.r.guard()
            time.sleep(0.05)

    def ev(self, code, value):
        if self.closed:
            return
        t = time.time()
        try:
            os.write(self.k, struct.pack("<qqHHi", int(t), int((t % 1) * 1e6), 1, code, value))
        except BrokenPipeError:
            self.closed = True  # meshflash quit and closed its end (an Esc hold)

    def press(self, key, shift=False):
        self.r.guard()
        if shift:
            self.ev(KEYS["shift"], 1)
        self.ev(KEYS[key], 1)
        time.sleep(0.03)
        self.ev(KEYS[key], 0)
        if shift:
            self.ev(KEYS["shift"], 0)
        self.r.nap(0.15)

    def hold(self, key, secs):
        """Held as a Linux keyboard holds a key: pressed, repeating every 33 ms after 250 ms, released. The ports
        are looked at on every repeat: a hold is up to 3.4 s on the Board, which meshflash scans every second."""
        self.r.guard()
        c = KEYS[key]
        start = time.time()
        self.ev(c, 1)
        try:
            self.r.nap(0.25)
            while time.time() - start < secs and not self.closed:
                self.ev(c, 2)
                time.sleep(0.033)
                self.r.guard()
        finally:
            self.ev(c, 0)
        return start

    def frame(self):
        with open(self.fb, "rb") as f:
            return f.read()

    def lines(self, raw=None):
        return ocr.read_lines(raw if raw is not None else self.frame(), self.r.font, self.r.width, self.r.height,
                              self.r.stride)

    def wait(self, pred, timeout, what):
        end = time.time() + timeout
        while True:
            self.r.guard()
            raw = self.frame()
            if pred(raw):
                return raw
            if self.p.poll() is not None:
                raise Lost("%s ended (exit %s) while waiting for %s" % (self.name, self.p.returncode, what))
            if time.time() > end:
                first = next((l for l in self.lines(raw) if l.strip()), "(blank)")
                raise Lost("no %s in %d s; the screen showed %r" % (what, timeout, first))
            self.r.nap(0.25)

    def wait_text(self, text, timeout):
        return self.wait(lambda raw: text in "\n".join(self.lines(raw)), timeout, repr(text))

    def on_board(self, raw):
        top = (self.lines(raw) or [""])[0]
        return top.split(" ")[0] in ("meshflash", "meshflash-demo") and "locked" not in top

    def wait_board(self, timeout):
        return self.wait(self.on_board, timeout, "the Board")

    def snap(self, name, raw=None):
        raw = raw if raw is not None else self.frame()
        self.r.frames.append((name, raw))
        first = next((l.strip() for l in self.lines(raw) if l.strip()), "(blank)")
        say("frame %s: %s" % (name, first[:60]))
        return raw

    def memory(self):
        mem = {}
        try:
            with open("/proc/%d/status" % self.p.pid) as f:
                for line in f:
                    if line.startswith(("VmHWM", "VmRSS", "VmPeak")):
                        k, v = line.split(":", 1)
                        mem[k] = v.strip()
        except OSError as e:
            mem["error"] = str(e)
        say("%s memory: %s" % (self.name, ", ".join("%s %s" % kv for kv in sorted(mem.items()))))
        return mem

    def end(self, timeout, since):
        """Waits for it to quit; returns the exit code (or what happened) and the seconds since `since`."""
        deadline = time.time() + timeout
        while True:
            try:
                code = self.p.wait(timeout=0.1)
                break
            except subprocess.TimeoutExpired:
                pass
            if time.time() > deadline:
                self.p.kill()
                self.p.wait()
                code = "killed: it hadn't quit %d s after the hold" % timeout
                break
            self.r.guard()
        took = round(time.time() - since, 2)
        try:
            os.close(self.k)
        except OSError:
            pass
        self.out.close()
        say("%s exit: %s, %.2f s after the hold began" % (self.name, code, took))
        return code, took

    def stop(self):
        if self.p.poll() is None:
            self.p.terminate()  # meshflash's own way out: it absorbs SIGTERM and quits the safe way
            try:
                self.p.wait(timeout=4)
            except subprocess.TimeoutExpired:
                self.p.kill()
                self.p.wait()
            say("%s stopped (exit %s)" % (self.name, self.p.returncode))


class Run:
    def __init__(self, a):
        self.a = a
        self.work = a.work
        self.home = os.path.join(a.work, "home")
        self.sys = os.path.join(a.work, "sys")
        self.apps, self.frames, self.notes = [], [], []
        self.result = {"notes": self.notes}

    def guard(self):
        """Stops the run if a serial port has appeared. Called every 0.1 s or so while meshflash runs, but meshflash
        looks for ports every second and asks a new one what it runs straight away, so it may get there first: the
        owner keeps every board unplugged until the run ends (docs/phase3.md step 5)."""
        if self.a.demo_only:
            return  # the demo build opens no port: its boards are fake
        p = ports()
        if p:
            raise Stop("a serial port appeared (%s): the run stopped; unplug the boards for this run" % " ".join(p))

    def nap(self, secs):
        """A sleep that looks for serial ports every 0.1 s."""
        end = time.time() + secs
        while True:
            self.guard()
            left = end - time.time()
            if left <= 0:
                return
            time.sleep(min(0.1, left))

    def setup(self):
        a = self.a
        if os.path.lexists(self.work):
            if not os.path.exists(os.path.join(self.work, MARK)):
                raise Stop("refused: %s exists and isn't this script's: remove it or pass another --work" % self.work)
            shutil.rmtree(self.work)
        os.makedirs(self.home)
        os.makedirs(self.sys)
        open(os.path.join(self.work, MARK), "w").close()
        geo = {"virtual_size": "320,170", "bits_per_pixel": "16", "stride": "640"}
        real = {}
        for k in geo:
            try:
                with open(os.path.join(a.fb_sys, k)) as f:
                    real[k] = f.read().strip()
            except OSError:
                pass
        self.result["fb0_sys"] = real
        if len(real) == 3:
            geo = real
            say("screen size from %s: %s, %s bits, stride %s" % (a.fb_sys, geo["virtual_size"],
                                                                  geo["bits_per_pixel"], geo["stride"]))
        else:
            say("screen size: %s unreadable, so 320,170, 16 bits, stride 640" % a.fb_sys)
        for k, v in geo.items():
            with open(os.path.join(self.sys, k), "w") as f:
                f.write(v + "\n")
        self.width, self.height = (int(x) for x in geo["virtual_size"].split(","))
        self.stride = int(geo["stride"])
        self.size = self.stride * self.height
        self.result["geometry"] = [self.width, self.height, self.stride, geo["bits_per_pixel"]]
        self.font = ocr.load_font(a.font)
        with open(a.font, "rb") as f:
            self.result["font"] = [os.path.basename(a.font), f.read().hex()]

    def screen_log(self):
        logs = glob.glob(os.path.join(self.home, ".cache", "*", "screen.log"))
        if not logs:
            return "(no screen.log)"
        with open(logs[0], "rb") as f:
            return f.read(200000).decode("utf-8", "replace")

    def update(self, s, t0):
        """Waits out the catalog update (u was pressed at t0) and records how it ended, from the screen: "enter
        done" is its end once the catalog is in (internal/ui/update.go), "u try again" a failure, whose words are
        the lines between the title and the keys. Leaves the Update screen."""
        def ended(raw):
            text = "\n".join(s.lines(raw))
            return "Fetching the catalog" not in text or not text.startswith("Update the catalog")
        upd = self.result["update"] = {}
        deadline = t0 + 150
        while True:
            try:
                s.wait(ended, max(1, deadline - time.time()), "the end of the fetch")
            except Lost:
                if s.p.poll() is not None:
                    raise
                upd.update(secs=round(time.time() - t0, 1), outcome="still")
                s.snap("03-update-still-fetching")
                s.press("esc")
                return
            secs = round(time.time() - t0, 1)
            self.nap(0.3)  # the frame is drawn a band at a time: read it again once it has settled
            raw = s.frame()
            if ended(raw):
                break
        upd["secs"] = secs
        lines = [" ".join(l.split()) for l in s.lines(raw) if l.strip()]
        upd["lines"] = lines[1:-1]
        if not lines or lines[0].split(" ")[0] != "Update":
            upd["outcome"], upd["lines"] = "elsewhere", lines[:1]
            s.snap("03-update-elsewhere", raw)
            return
        if "enter done" in lines[-1]:
            upd["outcome"] = "ok"
            s.snap("03-update-done", raw)
            s.press("enter")
        else:
            upd["outcome"] = "failed" if "try again" in lines[-1] else "unclear"
            s.snap("03-update-failed", raw)
            s.press("esc")

    def session(self):
        s = App(self, "session", ["--screen"])
        try:
            s.wait_board(20)
            self.nap(1.5)  # the first USB scan, and with the demo its probes
            s.snap("01-board")
            t0 = time.time()
            s.press("u")
            s.wait_text("Update the catalog", 5)
            self.nap(0.6)
            s.snap("02-update")
            self.update(s, t0)
            s.wait_board(5)
            self.nap(0.5)
            s.snap("04-board-after-update")
            s.press("c")
            s.wait_text("Saved files", 5)
            self.nap(0.3)
            s.snap("05-saved-files")
            s.press("esc")
            s.wait_board(5)
            s.press("?")
            s.wait(lambda raw: s.lines(raw)[0].startswith("Help"), 5, "Help")
            self.nap(0.3)
            s.snap("06-help")
            for _ in range(4):
                s.press("j")
            self.nap(0.3)
            s.snap("07-help-scrolled")
            s.press("esc")
            s.wait_board(5)
            start = s.hold("tab", 3.4)
            locked_at = start + 3.0  # internal/screen sends the lock once Tab has been down 3 s
            s.wait_text("to unlock", 3)
            self.nap(0.2)
            s.snap("08-lock-page")
            try:
                raw = s.wait(ocr.is_black, 9, "the dark screen")
                self.result["dark_after_secs"] = round(time.time() - locked_at, 1)
                s.snap("09-lock-dark", raw)
            except Lost:
                self.notes.append("the lock page wasn't all black 9 s after the lock")
                s.snap("09-lock-not-dark")
            s.press("j")
            s.wait_text("Tab then Enter to unlock", 3)
            self.nap(0.2)
            s.snap("10-lock-woken")
            s.press("tab")
            s.wait(lambda raw: (s.lines(raw) or [""])[-1].strip() == "Enter to unlock", 3, "'Enter to unlock'")
            s.snap("11-lock-armed")
            s.press("enter")
            s.wait_board(3)
            self.nap(0.3)
            s.snap("12-unlocked")
        except Lost as e:
            self.notes.append("session: %s; went straight to the Esc hold" % e)
            say("lost: %s" % e)
            if s.p.poll() is None:
                s.snap("session-lost")
        if s.p.poll() is None:
            self.result["memory"] = s.memory()
            # Esc held from Help: on the Board a tap already quits, so the hold's own 3 s shows only elsewhere. The
            # press closes Help; on the LCD the repeats never reach the screens, and the hold quits at 3 s.
            try:
                if s.on_board(s.frame()):
                    s.press("?")
                    s.wait(lambda raw: s.lines(raw)[0].startswith("Help"), 5, "Help")
                    s.snap("13-help-before-esc-hold")
            except Lost as e:
                self.notes.append("before the Esc hold: %s" % e)
            start = s.hold("esc", 3.4)
            self.result["session_exit"] = s.end(8, start)
            self.result["session_blank"] = ocr.is_black(s.frame())
        else:
            self.result["session_exit"] = ["ended early: exit %s" % s.p.returncode, 0]
        self.result["session_log"] = self.screen_log()

    def testpage(self):
        t = App(self, "testpage", ["--screen-test"])
        try:
            t.wait_text("screen test", 10)
            self.nap(0.5)
            t.snap("14-testpage")
            t.press("a", shift=True)
            t.press("-")
            t.press("up")
            t.press("2", shift=True)
            t.wait_text('"@"', 3)  # the last key down; the page counts Shift's presses too
            t.snap("15-testpage-keys")
        except Lost as e:
            self.notes.append("test page: %s" % e)
        if t.p.poll() is None:
            start = t.hold("esc", 3.4)
            self.result["test_exit"] = t.end(8, start)
            self.result["test_blank"] = ocr.is_black(t.frame())
        else:
            self.result["test_exit"] = ["ended early: exit %s" % t.p.returncode, 0]
        self.result["test_log"] = self.screen_log()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--binary", required=True)
    ap.add_argument("--sha256", default="")
    ap.add_argument("--demo-only", action="store_true")
    ap.add_argument("--work", required=True)
    ap.add_argument("--font", required=True)
    ap.add_argument("--fb-sys", required=True)
    ap.add_argument("--deadline", type=int, default=420)
    a = ap.parse_args()
    a.binary = os.path.expanduser(a.binary)
    for s in (signal.SIGTERM, signal.SIGHUP, signal.SIGINT, signal.SIGALRM):
        signal.signal(s, on_signal)
    signal.alarm(a.deadline)
    run = Run(a)
    res = run.result
    res["python"] = sys.version.split()[0]
    try:
        p = [] if a.demo_only else ports()
        if p:
            raise Stop("refused: serial ports are present (%s). Unplug the boards for this run: meshflash's Board "
                       "screen opens every port to ask what it runs." % " ".join(p))
        if not os.path.isfile(a.binary):
            raise Stop("refused: %s isn't there (make deploy)" % a.binary)
        res["sha256"] = sha256(a.binary)
        if a.sha256 and res["sha256"] != a.sha256:
            raise Stop("refused: %s isn't dist/meshflash (sha256 %s, dist/meshflash %s): run make deploy" %
                       (a.binary, res["sha256"][:16], a.sha256[:16]))
        v = subprocess.run([a.binary, "--version"], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=10)
        res["version"] = v.stdout.decode("utf-8", "replace").strip()
        if a.demo_only and not res["version"].startswith("meshflash-demo "):
            raise Stop("refused: %s is %r, not the demo build: on a PC only the demo runs (the release binary "
                       "would open this PC's serial ports and reach the real sites)" % (a.binary, res["version"]))
        if not os.path.isfile(a.font):
            raise Stop("refused: no font at %s" % a.font)
        say("%s (%s), sha256 %s" % (a.binary, res["version"], res["sha256"][:16]))
        run.setup()
        run.session()
        run.testpage()
    except (Stop, Lost) as e:
        if str(e).startswith("refused:") and not run.apps:
            res["refused"] = str(e)[len("refused: "):]
        else:
            run.notes.append(str(e))
        say(str(e))
    except Exception as e:
        run.notes.append("the device program failed: %r" % e)
        say("failed: %r" % e)
    finally:
        signal.alarm(0)
        for s in (signal.SIGTERM, signal.SIGHUP, signal.SIGINT):
            signal.signal(s, signal.SIG_IGN)  # the cleanup itself is not interrupted
        for app in run.apps:
            try:
                app.stop()
            except Exception as e:
                run.notes.append("stopping %s: %r" % (app.name, e))
            key = {"session": "session_out", "testpage": "test_out"}.get(app.name, app.name + "_out")
            if key not in res:
                try:
                    with open(os.path.join(a.work, app.name + ".out"), "rb") as f:
                        res[key] = f.read(20000).decode("utf-8", "replace")
                except OSError:
                    pass
        if run.apps and "session_log" not in res:
            res["session_log"] = run.screen_log()
        if os.path.exists(os.path.join(a.work, MARK)):
            shutil.rmtree(a.work, ignore_errors=True)
        res["work_removed"] = not os.path.exists(a.work)
    res["frames"] = [[n, base64.b64encode(zlib.compress(raw, 9)).decode()] for n, raw in run.frames]
    # Over a link that has died the result can fill the pipe and block: the folder is already gone, and this
    # program ends anyway 2 min on (SIGALRM still raises Stop).
    signal.alarm(120)
    try:
        emit(res)
    except (BrokenPipeError, Stop):
        pass
    signal.alarm(0)


main()
'''

# A last resort, from the T14, when the run's ssh failed or was stopped (or here, after Ctrl+C on a --local run):
# stop what it started and remove its folder. A pid is killed only while its command line still names the folder
# (its --framebuffer), so a number the system has since given another program is left alone.
CLEANUP = r'''
import os, shutil, signal, sys
work = sys.argv[1]
if os.path.exists(os.path.join(work, ".meshflash-s30")):
    try:
        for line in open(os.path.join(work, "pids")):
            try:
                pid = int(line)
                with open("/proc/%d/cmdline" % pid, "rb") as f:
                    if (work.rstrip("/") + "/fb").encode() in f.read().split(b"\0"):
                        os.kill(pid, signal.SIGKILL)
            except (OSError, ValueError):
                pass
    except OSError:
        pass
    shutil.rmtree(work, ignore_errors=True)
print("removed" if not os.path.exists(work) else "still there")
'''

FRAME_HINT = {
    "01-board": "the Board",
    "02-update": "u: Update the catalog, fetching",
    "03-update-done": "the update's end: the catalog is in",
    "03-update-failed": "the update's end: it failed, in its words",
    "03-update-still-fetching": "still fetching at 150 s: Esc stops it",
    "03-update-elsewhere": "the update left its screen",
    "04-board-after-update": "the Board after the update",
    "05-saved-files": "c: Saved files",
    "06-help": "?: Help",
    "07-help-scrolled": "Help after j x4",
    "08-lock-page": "Tab held 3.4 s: the lock page",
    "09-lock-dark": "the lock, dark",
    "09-lock-not-dark": "the lock 9 s on, not all black",
    "10-lock-woken": "j woke it",
    "11-lock-armed": "Tab: armed",
    "12-unlocked": "Enter: unlocked",
    "13-help-before-esc-hold": "? again: Esc is held from here",
    "14-testpage": "--screen-test",
    "15-testpage-keys": "--screen-test after Shift+a, -, Up, Shift+2",
    "session-lost": "where the --screen run lost its way",
}


def device_program():
    with open(os.path.join(dl.HERE, "ocr.py")) as f:
        src = f.read()
    return "OCR_SOURCE = %r\n" % src + dl.DEVICE_HELPERS + DEVICE


def glyphs_from_repo():
    """screen.Glyphs, read from internal/screen/testpage.go so the check follows the code."""
    try:
        with open(os.path.join(dl.REPO, "internal", "screen", "testpage.go"), encoding="utf-8") as f:
            m = re.search(r'^const Glyphs = "(.*)"$', f.read(), re.M)
        if m:
            return m.group(1)
    except OSError:
        pass
    return ocr.PREFERRED


def stream(cmd, data, rep, timeout):
    """Runs cmd with data on stdin, printing its progress lines as they come. Killed after timeout seconds (the
    device program ends itself well before). Returns a dict: code, result (the result line, or None), err (stderr),
    heard (how many lines came), last (when the last came), timed_out (whether the timeout killed it)."""
    p = subprocess.Popen(cmd, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    st = {"result": None, "heard": 0, "last": time.time(), "timed_out": False}

    def expire():
        st["timed_out"] = True
        p.kill()

    killer = threading.Timer(timeout, expire)
    killer.start()
    errs = []
    reader = threading.Thread(target=lambda: errs.append(p.stderr.read()))
    reader.start()
    try:
        try:
            p.stdin.write(data)
            p.stdin.close()
        except BrokenPipeError:
            pass  # ssh ended at once (no connection): its exit code and stderr say why
        for raw in p.stdout:
            st["heard"] += 1
            st["last"] = time.time()
            line = raw.decode("utf-8", "replace").rstrip("\n")
            if line.startswith(dl.RESULT_MARK):
                st["result"] = line
            else:
                print(line, flush=True)
                rep.log(line)
        st["code"] = p.wait()
    except KeyboardInterrupt:
        # Ctrl+C. With --local it reached the device program too (one process group); over ssh, ssh ends. The
        # device program stops meshflash and removes its folder itself: give it the time to, with its output
        # closed so it can't block on writing it. main() then runs CLEANUP, for what is left.
        try:
            p.stdout.close()
        except OSError:
            pass
        try:
            p.wait(timeout=15)
        except (subprocess.TimeoutExpired, KeyboardInterrupt):
            p.kill()
        raise
    except BaseException:
        p.kill()
        raise
    finally:
        killer.cancel()
        reader.join(15)
    st["err"] = b"".join(errs).decode("utf-8", "replace")
    return st


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    dl.host_arg(ap)
    ap.add_argument("--local-binary", default=os.path.join(dl.REPO, "dist", "meshflash"),
                    help="the build the deployed binary must match (default dist/meshflash)")
    ap.add_argument("--remote-binary", default="~/.local/bin/meshflash", help="the deployed binary on the device")
    ap.add_argument("--work", default=WORK, help="the throwaway folder on the device (default %s)" % WORK)
    ap.add_argument("--local", action="store_true", help="run here, without ssh (a PC: the demo build only)")
    ap.add_argument("--binary", default=os.path.join(dl.REPO, "dist", "meshflash-demo"), help="with --local")
    ap.add_argument("--font", help="with --local: a 6x12 PSF font (the device's own Terminus is used there)")
    ap.add_argument("--out", help="where the frames go (default dist/phase3/run-<time>)")
    a = ap.parse_args()

    out = a.out or dl.out_dir("run-" + dl.stamp())
    os.makedirs(out, exist_ok=True)
    rep = dl.Report(os.path.join(out, "run.log"))
    program = device_program().encode()
    if a.local:
        if not a.font:
            dl.die("--local needs --font: a 6x12 PSF font (on the device meshflash uses Terminus)")
        work = a.work if a.work != WORK else os.path.join(out, "work")
        args = ["--binary", os.path.abspath(a.binary), "--demo-only", "--work", os.path.abspath(work),
                "--font", os.path.abspath(a.font), "--fb-sys", "/nonexistent"]
        cmd = [sys.executable, "-", ] + args
        where = "this PC (--local)"
    else:
        if not os.path.exists(a.local_binary):
            dl.die("%s isn't built: make build-arm64, then make deploy" % a.local_binary)
        sha = dl.sha256_file(a.local_binary)
        args = ["--binary", a.remote_binary, "--sha256", sha, "--work", a.work, "--font", dl.DEVICE_FONT,
                "--fb-sys", "/sys/class/graphics/fb0"]
        cmd = ["ssh"] + dl.SSH_OPTS + [a.host, dl.python_command(args)]
        where = a.host
        work = a.work
    rep.say("meshflash device run (S30) on %s, %s" % (where, time.strftime("%Y-%m-%d %H:%M")))
    rep.log("command: %s  (the device program on its stdin)" % " ".join(cmd))
    try:
        st = stream(cmd, program, rep, TIMEOUT)
    except FileNotFoundError as e:
        dl.die("can't run %s: %s" % (cmd[0], e))
    except KeyboardInterrupt:
        rep.say("STOPPED (Ctrl+C).")
        cleanup(a, work, rep)
        finish(rep, out, False)
        sys.exit(130)
    err_text = dl.quiet(st["err"])
    if err_text:
        rep.log("stderr:\n" + err_text)
    if st["result"] is None:
        rep.say("FAILED: %s" % failure(a, st, err_text))
        if a.local or st["heard"] or st["timed_out"] or st.get("code") != 255:
            cleanup(a, work, rep)  # it may have started: not when ssh never connected
        finish(rep, out, False)
        sys.exit(1)
    r = dl.find_result(st["result"])
    if r.get("refused"):
        rep.say("REFUSED, nothing ran: %s" % r["refused"])
        finish(rep, out, False)
        sys.exit(1)
    finish(rep, out, report(r, out, rep))


TIMEOUT = 600  # the T14's own limit on the run; the device program stops itself at 420 s (its --deadline)


def failure(a, st, err_text):
    """Why the run brought no result back, in plain words."""
    code = st.get("code")
    if st["timed_out"]:
        heard = ("the last word from the device came %d s before that" % (time.time() - st["last"])
                 if st["heard"] else "not a word came from the device")
        return ("no result %d s after the start, so this PC stopped waiting (the device program stops itself at "
                "420 s); %s. The connection dropped or the device slept: wake it, then make device-run again." % (
                    TIMEOUT, heard))
    if not a.local and code == 255 and st["heard"]:
        return ("the connection to %s dropped mid-run (ssh heard nothing back for about 40 s, or the link closed): "
                "the device slept or left the Wi-Fi. Wake it, then make device-run again.%s" % (
                    a.host, " ssh said: %s" % err_text if err_text else ""))
    why = "" if a.local else dl.why_ssh_failed(a.host, code, st["err"])
    return why or "the device program ended without a result (exit %s): %s" % (code, err_text[-1500:] or
                                                                                "no message")


def cleanup(a, work, rep):
    """CLEANUP: on the device over a new ssh, or here with --local."""
    if a.local:
        try:
            c = subprocess.run([sys.executable, "-", work], input=CLEANUP.encode(), stdout=subprocess.PIPE,
                               stderr=subprocess.PIPE, timeout=30)
            rep.say("cleanup here: %s" % (c.stdout.decode().strip() or c.stderr.decode().strip()[-300:] or
                                          "exit %d" % c.returncode))
        except subprocess.TimeoutExpired:
            rep.say("cleanup here: no answer in 30 s")
        return
    c, o, e = dl.ssh(a.host, dl.python_command([work]), CLEANUP.encode(), 30)
    why = dl.why_ssh_failed(a.host, c, e)
    if why:
        rep.say("cleanup on the device: not reached. The device program stops meshflash and removes %s itself "
                "when it ends, 7 min after its start at the most. (%s)" % (work, why))
    else:
        rep.say("cleanup on the device: %s" % (o.decode().strip() or dl.quiet(e) or "exit %d" % c))


def shown(path):
    """A path as the summary names it: from here (make runs in the repository: dist/phase3/...), else in full."""
    rel = os.path.relpath(path, os.getcwd())
    return os.path.abspath(path) if rel.startswith("..") else rel


def finish(rep, out, frames):
    """The last summary line says what to paste, then the summary and the full log are written."""
    rel = shown(out)
    rep.say("Paste %s (this summary, without the progress lines)%s." % (
        os.path.join(rel, "summary.txt"), "; attach %s" % os.path.join(rel, "frames.png") if frames else ""))
    rep.save()
    with open(os.path.join(out, "summary.txt"), "w") as f:
        f.write("\n".join(rep.summary) + "\n")


def squeeze(lines):
    """Lines with each run of the same line said once, with its count (a held key's repeats)."""
    out = []
    for l in lines:
        if out and out[-1][0] == l:
            out[-1][1] += 1
        else:
            out.append([l, 1])
    return [l if n == 1 else "%s  (x%d)" % (l, n) for l, n in out]


def gist(text, n=2, width=110):
    """A frame's first lines on one line, spaces squeezed: enough to tell it apart (its .txt has it all)."""
    lines = [" ".join(l.split()) for l in text.splitlines() if l.strip()][:n]
    g = " | ".join(lines) or "(blank)"
    return g if len(g) <= width else g[:width - 1] + "…"


# A key line of --screen-test's screen.log (internal/screen/testpage.go): `code 42 down`, `"A" (code 30) up`.
KEY_LINE = re.compile(r'^(code \d+|".*" \(code \d+\)) (up|down|repeat)$')


def update_line(u):
    """The catalog update's outcome, from the Update screen's own words."""
    words = " ".join(u.get("lines") or []) or "(no words)"  # its lines are wrapped sentences
    outcome = u.get("outcome")
    if outcome == "ok":
        return "catalog update: OK in %.1f s: %s" % (u["secs"], words)
    if outcome == "failed":
        return "catalog update FAILED after %.1f s: %s" % (u["secs"], words)
    if outcome == "still":
        return "catalog update: still fetching at %.0f s, stopped with Esc" % u["secs"]
    if outcome == "elsewhere":
        return "catalog update: after %.1f s the Update screen had gone; the screen showed: %s" % (u["secs"], words)
    return "catalog update: unclear after %.1f s; the screen said: %s" % (u.get("secs", 0), words)


def report(r, out, rep):
    """The summary of a run that came back; returns whether it brought frames."""
    rep.say("binary: %s, sha256 %s" % (r.get("version", "?"), r.get("sha256", "?")[:16]))
    font = None
    if "font" in r:
        name, hexdata = r["font"]
        data = bytes.fromhex(hexdata)
        path = os.path.join(out, name)
        if os.path.exists(path):
            os.chmod(path, 0o644)
        with open(path, "wb") as f:
            f.write(data)
        os.chmod(path, 0o444)
        font = ocr.parse_font(gzip.decompress(data) if data[:2] == b"\x1f\x8b" else data)
    geo = r.get("geometry") or [320, 170, 640, "16"]
    w, h, stride = geo[0], geo[1], geo[2]
    if font:
        cols, rows, left, top = ocr.layout(font, w, h)
        rep.say("screen: %dx%d px, stride %d (%s); %dx%d cells in %s" % (
            w, h, stride, "the real fb0's" if len(r.get("fb0_sys", {})) == 3 else "the defaults", cols, rows,
            r["font"][0]))
    for n in r.get("notes", []):
        rep.say("NOTE: %s" % n)
    if "update" in r:
        rep.say(update_line(r["update"]))
    frames = []
    rep.say("frames (each one's text in its .txt, all of it in run.log):")
    for name, data in r.get("frames", []):
        raw = zlib.decompress(base64.b64decode(data))
        frames.append((name, raw))
        with open(os.path.join(out, name + ".raw"), "wb") as f:
            f.write(raw)
        fb2png.write_png(raw, os.path.join(out, name + ".png"), 2, w, h, stride)
        text = "(all black)" if ocr.is_black(raw) else (ocr.read_text(raw, font, w, h, stride) if font else "")
        with open(os.path.join(out, name + ".txt"), "w") as f:
            f.write(text + "\n")
        hint = FRAME_HINT.get(name)
        rep.say("  %s%s: %s" % (name, " (%s)" % hint if hint else "", gist(text)))
        rep.log("-- %s" % name)
        for l in text.splitlines():
            if l.strip():
                rep.log("   |" + l)
    if frames:
        fb2png.contact_sheet(frames, os.path.join(out, "frames.png"), font, 3, 1, w, h, stride)
    else:
        rep.say("  none came back")
    if "dark_after_secs" in r:
        rep.say("lock: dark %.1f s after the lock (the screens say 5 s), all black: yes" % r["dark_after_secs"])
    tp = dict(frames).get("14-testpage")
    if tp is not None and font:
        expected = glyphs_from_repo()
        row, res = ocr.glyph_check(tp, font, expected, w, h, stride)
        bad = [(c, v, got) for c, v, got in res if v != "ok"]
        own = "".join(c for c, v, _ in res if v == "ok" and font.has(c) and c not in font.extra)
        stand = "".join(c for c, v, _ in res if v == "ok" and c in font.extra)
        rep.say("glyphs on the test page (row %d): %d of %d drawn right; the font's own: %s; meshflash's "
                "stand-ins: %s" % (row, len(res) - len(bad), len(res), own, stand or "none"))
        for c, v, got in bad:
            rep.say("  GLYPH %s (U+%04X): %s" % (c, ord(c), "drawn as the font's '?': the font lacks it"
                                                  if v == "?" else "drawn as %r" % got))
    mem = r.get("memory")
    if mem:
        hwm = mem.get("VmHWM", "?")
        rep.say("memory before the Esc hold: VmHWM %s, VmRSS %s: %s" % (hwm, mem.get("VmRSS", "?"),
                                                                       dl.s9(hwm)))
    for key, blank, what in (("session_exit", "session_blank", "--screen (Esc held on Help)"),
                             ("test_exit", "test_blank", "--screen-test")):
        if key in r:
            code, took = r[key]
            rep.say("%s: exit %s, %.2f s after Esc went down (it quits at 3 s); picture left blank: %s" % (
                what, code, took, {True: "yes", False: "NO", None: "?"}[r.get(blank)]))
    for key, what in (("session_log", "screen.log (--screen run)"), ("test_log", "screen.log (--screen-test)")):
        if key in r:
            fname = key.replace("_", "-") + ".txt"
            with open(os.path.join(out, fname), "w") as f:
                f.write(r[key])
            lines = r[key].strip().splitlines()
            keys = [l for l in lines if KEY_LINE.match(l)]
            rep.say("%s:" % what)
            for l in squeeze(l for l in lines if not KEY_LINE.match(l)):
                rep.say("   " + l)
            if keys:
                downs = [KEY_LINE.match(l).group(1) for l in keys if l.endswith(" down")]
                rep.say("   (%d key lines, all in %s; down: %s)" % (len(keys), fname, ", ".join(downs)))
    for key in ("session_out", "test_out"):
        if r.get(key, "").strip():
            rep.say("%s printed: %s" % (key.split("_")[0], r[key].strip()[:600]))
    rep.say("device folder removed: %s" % ("yes" if r.get("work_removed") else "NO"))
    if frames:
        rel = shown(out)
        rep.say("PNGs in %s: frames.png (every frame on one sheet), and one per frame (2x): %s" % (
            rel, " ".join(n + ".png" for n, _ in frames)))
    return bool(frames)


if __name__ == "__main__":
    main()
