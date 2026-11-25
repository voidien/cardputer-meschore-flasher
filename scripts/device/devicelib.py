"""What the Phase 3 scripts share: reaching the device over ssh, saying why that failed, and where output goes.

Every script here runs on the owner's PC (the T14) from the repository, and reaches the CardputerZero with the
owner's own ssh alias (CardPuter by default, HOST=... in make), always with BatchMode=yes: no password prompt, so a
script never waits on one and never sees one. What runs on the device is a short Python program sent on ssh's
standard input (`python3 -`), which reads and prints and writes nothing there unless its docstring says so.
Output goes to dist/phase3/ (dist/ is git-ignored). Standard library only (Python 3.9 or newer).
"""

import base64
import datetime
import hashlib
import json
import os
import shlex
import subprocess
import sys
import zlib

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.dirname(os.path.dirname(HERE))
OUT = os.path.join(REPO, "dist", "phase3")
DEFAULT_HOST = "CardPuter"
DEVICE_FONT = "/usr/share/consolefonts/Uni2-Terminus12x6.psf.gz"  # internal/screen DefaultFont
# BatchMode: never a password prompt. ConnectTimeout: a device asleep or off the Wi-Fi fails in 8 s, not TCP's 2 min.
# ServerAlive: a link that dies mid-way ends ssh (exit 255) after about 40 s without an answer, rather than never.
# LogLevel=ERROR: no login notice (and no "Timeout, server not responding" either: why_ssh_failed says it instead).
SSH_OPTS = ["-o", "BatchMode=yes", "-o", "ConnectTimeout=8", "-o", "ServerAliveInterval=10",
            "-o", "ServerAliveCountMax=3", "-o", "LogLevel=ERROR"]

# The Zero's sshd prints this notice to every login (the kit's docs/making-apps.md, "SSH habits"): not an error.
BANNER = ("Please note", "valid user", "rptl.io")

# The line a device program prints its result on, base64 of zlib of JSON, so it can't be mixed up with the rest.
RESULT_MARK = "@@MESHFLASH-PHASE3@@ "


def stamp():
    return datetime.datetime.now().strftime("%Y%m%d-%H%M%S")


def out_dir(*parts):
    path = os.path.join(OUT, *parts)
    os.makedirs(path, exist_ok=True)
    return path


def sha256_file(path):
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for block in iter(lambda: f.read(1 << 20), b""):
            h.update(block)
    return h.hexdigest()


def mb(n):
    return "%.1f MB" % (n / 1e6)


def s9(vmhwm):
    """S9's verdict on a VmHWM line ("12345 kB"): under 40 MB, counted as the kit's docs/device.md counts it
    (/proc's kB over 1024: Loom's 11673 kB is its "11.4 MB")."""
    try:
        kb = int(vmhwm.split()[0])
    except (AttributeError, ValueError, IndexError):
        return "can't read it"
    mb = kb / 1024
    verdict = "under 40 MB (S9)" if mb < 40 else "OVER 40 MB (S9)"
    return "%.1f MB, %s%s" % (mb, verdict, ", but close" if 36 <= mb < 40 else "")


def quiet(stderr):
    """stderr without the login notice."""
    return "\n".join(l for l in stderr.splitlines() if l.strip() and not any(b in l for b in BANNER))


