# Handoff: meshflash, from the cloud session to the T14

Written 27 Nov 2026 by the cloud Claude Code session that built Phases 1 and 2 and the Phase 3 tools. It is for a
new Claude Code session on the owner's T14, opened in this folder, with no copy of the cloud session's conversation.
The owner may read it too. The cloud session could never reach the T14 or the CardputerZero. You can, but only as far
as the owner allows (section 3). Until they answer, `SPEC.md` and `docs/phase3.md` stand: the owner runs every command.

## 1. Start here

Read in this order:

1. `CLAUDE.md` (it loads by itself): the rules and the commands.
2. This file.
3. `SPEC.md`: §11 (the owner's answers, with dates), §12 (the phases) and "Changes to this spec" at the end. The
   25 Nov Phase 3 answers on who runs what aren't in §11: see §12, the last "Changes" line and section 3 here.
4. `docs/phase3.md`: the device runbook. Phase 3 is stopped after its steps 1 and 2 were handed to the owner.
5. The kit, `~/Projects/cardputer-app-kit`: `docs/standards.md` (S1–S38 and the list "Before handing an app to the
   owner") and `docs/device.md` (the device as checked on 18 Nov 2026).

When you need them: `docs/phase2-design.md` (how the screens were built; §18.9 lists the lock's device checks),
`docs/meshcore-flashing.md` (every protocol fact with its source), the docstrings of `scripts/device/*.py` (what each
script reads and writes), and the `doc.go` of `catalog`, `esp`, `nrfdfu`, `nodecli` and `usbid` under `internal/`
(their departures from the reference). UNVERIFIED notes also sit in code comments, for example
`internal/screen/backlight.go`, `internal/ui/lock.go`, `internal/ui/frame.go` and `internal/flash/backend.go`.

Your first message to the owner:

- [ ] Say you have read this file, and where Phase 3 stands: steps 1 and 2 were given on 25 Nov; no results yet.
- [ ] Before asking about steps 1 and 2, look for `dist/phase3/check.log` in this checkout (reading files is fine).
      If it's there, read it and say what it settles. `make test`'s output isn't saved, so ask for its last lines.
- [ ] Ask with AskUserQuestion, in one call: whether you may run commands, and which (section 3); whether they have
      run steps 1 and 2; and the CLAUDE.md question (section 8, item 2). Run nothing before the answers.
- [ ] Keep the other open questions (section 8) for when they matter.
- [ ] Change no app code until the device facts are in. A script that fails on its first real run may need a fix
      sooner (section 2).

Once you may run git: `git status` should name the branch `claude/new-session-lde1l2`, and `git log --oneline -3`
should show the commit that adds this file on top of `cb1f4af`. If it doesn't, see section 9.

## 2. Where things stand (27 Nov 2026)

| Phase | When | Commits | State |
| --- | --- | --- | --- |
| 1. Protocols, against fakes | 24 Nov | `d28f9d3` … `c02d032` | Done and reported |
| 2. Screens, against fakes | 24–25 Nov | `ad7a0a0` … `0f52db4` | Done; the report's questions answered 25 Nov |
| 3. The device, with the owner | from 25 Nov | `8dc6b71`, `cb1f4af` | Begun: tools and runbook; nothing run yet |

- **Phase 2's close.** The owner's answers to the Phase 2 report are in SPEC §11 ("The Phase 2 report's questions,
  answered 25 Nov 2026"). They approved the icon (`packaging/meshflash_100.png`, S33) and the other details as built.
  Only one answer changed the code: Tab in a Setup box opens the next row (`f27530d`). `0f52db4` added the Phase 2
  commands to CLAUDE.md, with the owner's approval.
- **`8dc6b71` "scripts/device: Phase 3 tools the owner runs from the T14".** It adds `scripts/device/` and the make
  targets `device-check`, `device-run`, `device-shot`, `device-mem`, `device-logs` and `device-tools`. They ran end
  to end only in the cloud session, against ssh and scp stand-ins and the demo build (`screendrive.py --local`).
  **They have never run against the real device, or over real ssh.** Expect first-run problems. Read a failure's
  whole message. A fix to a script is a commit like any other; anything on the device is the owner's to change.
- **`cb1f4af` "docs: the Phase 3 runbook".** It adds `docs/phase3.md`, the README's "On the device", a CHANGELOG line
  and SPEC §12's list of the targets.
- **The owner's next move.** On 25 Nov the owner was given steps 1 and 2: on the T14, `git fetch origin
  claude/new-session-lde1l2`, `git checkout claude/new-session-lde1l2`, `git pull` and `make test`; then
  `make device-check`. They have not pasted any results.
- **27 Nov.** The owner asked "How do I connect you to the t14". They were told: Remote Control, or a session on the
  T14 itself, and that Claude would ask before running anything there, starting with the two read-only steps,
  `make test` and `make device-check`. That promise is now yours. Then they asked for this handoff and a zip.
- **The local run** (cloud session, demo build, stand-in font): 15 frames right, the lock dark at 5.1 s, both exits 0
  at 3.04 s with the picture left blank, VmHWM 38.5 MB. Not committed. The Phase 2 PC run's figure, 38.4 MB, is in
  `docs/phase2-design.md` §19.

## 3. How the owner works

- **Ask first** (CLAUDE.md, the kit's S36). The owner wants clarifying questions, not guesses. Ask before anything
  that changes what they'll see, the keys, what gets saved, or anything hard to undo. Ask multiple choice, with the
  recommended option first and "(Recommended)" in its label.
- **Ask with the AskUserQuestion tool**, never as a numbered list in a message. On 24 Nov the Phase 1 report listed
  its questions as text; the owner replied "Ask me questions in chat", and every round since went through the tool,
  up to four questions a call. Reports still list "Decided without asking" items as text.
- **Record answers** in SPEC §11 with their date. (The 25 Nov Phase 3 round went into §12 instead; see below.)
- **One phase at a time.** Phase 3 ends with a short report, and then you stop.
- **The code wins.** Where SPEC.md and the code disagree, fix SPEC.md, add a dated line under "Changes to this
  spec", and say so in the report.
- **Commits** (CLAUDE.md). One concern each, with the reason in the message. Push `claude/new-session-lde1l2`; no
  pull request. Update CHANGELOG.md for anything the owner would notice, and README.md for installing or using it.
  End commits with the attribution lines your own session gives; earlier commits carry `Co-Authored-By` and
  `Claude-Session` trailers.
- **The cloud session's own practice**, not a rule of the owner's but worth keeping: no model names in code, docs or
  commit message text, and none of the owner's personal details (email, addresses, IPs) in anything committed. The
  kit's `docs/device.md` holds the device's addresses; don't copy them into this repository.

### Who runs what

On 25 Nov, after the Phase 2 report set out Phase 3, the owner wrote: "You want to ssh into the cardputer? Can we
just build this on the t14 and copy it to the cardputer, and you can write scripts for me to run and put the output
here". Then they chose, each time the recommended option:

- The scripts live in the repository and run from the T14 ("In the repo, run from the T14").
- Results come back as pasted text and attached PNGs ("Paste text, attach PNGs").
- "I run everything": nothing runs on their machines unless they start it. They declined connecting the T14 to the
  cloud session through Remote Control. On 27 Nov they asked how to connect it (section 2), so ask again.

You run on the T14, so you could run commands yourself. Don't assume you may. Ask it with AskUserQuestion, with
options like these:

> May I run commands on the T14 for Phase 3?
> 1. The read-only checks: git status, log and pull and `make test` on the T14, and `make device-check`, which logs
>    in to the Cardputer over ssh and only reads (Recommended)
> 2. Those, plus the read-only reads when a step asks for them: `make device-shot`, `device-mem`, `device-logs`
> 3. Those in 2, plus the steps that write to the device without sudo (`make deploy`, `device-tools`, `device-run`),
>    each only after you say go
> 4. None: I run everything and paste the results, as before

Until the owner answers, SPEC §11's "Device phase" row, §12 and phase3.md stand: the owner runs every command. Once
they answer, record it in SPEC §11 with its date, in a Phase 3 table that also holds the three 25 Nov answers above.
If they let you run any commands, also update phase3.md's opening paragraph ("Claude can't reach the device or the
T14") and each step's "Who, where" line, §11's "Device phase" row and §12, with a dated line under "Changes to this
spec". That is a docs commit, not code.

Whatever they answer, these stay the owner's, done by the owner, with the owner watching:

- every `sudo`: step 3 (`usermod`, `reboot`) and step 6 (`make install-desktop`, which asks for the sudo password);
- anything on the LCD (steps 7–9) and any real board (step 9);
- `ssh-copy-id CardPuter`, if the key login isn't set up.

Anything that writes to the device (`make deploy`, `make device-tools`, `make device-run`) runs only after the owner
says go for that step, with them there (CLAUDE.md: `make deploy` is run with the owner).
`make device-run` runs only after the owner has said every board is unplugged from the device and that they will
plug none in until the run has ended.

Before each command you run, say what it does and what it touches. phase3.md has a "Touches" line for steps 1–6 and
9. For steps 7 and 8, take it from the step's text and the script's docstring: `device-shot` reads `/dev/fb0`,
`device-logs` reads meshflash's own files, and `device-tools` writes `~/meshflash-check/keyrate.py` on the device.

## 4. The hard rules

CLAUDE.md's "Hard rules" apply in full, and it loads by itself: read them there. In short: the LCD only with the owner
watching, a real board only in this phase with the owner watching, every `sudo` the owner's, tests with fakes in
virtual time only, never a password stored or logged, only the firmware written, no server changed, one static Go
binary, the same bytes as the web flasher at `45a5257`. What follows from them on the T14:

- Never run the real `meshflash` on the T14: it would open the T14's own serial ports and reach the real servers.
  Use the demo build (`make demo`, `make run-small`).
- The `make device-*` targets keep to the list "What no script ever does" at the top of phase3.md. Keep them that way.

## 5. Phase 3, step by step

None of these steps has run yet. "Run" means whoever section 3's answer says. Steps 3 and 6 are the owner's, and so
is everything done on the device itself in steps 7 to 9. The writes in steps 4, 5 and 8 wait for the owner's go.
Each `make device-*` prints a summary to paste and keeps everything under `dist/phase3/` (git-ignored).
`HOST=CardPuter` is the default.

1. **The T14 checkout.** Run `git pull` on the branch, then `make test`. Collect the last lines of `make test`.
   Settles: the checkout builds and passes on the T14 (S27–S29, S31).
2. **The facts.** Run `make device-check` (it only reads). Collect the summary; the rest is in
   `dist/phase3/check.log`. Settles: the ssh key login, the device-tree model's text and bytes, backlight access,
   groups, ports, battery, Terminus, fb0's size, and whether the three sites are reachable.
3. **dialout.** The owner runs, on the device, `sudo usermod -aG dialout void` and then `sudo reboot`. Collect
   `make device-check` again: "launcher … has dialout: yes". Settles: meshflash, started by the launcher, can open
   serial ports.
4. **Deploy.** Run `make deploy`. Collect the `--version` line. Settles S3, and S34's copy without sudo. The checksum
   match comes from step 5 (or a second `make device-check`: "same as dist/meshflash"), S34's install-desktop from 6.
5. **The run into a file.** Run `make device-run`, with **every board unplugged, and none plugged in until it ends**.
   It takes about a minute and a half. Collect `dist/phase3/run-<time>/summary.txt` and `frames.png`. Settles: VmHWM
   with no board, the catalog over Wi-Fi, the test page's glyphs in Terminus, the screen left blank (S8), and the exit
   after an Esc hold (S6).
6. **Home screen.** The owner runs `make install-desktop` and types the sudo password. Collect "MeshFlash is on the
   home screen." Settles S32 on the device.
7. **The LCD,** with the owner watching. They open MeshFlash; `make device-shot` runs meanwhile. Then the lock, the
   quit by Esc hold, and `make device-logs`. Collect the text and the PNG, what the owner saw, and `screen.log`.
   Settles: whether the backlight goes off or the screen is only drawn black, waking and unlocking, and a clean home
   screen afterwards.
8. **The CLI app.** Run `make device-tools`. The owner runs `meshflash` and `keyrate.py` in the CLI app, then
   `make device-logs` brings `keyrate.txt` back. Collect what they saw, and `keyrate.txt`. Settles: key repeat
   against the 0.5 s rules.
9. **A real board,** with the owner watching. The owner picks the board. Flash it, then run Setup for a repeater
   or room server. Run `make device-mem` before quitting and `make device-logs` after. Collect what each screen
   showed, the shots, the memory and the logs. Settles: section 6's board facts, and VmHWM after a real flash and
   setup.

After each paste: say what it settled. Say what you would change, and ask before any change the owner would see.
After a code change: `make test`, commit and push. Before the next device step, `make deploy` runs again: the owner
runs it, or you do once they say go, if section 3's answer allows it.

Two gaps in the runbook. Raise them with the owner; don't add steps on your own:

- **S7 on the device** (a draft left, an Esc hold, the drafts file read back) needs the Setup form, so a repeater or
  room server. `make device-run` has no board and leaves no draft, so step 9 is the first chance.
- **`docs/phase2-design.md` §18.9** lists lock checks the runbook leaves out:
  - a real write of `brightness` (`echo 0 >`, then back), with the owner watching (1): step 2 only tests access(),
    though step 7's lock makes the same write through meshflash and `screen.log` says whether it worked;
  - the lock on Confirm, on the Setup form and in a Setup text box (2): step 7 tries it only on the Board, and
    Confirm and Setup need a board, so step 9;
  - a flash or setup while locked (3);
  - the Esc hold while locked and dark (4);
  - SIGKILL while dark (5): `kill -9`, the owner's command;
  - the CLI app's own lock (7);
  - nothing dimming during a long flash (8);
  - a held Tab's evdev events in `--screen-test` (10).

### After Phase 3

From the owner's original prompt and SPEC §12:

1. Run the kit's "Before handing an app to the owner" checklist (`docs/standards.md`), each item with its evidence.
2. Give the exact install commands, as README's "Installing" has them: `make build-arm64`, `make deploy
   HOST=CardPuter`, `make install-desktop HOST=CardPuter`, and the `dialout` commands.
3. Say what to try first.
4. Write the Phase 3 report, which is the final report, and stop.

## 6. Things to watch on the device

Each item names where it lives in the code and which step answers it.

- **VmHWM under 40 MB (S9).** The demo measured 38.1–38.5 MB on x86-64 across runs. That's close, but it includes
  the fake world (about 9.5 MB) and a catalog (about 3.8 MB). The soft limit is `debug.SetMemoryLimit(32 << 20)` in
  `internal/app/app.go`. From: step 5's summary, and `make device-mem` in step 9.
- **The text of `/proc/device-tree/model`.** `onZero` (`internal/app/deps.go`) needs it to contain "Raspberry Pi
  Compute Module 0" once the trailing NUL is trimmed. From: step 2, which shows the text and its bytes.
- **Terminus glyphs.** Every character in `screen.Glyphs` (`internal/screen/testpage.go`) must draw, none as "?".
  meshflash draws its own ○ and ✎ if Terminus lacks them (`AddStandIns`, `internal/screen/font.go`). From: step 5's
  test-page check, and step 7 by eye.
- **The backlight.** Can `void` write it, and does the lock's dark screen turn it off or only draw black?
  (`internal/screen/backlight.go`.) From: step 2 (an `access()` test, never a write), step 7 (what the owner sees)
  and `screen.log`.
- **The CLI app's key repeat** against the 0.5 s rules: `lockTabGap` (`internal/ui/lock.go`), `escRepeatGap` and
  `heldYGap` (`internal/ui/frame.go`). From: step 8's `keyrate.txt`. If a held key never repeats there, design §18.9
  item 6 says what to ask the owner.
- **The ESP32-S3's port across a reset.** Does an ESP32-S3 on USB-Serial-JTAG (the Heltec v4) keep its port across
  the reset into its ROM? (`internal/esp`, `internal/usbid/table.go`.) From: step 9 with the Heltec v4.
- **`FirstBootWait` after a full wipe** (20 s now, the same as `nodecli.ConfirmWait`; `internal/flash/backend.go`).
  From: step 9 with `w` on an ESP32, and `screen.log`.
- **ZephCore's reply to `ver`** (`internal/nodecli`). From: step 9, if a board runs ZephCore.
- **The catalog update over the device's Wi-Fi** (`internal/catalog`). From: step 2 (the sites reached) and step 5
  (`u`).
- **`dialout` for the launcher** after `usermod` and a reboot. From: step 3's `make device-check`.

When the device settles one of these, update the "UNVERIFIED" note where it stands: in the code's comments, the
package's `doc.go` and `docs/meshcore-flashing.md`. Add a line under SPEC's "Changes to this spec" if the plan moves.

## 7. Code and tooling notes

- **Go.** Use `export GOTOOLCHAIN=go1.25.4` if the local Go is older. The Makefile sets it by default, but a bare
  `go` command doesn't. The module is `meshflash`, on Go 1.25 with Bubble Tea v1.
- **Never run `go mod tidy`, and never remove a requirement.** `go.mod` pins every module
  (`docs/phase2-design.md` §0).
- **`make test` is the whole check.** It runs the gofmt check, vet (host, linux/arm64, `-tags demo`), `go test
  -race ./...` and the demo's cmd tests. It also checks that the release binary links no `fakeworld`, `testing` or
  `net/http/httptest`. `-race` needs cgo, so the T14 needs a C compiler (gcc). The release binary itself is
  `CGO_ENABLED=0`. On a fresh module cache, Go also fetches the modules `go.sum` pins, once.
- **No real sleeps in tests.** `TestNoRealSleepsInTests` (`internal/app/deps_test.go`) fails any `time.Sleep` in a
  test outside its two named exceptions. Use `serialport.VirtualClock` or the test driver's clock.
- **Goldens.** Every screen has goldens at 53×14, 45×10 and 40×10, in `internal/ui/testdata/<Test>/<W>x<H>.golden`.
  Update them with `go test ./internal/ui -update`, only on purpose, and read the diff. `internal/usbid` has its own
  `-update`.
- **The deployed binary must match.** Go stamps the checkout's VCS state into `dist/meshflash` (`vcs.revision`,
  `vcs.modified`), so its sha256 changes with any commit or uncommitted edit. `make device-run` rebuilds
  `dist/meshflash` first and refuses a deployed copy that differs. Run `make deploy` again after any change.
- **Boundaries.** `internal/serialport` is the only code that touches a tty. `internal/ui` does no network or serial
  I/O. `internal/flash` is the orchestration the screens call.
- **The scripts.** They need Python 3.9 or newer, standard library only. They reach the device over ssh in
  BatchMode with a key login, so there is never a password prompt. `HOST=<alias>` overrides `CardPuter`. To try a
  script change without the device:
  `python3 scripts/device/screendrive.py --local --binary dist/meshflash-demo --font <a 6×12 PSF>`.
- **Two kinds of key.** The LCD's keyboard (evdev) sends real key-ups. The CLI app's terminal sends a held key again
  and again and never its release, which is why the 0.5 s rules exist.
- **The reference.** `make reference` needs Node 22 and Python 3, and clones the flasher into `reference/.flasher`
  (network) unless it is there already. Phase 3 doesn't need it.

## 8. Open questions for the owner

1. **May you run commands, and which ones?** See section 3. Ask this first.
2. **Should the `make device-*` targets go into CLAUDE.md's Commands?** CLAUDE.md is the owner's file. Its Phase 2
   commands went in with their approval (`0f52db4`). The cloud session asked about this on 25 Nov; there's no answer.
3. **Which board to flash first (step 9).** That's the owner's call. Their boards, from SPEC §11: a Heltec v4
   (ESP32-S3, native USB), a RAK WisBlock 4631, several Seeed Xiao nRF52, and a Seeed Wio Tracker L1 Pro (nRF52840).
   They also pick update or full wipe every time; nothing is preselected.
4. **Where the device facts go.** S9 and S37 name a `docs/device.md`, and the checklist names a user guide. meshflash
   has neither: the README covers using it, and the kit's `docs/device.md` belongs to another repository. Ask before
   creating files or touching the kit.
5. **Whether to add the runbook's two gaps** (section 5) to steps 7 and 9.
6. **Notes from the Phase 2 report that the owner hasn't taken up.** They stand unless the owner overrules them:
   - Decided without asking: the Setup form's Tab walk stops on a row whose box or list can't hold its value (this
     one is in SPEC §11); no make target forces the network off for tests.
   - Left as they are: a wait like "15 s." can wrap between the number and the "s"; only Setup has a test for a
     crash mid-job.
   - Only the first item is in the repository. The rest were in the report given in chat.

## 9. If you got this from the zip

- **What's in it.** The project as it sat in the cloud session:
  - the working tree and `.git` (the history, the branch, `origin` set to the GitHub repository);
  - `dist/`: `meshflash` for linux/arm64 and `meshflash-demo` for linux/amd64;
  - `reference/` with its git-ignored `.flasher/`, the web flasher checked out at `45a5257`;
  - `reference/esp32/out/`, git-ignored scratch from re-recording (safe to drop).
  It doesn't include the 59 MB research folder (`/home/user/meshflash-research` in the cloud session).
- **Which copy to work in.** If this session was opened in the unzipped folder and the T14 also has a clone, ask the
  owner (AskUserQuestion) which to use, their clone recommended, and don't work in both. You can't move this session
  to another folder: the owner reopens Claude Code there. That clone has the owner's own git setup and credentials;
  the zip's `.git` came from a cloud container. Once they allow git (section 3), run
  `git fetch origin claude/new-session-lde1l2`, check out the branch and pull, or give them those commands. Copy
  `reference/.flasher` from the zip only if they agree; make rebuilds `dist/`.
- **The remote.** The branch is on the GitHub repository that the zip's `.git/config` names as `origin`. The owner's
  original prompt suggested a Forgejo repository, so if the clone's `git remote -v` names another host, ask which
  remote to fetch from and push to before step 1.
- **If the clone's log lacks this file's commit**, it wasn't pushed in time: copy `docs/handoff.md` from the zip and
  tell the owner.
- **The branch was rewritten on 24 Nov** (SPEC §11, Ripple's release notes) so the old text never appears in its
  history. If `git pull` on the T14 says the branch has diverged, don't merge: ask the owner, and reset to origin's
  branch only with their OK. Before zipping, the cloud session expired the zip copy's reflog and pruned its
  unreachable objects, so the old commit isn't in the zip's `.git`. Even so, never push from it with `--all` or
  `--mirror`.
- **Don't deploy the zipped `dist/meshflash` as it is.** It was built from the commit that adds this file, in a
  clean tree; `go version -m dist/meshflash` shows which. `make build-arm64`, `make deploy` and `make device-run`
  rebuild it on the T14 anyway, and `make demo` rebuilds the demo for the T14.
- **Without the research folder**, the full-size sample tests skip: `samples_test.go` in `internal/catalog`,
  `internal/esp`, `internal/nodecli` and `internal/nrfdfu`, and `internal/usbid`'s table and scan checks, all gated on
  `MESHFLASH_SAMPLES`. `make test` passes without them.