def why_ssh_failed(host, code, stderr):
    """ssh's failure in plain words, with what to do about it, or "" when ssh itself worked (the command failed)."""
    if code != 255:
        return ""
    e = quiet(stderr)
    low = e.lower()
    if e.startswith("ssh: no answer within"):  # ssh()'s own time limit, not ssh's
        return ("%s stopped answering before the command ended (%s): the link dropped or the device slept. Wake it "
                "and try again." % (host, e[len("ssh: "):]))
    if "permission denied" in low:
        return ("%s refused a key login. These scripts use BatchMode=yes, which never asks for a password, so they "
                "need a key login: `ssh-copy-id %s` sets one up (it asks for void's password once; your call). "
                "ssh said: %s" % (host, host, e))
    if "could not resolve hostname" in low or "name or service not known" in low:
        return ("This PC doesn't know the host %r: it is the ssh alias in ~/.ssh/config (Host %s, with HostName "
                "and User void). Add it, or run with HOST=<name or address>. ssh said: %s" % (host, host, e))
    if "timed out" in low or "no route to host" in low or "network is unreachable" in low:
        return ("%s didn't answer within 8 s: asleep, switched off, or not on this Wi-Fi. Wake it (press a key on "
                "it) and try again. ssh said: %s" % (host, e))
    if "connection refused" in low:
        return "%s answered, but no ssh server listens there. ssh said: %s" % (host, e)
    if "host key verification failed" in low or "identification has changed" in low:
        return ("This PC doesn't trust %s's host key (new, or changed). Run `ssh %s` once by hand and check the "
                "fingerprint before saying yes (your call). ssh said: %s" % (host, host, e))
    if not e:
        return ("ssh to %s failed (exit 255) without a word: most often the link dropped (ssh gives up after about "
                "40 s with no answer): the device slept or left the Wi-Fi. Wake it and try again." % host)
    return "ssh to %s failed (exit 255): %s" % (host, e)


def ssh(host, command, data=None, timeout=60):
    """Runs one command on the device; returns (exit code, stdout bytes, stderr text)."""
    try:
        r = subprocess.run(["ssh"] + SSH_OPTS + [host, command], input=data, stdout=subprocess.PIPE,
                           stderr=subprocess.PIPE, timeout=timeout)
    except FileNotFoundError:
        return 255, b"", "ssh: this PC has no ssh command"
    except subprocess.TimeoutExpired:
        return 255, b"", "ssh: no answer within %d s (timed out)" % timeout
    return r.returncode, r.stdout, r.stderr.decode("utf-8", "replace")


def python_command(args):
    return "python3 - " + " ".join(shlex.quote(a) for a in args)


def run_device_python(host, source, args=(), timeout=120):
    """Runs a Python program on the device (sent on stdin) and returns its result (the JSON it printed after
    RESULT_MARK), or raises Failed saying why."""
    code, out, err = ssh(host, python_command(list(args)), source.encode(), timeout)
    why = why_ssh_failed(host, code, err)
    if why:
        raise Failed(why)
    result = find_result(out.decode("utf-8", "replace"))
    if result is None:
        tail = quiet(err)
        if "python3: command not found" in tail or "python3: not found" in tail:
            raise Failed("the device has no python3 (the kit's docs/device.md lists 3.13 there)")
        raise Failed("the device program ended without a result (exit %d): %s" % (code, tail[-2000:] or "no message"))
    return result


def find_result(text):
    for line in text.splitlines():
        if line.startswith(RESULT_MARK):
            return json.loads(zlib.decompress(base64.b64decode(line[len(RESULT_MARK):])).decode())
    return None


class Failed(Exception):
    pass


# The device side's half of RESULT_MARK, pasted at the top of each device program.
DEVICE_HELPERS = r'''
import base64, json, zlib
RESULT_MARK = %r
def emit(obj):
    print(RESULT_MARK + base64.b64encode(zlib.compress(json.dumps(obj).encode(), 9)).decode(), flush=True)
''' % RESULT_MARK


class Report:
    """Lines for the owner to paste (the summary) and the full log, written together."""

    def __init__(self, log_path):
        self.log_path = log_path
        self.summary = []
        self.full = []

    def say(self, line=""):
        """A summary line: printed, and in the log."""
        print(line, flush=True)
        self.summary.append(line)
        self.full.append(line)

    def log(self, line=""):
        """A detail for the log only."""
        self.full.append(line)

    def save(self):
        os.makedirs(os.path.dirname(self.log_path), exist_ok=True)
        with open(self.log_path, "w") as f:
            f.write("\n".join(self.full) + "\n")


def host_arg(ap):
    ap.add_argument("--host", default=DEFAULT_HOST, help="the device's ssh alias (default %s)" % DEFAULT_HOST)


def die(msg, code=1):
    print(msg, file=sys.stderr)
    sys.exit(code)
