# meshflash Phase 2: the build plan

The screens, against fakes (`SPEC.md` §12, Phase 2): every screen with goldens at 45×10, 40×10 and 53×14; the whole
flow end to end against a fake board and `httptest`; the icon and the home-screen entry; a run with the screen drawn
into a file on the PC. Written 24 Nov 2026 for five builders working at once on disjoint files, against the contract
files already in the tree, and revised the same day after three reviews (section 17). `SPEC.md` is the plan and wins
over this file; where this file departs from it, section 14 says so. The kit's standards are cited as S1–S38
(`cardputer-app-kit/docs/standards.md`).

## 0. How to use this

- The contract is compiled Go: `go build ./... && go vet ./...` pass with it as it stands (host, `linux/arm64` and
  `-tags demo`). Each builder replaces placeholder bodies in the files they own and adds files of their own (section
  11). Nobody changes a fixed file; a builder who finds one wrong stops and reports it rather than changing it.
- The frame of the screens is fixed and **working** now (`internal/ui/frame.go`, `steps.go`, `editor.go`,
  `text.go`): Update's order, View's layout, the status line, the scans, the battery, Help, the questions, the Job
  bookkeeping and quitting. Builder C fills the screens and builder D fills Setup through the hooks in
  `internal/ui/hooks.go`, and each runs their own goldens and key tests through the real frame while the other's
  methods are still placeholders.
- Placeholders do no I/O, never block and never panic: the store's writes return "not built yet", the backend's
  calls report it, every Job they make is already finished, the screens' hooks draw nothing.
- Each builder's own tests pass on their own once their part is written, except the end-to-end tests (`TestE2E…`),
  which need builder B's backend and world. Integration (section 12) runs everything together.
- Every builder works in their own copy of the tree (section 12), so a half-written file of one never stops another
  compiling.
- Nothing in Phase 2 touches a real board, the real LCD or the network (CLAUDE.md): fakes, `httptest`, the committed
  catalog, virtual time. The Go module proxy is the only network allowed, and `go.mod` already pins every module:
  never run `go mod tidy` and never remove a requirement.
- The owner's answers to section 16 come first: they are asked in chat before the builders start, and the answers go
  into SPEC §11. Each answer is a small change behind one function or constant, and the recommended one is what this
  file and the contract describe.

## 1. Packages

```
cmd/meshflash ──► internal/app ──► internal/ui ──► internal/flash (types, Job, rules) ──► catalog, esp, nrfdfu,
     │                  │               │                                            nodecli, usbid, serialport
     │                  │               ├──► internal/store (types)
     │                  │               └──► catalog, nodecli, usbid (types and pure functions only)
     │                  ├──► internal/flash (New, Deps) and internal/screen (Run, Battery's sysfs folder)
     │                  └──► internal/store (OpenApp)
     └── demo.go (-tags demo) ──► internal/flash/fakeworld ──► flash, catalog, esp, nrfdfu, nodecli, serialport
```

| Package | What | Builder |
| --- | --- | --- |
| `internal/store` | `config.json` (last radio sent, board picked per USB serial) and `drafts.json` (setup form per board, no passwords), 0600 in 0700, atomic and flushed | A |
| `internal/flash` | the orchestration: `Backend` (scan, probe, catalog, downloads, prepare, flash, read node, setup), `Job`, the shared rules (`rules.go`); the only Phase 2 package doing serial I/O, through `Deps.Open` and `Deps.Scan` | B |
| `internal/flash/fakeworld` | the fake world: boards built from the Phase 1 fakes, the flasher sites, battery, clock; `Demo` for the demo build | B |
| `internal/ui` | all screens as one Bubble Tea v1 model; no serial or network I/O | frame: fixed; C (all screens but Setup); D (Setup) |
| `internal/app` | flags, wiring, SIGTERM and SIGHUP, `screen.log`, color profile, memory limit, shutdown | E |
| `cmd/meshflash` | `main`, `Version`; `demo.go` under `-tags demo` | E |
| `packaging/` | `meshflash.desktop.in`, `make_icon.py` (icon A), `meshflash_100.png` | E |
| `Makefile` | `build-arm64`, `deploy`, `install-desktop`, `run-small`, `icon`, `demo`, and `test` extended | E |

`internal/screen` and the Phase 1 packages are not changed (owner question 17 asks about one small addition to
`usbid`). `internal/ui` imports `flash` for types, `Job` and the pure rules, `store` for types, and `catalog`,
`nodecli`, `usbid` for types and pure functions (`catalog.Choose`, `nodecli.Check`, `nodecli.TxLimit`,
`nodecli.Presets`, `usbid.SameBoard`, …): nothing that opens a port or a socket.

## 2. The contract files

| File | Status | What it fixes |
| --- | --- | --- |
| `internal/store/types.go` | fixed | `Config`, `Radio`, `Draft` (no password field; `Cleared`), `ErrNoKey`, file names |
| `internal/store/store.go` | signatures fixed, bodies A's | `OpenApp`, `New`, `ConfigDir`, `CacheDir`, `LoadConfig`, `RememberBoard`, `SaveRadio`, `LoadDraft`, `SaveDraft`, `DeleteDraft` |
| `internal/flash/types.go` | fixed | `Deps`, `Port`, `Scan`, `Problem`, `Node`, `Request`, `Prepared` (with `Ask`), `SetupRequest`, `JobKind`, `StepState`, `Step`, `Risk`, `Progress` (with `Risk`), `Outcome`, `Sent` |
| `internal/flash/job.go` | fixed, complete | `Job`: `NewJob`, `ID`, `Kind`, `Set`, `Finish`, `Snapshot`, `Next` (returns at once once `Done`), `Cancel`, `Done` |
| `internal/flash/rules.go` | fixed, complete | `PortOf`, `Port.DraftKey`, `Port.InBootloader`, `CanFlash`, `Fits`: the rules both B and C need |
| `internal/flash/backend.go` | signatures fixed, bodies B's (B may add unexported fields) | `Backend`, `New`, the 14 methods of `ui.Backend`, `BoardBackWait`, `FreshBootloaderWait`, `FirstBootWait`; the rules every body keeps |
| `internal/flash/fakeworld/world.go` | exported API fixed (B may add), bodies B's | `World`, `New`, `Deps`, `DepsOver`, `Clock`, `Wire`, `Handler`, `Requests`, `SetOffline`, `FailPath`, `SetBattery`, `SetBatteryUnreadable`, `SetClockSynced`, `SetDialout`, `SetMessenger`, `At`, `Plug`, `Unplug`, `Problems`; `Board`, `Firmware`, `HeltecV4`, `HeltecV3`, `RAK4631`, `XiaoNRF52` and the board methods; `Demo`; `Sites` |
| `internal/ui/backend.go` | fixed | `Backend` (and where each call is made), `Store`, `Options` (with `Name`) |
| `internal/ui/state.go` | fixed | `screen`, `confirmKind` and their constants, `Model`'s fields |
| `internal/ui/hooks.go` | fixed | every hook between the frame, C's screens and D's Setup, checked at compile time, and the rules for both |
| `internal/ui/frame.go` | fixed, complete | `New`, `Init`, `Update`, `View`, `SaveUnsent`, `Shutdown`, `Running`, `SavedRadio`; the status line, scans, battery, Help, questions, Job bookkeeping, `quitNow`, `toBoard`, `watch`, `canStart`, `ask`, `rescan`, `listed`, `after`, `now`, `size`, `logf`, `wrapIndent` |
| `internal/ui/steps.go` | fixed, complete | `stepLines`, `fitSteps` and the step marks |
| `internal/ui/editor.go` | fixed, complete | the kit's text box with a byte limit and `Clear`: `editor`, `newEditor`, `newBytesEditor`, `window` |
| `internal/ui/text.go` | fixed | the kit's `width`, `cut`, `fitHint`, `wrap`, `plain`, `center`, `ago`, `clock`, `plural`, `elapsed`; added `heading`, `indented`, `scrolled`, `byteSize`, `took`, `day` |
| `internal/ui/model.go` | C's; the hook signatures fixed | C's hooks (placeholders), `flow`, C's screen state types |
| `internal/ui/setup.go` | D's; the hook signatures fixed | D's hooks (placeholders), `setupScreen` |
| `internal/ui/driver_test.go` | fixed, complete | the test driver: `TestMain`, `sizes`, `eachSize`, `testNow`, `newDriver`, `newWorldDriver`, `newWorldDriverOver`, `send`, `run`, `keys`, `typeText`, `do`, `advance`, `settle`, `hold`, `heldCmds`, `release`, `releaseAll`, `stopJobs`, `timersSet`, `view`, `logText`, `check`, `noSecret`, `testCatalog` |
| `internal/ui/fakebackend_test.go` | fixed, complete | `fakeBackend` and its fields, `newJob`, `finishedJob`, `portOf` (`flash.PortOf`) and the owner's boards as ports (`heltecV4Port`, `rakPort`, `rakBootloaderPort`, `rakBootloaderUnconfirmedPort`, `xiaoPort`, `cp2102Port`, `ch340Port`) |
| `internal/app/app.go` | `Program`, `Fake`, `Env`, `Main`'s signature fixed; bodies E's | and the compile-time checks that `*flash.Backend` is a `ui.Backend` and `*store.Store` a `ui.Store` |
| `cmd/meshflash/main.go`, `demo.go` | E's; `Version` and the `name`/`fake` wiring fixed | |

## 3. The backend

### 3.1 The calls

`ui.Backend` (`internal/ui/backend.go`) is what the screens may ask; `*flash.Backend` implements it. Two rules say
where each call is made:

- `Download`, `Flash` and `Setup` start a Job and return it at once, with no I/O before they return (the board lock,
  every wait and every open are on the Job's own goroutine). The screens call them **in Update**, only when
  `canStart()`, with `context.Background()`, and give the Job to `watch` in the same Update. So a second `y`, a
  held `y` or a Ctrl+C can never find a Job started but not yet watched. A Job stops only through `Job.Cancel`.
- Every other call is made **inside a `tea.Cmd`**, its arguments captured before the Cmd is returned (the kit's
  pattern, which keeps `-race` clean). View reads only what a message stored.

| Call | Where | Kind | When the screens make it |
| --- | --- | --- | --- |
| `Scan() (flash.Scan, error)` | Cmd (frame) | sysfs only, opens nothing | at start; every second on every screen; `r`; `toBoard` |
| `Probe(ctx, Port) flash.Node` | Cmd | board, ~0.3–2 s | on Board, for each port that appears (bootloaders too: the backend answers those without opening them); again on `r` |
| `LoadCatalog()` | Cmd | disk, ~1 s on the Zero | at start; after an update |
| `UpdateCatalog(ctx)` | Cmd | network | `u` (Board); `u` again on a failed update |
| `SavedFiles()` | Cmd | disk | Version and Saved files open; after a download or delete |
| `DeleteFile(name)` | Cmd | disk | `y` to Saved files' "Delete …?" |
| `Download(ctx, files) *Job` | Update | network | `d` on Version; `y` on Confirm when the file isn't saved |
| `Prepare(Request) Prepared` | Cmd | disk, hashes the file | Confirm opens; `u`/`w` pick the kind; after a download |
| `Flash(ctx, Request) *Job` | Update | board | `y` on Confirm, once every question is answered |
| `ReadNode(ctx, Port, firstBoot) Node` | Cmd | board, ~1 s (up to 20 s first boot) | Setup opens |
| `Setup(ctx, SetupRequest) *Job` | Update | board | `y` on the Setup form |
| `Battery()` | Cmd | local | every 30 s (frame, while the status line shows it); in Confirm's Prepare Cmd; at `y` on Confirm |
| `ClockSynced()`, `MessengerRadio()` | Cmd | local, a file | the Setup form opens (with ReadNode) |

The store is not behind `Backend`: its writes are small local files the screens make from Update, as the kit does,
through the `ui.Store` interface (`*store.Store`). Tests use a real store in temporary folders, so they can read the
bytes of `drafts.json` and `config.json`.

### 3.2 Jobs

A download, a flash and a setup each return a `*flash.Job` at once and run on the backend's goroutine. A Job is
seen as whole pictures (`flash.Progress`: every step so far, its bytes and times, the `Risk` of stopping now, and at
the end the `Outcome`), never as a stream of events:

- The worker calls `Set` and `Finish`, which never block, so esp's and nrfdfu's progress callbacks (on the flashing
  goroutine, which must not block: backend brief rule 14) call `Set` directly. Nothing queues; a slow screen never
  slows a flash.
- The screens ask for the next picture with `Options.Wait(j, seen)`: by default a Cmd blocking in `Job.Next`, which
  returns at once for a finished Job whatever `seen` is. The `flash.Progress` it returns is itself the `tea.Msg`. The
  frame ignores a picture whose `Job` isn't `m.job.ID()` (stale), re-arms the wait until `Done`, and at `Done` lets
  the Job go, closes Help and drops a stop or quit question (S16), then hands the picture to the screen.
- Every Job finishes exactly once, however it ends (an error, `Cancel`, a recovered panic).
- At most one Job runs (`m.job`). `watch` refuses a second (it cancels it and logs: a bug), but the screens never
  start one: they check `canStart()` first.
- The running step's time is `Progress.Now − Step.Started` (the backend's clock) plus how long ago, on the screens'
  clock, the picture arrived (`m.now() − m.jobSeenAt`), so it moves between pictures (an erase with no progress for
  10 s) and is deterministic in tests, where the two clocks are separate (brief P9).

### 3.3 Types the screens read

- `flash.Port`: the `usbid.Port`, its `Kind`, `Label` (bridge chip or product), `Candidates`, and `Key`: the serial
  in one form, or `""` when it can't tell boards apart (none, all zeros, a CP210x's "0001"). `flash.PortOf` builds
  every one, in the backend and in the screens' test fake alike. Boards are remembered by `Key` only; drafts are kept
  by `DraftKey` (owner question 13).
- `flash.Node`: `Problem` (`NoProblem`, `Silent`, `Stuck`, `NotSettable`, `InBootloader`, `NoPermission`, `Busy`,
  `Gone`, `Failed`) and `Reason` (also set for `InBootloader` when it needs a double press first); `Info`; `Current`
  settings when read. The screens switch on `Problem`, never on error values.
- `flash.Prepared`: Choose's pick, whether the file is saved and still matches its SHA-256, a `Problem` in plain
  words, `Notes`, and `Ask` (a second question before flashing: owner question 20).
- `flash.Progress.Risk`: what stopping now leaves the board with (`RiskNone`, `RiskErased`, `RiskWritten`,
  `RiskSettings`, `RiskRadioSent`), so "Stop now?" says what is true.
- `flash.Outcome`: `Title`, `Summary`, `State`, `Recovery` (all plain words the screens show as they are), `Retry`,
  `Setup`, `Board` (where the board is now), `Entries`, `Sent`, `Log`. All the protocol knowledge of what a failure
  leaves the board in lives in builder B's code; the screens never translate a protocol error.
- `flash.CanFlash(d)` and `flash.Fits(d, port)`: whether meshflash can flash a board at all, and whether the board on
  a port can be that board (the chip family), with a sentence and a short tag. Choose board, Prepare and Flash use
  the same functions.

## 4. internal/flash (builder B)

### 4.1 Rules

Everything in the backend brief's section 2 (`map-backend.md`) and `backend.go`'s rules, with these points:

- **One board operation at a time.** A mutex around Probe, ReadNode, Flash and Setup, taken on the Job's goroutine
  for Flash and Setup. The screens cancel a probe before they flash or set up; the backend waits for it to let go
  (≤100 ms after cancel). Every port is closed and the lock released before `Finish` (or before Probe and ReadNode
  return).
- **Every open** passes `Baud` (115200, `nrfdfu.DFUBaud` for DFU) and `Exclusive: true` (section 13). Only
  `nrfdfu.Touch` opens at 1200.
- **Back to 115200 after a touch.** Linux keeps a tty minor's speed after close, so a touched minor stays at 1200:
  the next program that opens and closes it without setting a speed (the owner's messenger, a terminal) reboots a
  companion board into DFU. Every Flash that made a touch ends, whatever the outcome (a cancel too), with a step
  "Back to 115200 baud": if a port now has the touched tty's name, open it at 115200, exclusive, send nothing, close
  it. The same for the app port found after STOP when the firmware has no console (the repeater check already opens
  it at 115200).
- **Find the board again** at the start of every Flash and Setup: the `Request.Port` is what Board showed, maybe
  minutes ago. Rescan and pick the port `usbid.SameBoard` says is it; none: "Not flashed", "The board isn't plugged
  in", nothing sent.
- **The family before anything** (`flash.Fits`), in Prepare and again in Flash, before any open: an nRF52 board needs
  an nRF52 app or bootloader port; an ESP32 board an ESP32 port or an unknown one (the ROM's chip read still guards
  that); an nRF52 board on an unknown port is refused with "press reset twice". A refusal is `Prepared.Problem`, or
  Outcome "Not flashed" with the sentence and "Nothing was sent to the board: it still has its old firmware."
- **Match before touch**, every time (also in Flash after Prepare): `catalog.Choose` → `client.Saved` →
  `client.ReadVerified` (re-check the entry's URL against the file's URL or fallback) → ESP32 `esp.ImageChip` and
  the board's chip (`usbid.DeviceChip`), nRF52 `nrfdfu.OpenPackage` and `CheckSoftDevice(Device.SoftDevice)` on the
  very `*Package` flashed. Write exactly the bytes `ReadVerified` returned.
- **A flash never downloads.** A file not saved, or changed since Prepare, is "Not flashed": "The file isn't saved;
  press y on Confirm to download it." (A test checks `world.Requests()` is unchanged across a Flash.)
- **ESP32-C6** (the two ZephCore-only boards): `CanFlash` refuses them before any download (owner question 8);
  `esp.Flash` refuses the chip anyway.
- **DFU only to a positively identified bootloader** (`Port.InBootloader`: `NRFBootloader` and the interface string
  "nRF Serial"), and **never START into a stale session** (section 4.4).
- **Probes** never open a bootloader port: they answer `InBootloader` with `Reason` "" when `InBootloader()` and the
  session isn't stale, "Its last flash stopped part-way: …" (4.4) when it is, and "Looks like its bootloader, but
  meshflash can't confirm it: press reset twice quickly." when the port is a bootloader by VID:PID only. A probe
  sends only `ver`, `get role`, `board` (and the read-only `get`s for the name, owner question 1) and clears
  `Current.GuestPassword` before it returns.
- **ZephCore is never set up.** A `ver` reply naming ZephCore (case-insensitive "zeph" in `Info.Version.Raw`) is
  `NotSettable`, Reason "ZephCore: meshflash sets up only MeshCore's own repeater and room-server firmware.", in
  Probe and ReadNode. `Outcome.Setup` is set only when `Request.Firmware.Source == catalog.SourceMeshCore`, its Role is
  `repeater` or `roomServer`, and the board answered as one after the flash. (Phase 3 records a real ZephCore `ver`.)
- **Risk**: `RiskNone` until the first erase or write (an ESP32's first FLASH_BEGIN, an nRF52's START), `RiskErased`
  until the image is written (MD5 checked, STOP acknowledged or `MayHaveWorked`), then `RiskWritten`. A setup is
  `RiskNone` until the node saves its first setting, `RiskSettings` after, `RiskRadioSent` once `set radio` is
  answered.
- **Long waits say so** in the running step's `Note`: "Stopping once the erase ends (about 8 s)" after a cancel
  during the nRF52 erase; "No answer: sending again (2 of 3)" on a resent frame; "attempt 2 of 4" on ESP32 connects.
- **Words**: `State` is one short sentence that `Recovery` never repeats (where esp's or nrfdfu's Recovery starts with
  the state, that sentence becomes `State` and the rest `Recovery`). The Result screen adds "Press r to try again:
  the file is saved, so no Wi-Fi is needed." when `Retry`; Recovery doesn't say it.
- **Passwords**: `Step.Text` is `nodecli.Step.Display`; never `Step.Command`, never the `serialport.Log`, never
  `%#v` of a `Settings`. Drop `SetupRequest.Settings.AdminPassword` (and the guest password) from the backend's
  memory once `Run` returns.
- **Log** (`Deps.Logf`, `Outcome.Log`): which step and why, in the words of nodecli's `StepError.Reason` or
  `Progress.Note`, or esp's and nrfdfu's error text; never a raw reply from the node (the `password` step's reply
  is "password now: <pw>", a failed read-back of `get guest.password` echoes the guest password), a `Step.Command`, a
  `serialport.Log`, or a `Settings`/`SetupRequest` printed with `string(Secret)`. One line per failure and one per
  Outcome.

### 4.2 Steps and outcomes

The step texts are what Flashing and Setup show; keep them short (they wrap at 40 columns). A step may change its
Text when it ends ("Erasing" → "Erased"). Targets:

| Flash of | Steps |
| --- | --- |
| ESP32 | "Into the bootloader" (Note "attempt 2 of 4"), "Flasher stub loaded (ESP32-S3)", wipe only "Erased the whole chip", "Writing" (bytes), "Checked: MD5 matches", "Restarted" |
| ESP32-S3 TinyUSB | as ESP32, with "Into the bootloader" covering the re-enumeration to the ROM's port (`map-backend.md` 1.4 step 6) |
| nRF52 | "Into the bootloader (1200-baud touch)" (or "Already in its bootloader"), "Erasing" (`Expect` = `nrfdfu.EraseWait`), "Writing" (bytes), "The board checked the CRC", "Back on USB (ttyACM0)", MeshCore repeater/room server only "Running v1.18.0", then "Back to 115200 baud" when a touch was made |
| nRF52 on a stale session (4.4) | first "Press the board's reset button twice quickly" (waits up to `FreshBootloaderWait` for the bootloader port to go and come back), then as nRF52 from "Erasing" |

Outcomes, by what happened (B writes each, with a test; the screens show them as they are):

| What happened | Title | State | Recovery | Retry | Setup |
| --- | --- | --- | --- | --- | --- |
| ESP32 written, MD5 matches, restarted | Flashed | | | | MeshCore repeater/room server |
| nRF52 STOP acknowledged, back on USB, `ver` names the version (repeater/room server) | Flashed | | | | yes |
| nRF52 back on USB, firmware with no console (companion, KISS, ZephCore, Ripple) | Flashed (owner question 7) | | | | |
| nRF52 repeater/room server back on USB (also after `MayHaveWorked`) but no `ver` answer within `ConfirmWait` | Flashed, not checked | "It came back on USB but didn't answer as a repeater." | "Press reset once; if it still doesn't answer, flash it again." | yes | |
| Refused before the board was touched (`Fits`, Choose, wrong chip, SoftDevice, damaged file, not saved) | Not flashed | "Nothing was sent to the board: it still has its old firmware." | the reason | | |
| Board not found at the start | Not flashed | the same | "Plug it in and press r." | yes | |
| ESP32 no bootloader | Not flashed | "Flash untouched." | the rest of `FlashError.Recovery()` (BOOT/RESET) | yes | |
| ESP32 unplugged or silent while writing | Flashing stopped | "Partially written: …" | the rest of `Recovery()` | yes | |
| ESP32 MD5 mismatch | Flashing stopped | "Written, but the flash doesn't match the file." | the rest of `Recovery()` | yes | |
| nRF52 touch did nothing (no bootloader in 10 s) | Not flashed | "Nothing was sent: it still has its old firmware." | "Press reset twice quickly, then press r." | yes | |
| nRF52 failure past START | Flashing stopped | "Its app is erased." | "Press r; meshflash then asks you to press reset twice quickly." (owner question 17 (a); with (b) "Press reset twice quickly, then press r.") | yes | |
| nRF52 STOP sent, ACK lost (`MayHaveWorked`) and the app came back | Flashed | | | | as above |
| nRF52 `MayHaveWorked` and nothing came back in 15 s | Flashing stopped | "Most likely written, but the board didn't come back." | `Recovery()` | yes | |
| `ver` names another version after flashing | Flashing stopped | "It runs vX, not the version just flashed: the bootloader most likely refused the image." | "Press reset twice quickly, then flash again." | yes | |
| Fresh bootloader never came (stale session, 60 s) | Not flashed | "Nothing more was sent: its app is still erased." | "Press reset twice quickly, then press r." | yes | |
| Owner stopped it (Esc, y; or a quit) | Flashing stopped (`Stopped`) | from where it stopped, as above | as above | yes | |

Setup outcomes: OK "Set up" with `Summary` ("Name, radio, TX power … sent and read back.", "It restarted and runs
v1.18.0 on 869.618 MHz."), `Sent`; a `*nodecli.StepError` gives "Setup stopped", `Summary` its `Display` and `Reason`,
`State` its `State` ("nothing changed", "the settings before this step saved", "everything saved but not
rebooted", "still on its old radio until it restarts"), `Retry` true; after the reboot, `ErrChanged` gives "Set up, but
it came back different" with what differs.

### 4.3 Tests (package `flash_test`: `fakeworld` imports `flash`, so an internal test can't import it)

Each path of 4.2 against the world, asserting the Outcome's fields, the steps' `Risk`, and that `world.Problems()`
is empty: ESP32 update and wipe (Heltec v4), the TinyUSB re-enumeration, the bridge board (Heltec v3); nRF52 RAK 4631
with the touch, the bootloader on the same and on a new tty name, a board already in its bootloader (no touch),
`drop-ack`, `unplug-at`, a SoftDevice refused before the touch, `MayHaveWorked`, a wrong version after STOP, came back
but silent; the touched tty back at 115200 (touch did nothing; the bootloader on a new tty name; a companion app
coming back); probes (repeater, room server, companion silent, stuck, no permission, busy, gone, a bootloader not
opened, an unconfirmed bootloader, ZephCore `NotSettable`: `TestProbeZephCoreNotSettable`); ReadNode; Setup OK,
failing at a step (each `State`), failing at the `password` step with a wrong reply holding the password and at `get
guest.password` (no Logf line, Outcome or Step holds either password), unplugged mid-setup; downloads (progress, an
HTML answer refused, the GitHub fallback, a ZephCore file), each route once over `DepsOver` (httptest on loopback:
SPEC §10's stand-in for S29) as well as in memory; Prepare's refusals; `TestPrepareRefusesOtherFamily` (both ways,
no open recorded, `Problems` empty); `TestFlashNeverFetches`; the stale session (4.4: `TestFlashRetryNeedsFreshBootloader`,
a double press through `World.At`); Job (`-race`: many `Set`s, `Next` never misses `Done`, `Next` after `Done`
returns at once, `Finish` twice); one board operation at a time; `TestPortOfAgreesWithSameBoard` (`PortOf`'s Key
rule against `usbid.SameBoard`); no password in any Step, Outcome, Log line or error. No real sleeps: everything on the
world's `VirtualClock`. The catalog is resolved once for the package; `withCatalog` only where loading it is what's
tested (it costs about a second under `-race`).

### 4.4 nRF52: never START into a stale session

The Adafruit bootloader keeps one DFU session: START sent into a session that already had START (after a failure past
START, a DFU cancelled by Esc or Ctrl+C, an Esc hold or SIGTERM, a `MayHaveWorked` flash that never came back) breaks
research 10.4 rule 2. So:

1. `Backend` keeps `stale map[string]staleDFU` by `Port.DraftKey` (an nRF52's Key: its serial). It is set the moment
   START is written to a bootloader session, and cleared when that board's app is seen on USB again (any scan, the
   screens' included: they all go through `Backend.Scan`), or when its bootloader port is seen gone and then back
   after the record.
2. Flash on a recorded board never uses the bootloader port as it is: it runs the step "Press the board's reset
   button twice quickly", then waits (polling `Deps.Scan` every 100 ms on the clock) first for the port to go and
   then to come back, within `FreshBootloaderWait`; Esc stops it. None in time: "Not flashed" (4.2).
3. Probe on a recorded board answers `InBootloader` with Reason "Its last flash stopped part-way: meshflash asks you
   to press reset twice quickly when you flash it." (owner question 17 (a); with (b): "…press reset twice quickly,
   then flash again."). The Board screen shows the Reason when it is set.
4. A double press made before `r`: with owner question 17 (a, no Phase 1 change), the Result tells the owner to press
   `r` first and the wait in step 2 runs whenever the record stands; a double press the every-second scans happened
   to see (gone, then back) has already cleared it, and one they missed costs a second double press when asked. With
   (b), `usbid.Port` gains `DevNum` (sysfs `devnum`, new on every enumeration; an additive Phase 1 change) and the
   record keeps the bootloader's DevNum at START, so the same Key with another DevNum is a fresh session and step 2 is
   skipped; the Result then says "Press reset twice quickly, then press r."
5. Across a restart (owner question 18): (a) the backend notes each bootloader port it has seen appear in this run
   (absent in one scan, present in a later one); a bootloader already present at the first scan is treated as
   stale; (b) the record is kept in `config.json` instead.
6. The world reports "START sent into a bootloader session that already had START". Tests:
   `TestFlashRetryNeedsFreshBootloader` (B) and `TestE2ERetryAfterStopped` (C: `NextBootloader("mute-from:20")`,
   Result, `r`, `y` with no `DoublePressReset`: no START sent, the step reads "Press the board's reset button twice
   quickly"; then a `DoublePressReset` through `World.At`, and it flashes).

## 5. internal/flash/fakeworld (builder B)

- **Boards** are composites of the Phase 1 fakes behind `serialport.Fake` ports, as `map-backend.md` section 3 lays
  out: the identities of the owner's boards (`internal/usbid/fakesys_test.go`), each mode's `Device` (nRF52 app =
  `FakeApp` + `FakeNode`; bootloader = a fresh `FakeBootloader` per session; ESP32 = `FakeESP` + `FakeNode` switched on
  `FakeESP.Mode()`), the transitions (touch → bootloader after ~1 s, same tty unless a minor is held; STOP with a good
  image → the app ~1 s later running the image's marker; a refused INIT → Bluetooth DFU, gone until
  `DoublePressReset`; an ESP hard reset → the app at 0x10000's marker; a node reboot → re-enumerating boards drop and
  return their port).
- **One ticker.** The world registers exactly one `VirtualClock.OnTick` in `New`, which runs every transition,
  `At` event and re-enumeration from the world's state (`OnTick` can't remove a ticker); `Demo` one more for pacing.
- **Real-looking time.** The fake ESP32 moves the clock on per `FLASH_DEFL_DATA` block (its bytes at about 11.5 KB/s)
  and per chip erase (about 10 s), so step times and the demo's progress look like a real board's.
- **Each tty's speed** is kept as Linux keeps it (a close leaves it; a close at 1200 on an nRF52 app's tty reboots it
  into DFU), for the "Back to 115200 baud" rule.
- **Firmware** is made up per file name on request (`Handler`): ≥ 52 KB, an ESP32 image header naming the chip of the
  catalog board that lists the file (the merged image with the app at 0x10000), or a DFU `.zip` with an init packet
  for that board's SoftDevice; each with the marker `MESHFLASH-FAKE <role> <version>`. A `Firmware{Role: "repeater",
  Version: "1.18.2-zephcore"}` console answers like MeshCore's with that version, for the ZephCore rule.
- **Sites**: one `http.Handler` under `/mc`, `/zc`, `/gh`, serving a copy of the five committed catalog files
  (`internal/catalog/testdata/{meshcore,zephcore}-*.json.gz`, copied into `fakeworld/catalog/` and embedded; a test
  checks the copies are byte for byte the originals). `Deps` uses an in-memory `http.Client` (a `RoundTripper` over
  the handler: no socket at all); `DepsOver` an `httptest.Server`. `SetOffline` and `FailPath` make it fail.
- **Events**: `At(d, f)` runs f once on the world's ticker after d of virtual time: how a test acts during a wait. Tests
  act on the world only through `At` or the event-based `UnplugWhileWriting` and `UnplugAfterCommands`, never from
  their own goroutine while a Job runs.
- **Problems** collects every rule broken (the list in `world.go`: a port left open, an open without baud or
  `Exclusive`, a probe opening a bootloader or sending more than `ver`/`get`/`board`, a touch on a non-nRF52-app port,
  SLIP to an nRF52, DFU to an app, START outside the bootloader or into a used session, FLASH_BEGIN or START with no
  flash job, a tty left at 1200, a password outside the password commands). Each problem names the rule and the
  command's place, never its bytes or a password: tests print them.
- **Battery**: `SetBattery` (negative: no battery), `SetBatteryUnreadable` (there, but unreadable: `(-1, true)`).
- **Demo**: a Heltec v4 running repeater v1.17.1 named “Hilltop 2” and a RAK 4631 running companion firmware, the
  catalog saved under the demo's cache folder if none is, battery 87%, the clock synchronised, and pacing (a ticker
  that sleeps a fraction of each virtual advance in real time) so a flash takes about half a minute on the screen.
  Only the demo build calls it.

## 6. internal/store (builder A)

The kit's `store.go` in shape (`map-kit-app.md` section 2). `Config` and `Draft` as `types.go` defines them;
`OpenApp(name)` uses `os.UserConfigDir()/<name>` and `os.UserCacheDir()/<name>`. Every write: `MkdirAll` 0700, temp
file in the same folder, `Chmod` 0600, write, `Sync`, `Close`, `Rename`, then fsync the folder (as
`catalog.writeAtomic` does). Every change re-reads its file under the store's mutex. `LoadConfig`: missing or damaged
(syntax error, wrong type, truncated) is the zero `Config` and no error; unreadable is an error the app logs and starts
without. A board name or radio isn't checked here: the screens ignore a remembered board no longer in the catalog and
a saved radio `nodecli.CheckRadio` refuses. Draft keys are any non-empty string (`flash.Port.DraftKey`: a serial, or
"usb 1-1.2 10c4:ea60").

Tests: `TestConfigIsPrivate` (0600 files, 0700 folder), `TestADamagedConfigIsNone` (`""`, cut-short JSON, `[1, 2]`,
wrong types; then a save over it), `TestUnreadableConfig` (chmod 0, skipped as root), `TestRememberBoard` (merges,
keeps the radio, empty serial is `ErrNoKey`, empty device forgets), `TestSaveRadio`, `TestDrafts` (save, load, replace,
delete, unreadable file replaced, other keys kept, a "usb …" key, `Cleared` round-trips), `TestEachAppKeepsItsOwnFiles`
(XDG folders, "meshflash" and "meshflash-demo"), `TestConcurrentChanges` (`-race`, many goroutines),
`TestDraftHasNoPasswordField` (reflect: no field whose name or JSON key contains "pass").

## 7. internal/app, cmd/meshflash, packaging, Makefile (builder E)

### 7.1 internal/app

The kit's `app.go` (`map-kit-app.md` section 1), with:

- `Main`: the kit's flags without `--server`; `--version` prints `<Name> <Version>`. Reshape into `run(args []string,
  stdin io.Reader, stdout, stderr io.Writer) int`, called by `Main`, and split what tests need into parts:
  `openScreenLog(cacheDir, name, version)`, `deps(p Program, env Env) (flash.Deps, error)`, and `finish(...)` (the
  shutdown below). Only `TestVersionFlag` calls `run` itself.
- **Real dependencies only outside tests.** `deps` calls `p.Fake(env)` when set; otherwise `realDeps`, the one function
  that builds `serialport.Open`, `func() { return usbid.Scan("") }`, `usbid.DialoutStatus`, `serialport.RealClock`,
  `catalog.NewClient(st.CacheDir(), p.Version, catalog.Options{})`, the battery, `ClockSynced` from `unix.Adjtimex`
  (synced when the state isn't `TIME_ERROR` and `Status&STA_UNSYNC == 0`) and `nodecli.MessengerPath()`. `realDeps`
  panics under `testing.Testing()`. Every test of the package sets `Program.Fake` (a fakeworld or stub Deps whose
  `Open` fails the test): `TestNoRealDepsInTests`.
- **The battery**: its own wrapper over the power-supply folder (`screen.PowerSupplies`), not `screen.Battery`'s
  cached string: `(0, false)` only when no power supply of type `Battery` exists (a PC); present but unreadable is
  `(-1, true)`, and a failure is never cached. The screens then ask "Battery level unknown. Flash anyway?".
- **screen.log** (`~/.cache/<Name>/screen.log`, 0600, truncated, first line `<Name> <Version> started <RFC3339>`)
  opened on every run, in the terminal too (section 13), wrapped once in a locked writer that is given as
  `ui.Options.Log`, as the base of `flash.Deps.Logf`, and as `Env.Log`.
- `run`: `debug.SetMemoryLimit(32 << 20)`, absorb SIGTERM and SIGHUP (the CLI app's terminal closing sends SIGHUP;
  absorbed, Bubble Tea's input ends instead and `run` reaches its shutdown), `store.OpenApp(p.Name)`, `LoadConfig` (an
  error is a line in screen.log, not a stop), `lipgloss.SetColorProfile(colorProfile())` before `ui.New`, then the
  deps, `ui.Options{Name: p.Name, Version: p.Version, ShowBattery: so != nil, …}`, then `screen.Run` or
  `tea.NewProgram(m, tea.WithAltScreen(), tea.WithInput(stdin))`. The demo build refuses `--screen` without
  `--framebuffer` (it must never draw on the real LCD by accident).
- **After Run** (`finish`): when Run ended normally (Ctrl+C and y, Esc on Board, an Esc hold, SIGTERM), `fm.Shutdown()`
  on the final `*ui.Model`; if it returns a Job, wait for `j.Done()` at most `shutdownWait` (1.5 s, a variable tests
  shorten), then write its Outcome's `Title`, `State` and `Recovery` to screen.log, or "still stopping" (with "its app
  is erased" when the last picture's `Risk` is `RiskErased`). When Run ended by a panic or a lost terminal, owner
  question 19: (a) `fm.SaveUnsent()`, then `fm.Running()` is left to finish, bounded at about 3 min, and its Outcome
  logged; (b) as a normal end. Either way, a `JobSetup` that is `Done` with `Outcome.Sent` set has its radio saved
  (`st.SaveRadio(ui.SavedRadio(*o.Sent))`): the node took it even if the screens never saw the end. The whole shutdown
  is bounded: after the signals are absorbed only SIGKILL ends a hang, and the launcher sends it 3 s after its
  SIGTERM.
- Tests: `TestColorProfile` and `TestASIGTERMAfterQuittingIsAbsorbed` (the kit's; env `MESHFLASH_SIGTERM_CHILD`; its
  200 ms sleep is in a child process, not serial code), `TestScreenLogFreshEachRun` (`openScreenLog`),
  `TestVersionFlag`, `TestShutdownIsBounded` (`finish` with a Job that never finishes; `shutdownWait` shortened),
  `TestShutdownSavesSentRadio`, `TestDemoDepsWired` (a `Fake` given is used), `TestNoRealDepsInTests`,
  `TestBatteryUnreadable` (a fake power-supply folder).

### 7.2 cmd/meshflash

`main.go` as it stands (`Version = "0.1.0"`, `name`, `fake`). `demo.go` (`//go:build demo`) sets `name =
"meshflash-demo"` and `fake` to `fakeworld.Demo`: the demo binary has its own folders (S2) and never reads or writes
the real `~/.config/meshflash`. `packaging_test.go`: `TestHomeScreenEntry` (the kit's, one entry: `Name=MeshFlash`,
`Exec=/home/void/.local/bin/meshflash --screen` at most 512 characters with none of `` |&;<>`$\ ``, `Terminal=false`,
`Icon=share/images/meshflash_100.png`, `Type=Application`, first line `[Desktop Entry]`, no key twice) and `TestIcon`
(`png.DecodeConfig` is 100×100).

### 7.3 packaging/

`meshflash.desktop.in` (`Exec=@BIN@ --screen`); `make_icon.py`, the kit's conventions with concept A's shapes
(`map-kit-app.md` 5.2: teal tile, antenna, green board, header pins, chip, lightning bolt), writing
`meshflash_100.png` next to itself. The PNG is shown to the owner before it is committed (S33, SPEC 9.2).

### 7.4 Makefile

`build-arm64`, `deploy`, `install-desktop`, `run-small` as `map-kit-app.md` section 7, but `run-small` runs the demo
build (`$(GO) run -tags demo ./cmd/meshflash`): the real binary on a PC would probe its real serial ports and reach the
real servers. Add `icon` (`python3 packaging/make_icon.py`), `demo` (`go build -tags demo -o dist/meshflash-demo
./cmd/meshflash`), and to `test`: `$(GO) vet -tags demo ./...`, and a check that the release binary links no fake
(`$(GO) list -deps ./cmd/meshflash | grep -q fakeworld` fails the target). Update the header comment.

## 8. internal/ui: the frame (fixed) and the model's rules

### 8.1 Update, in this order (frame.go)

1. `tea.WindowSizeMsg`: store `w`, `h`; nothing else.
2. `tea.KeyMsg`: note whether it is a held `y` (8.8); Ctrl+C (quit, or `confirmQuit` while a flash or setup Job runs:
   owner question 16); then `m.note = ""`; then Help if open (j/k/↑/↓ `scrollStep` lines, Space/b a page, any other
   key closes it); then an open question (any key but `y` says no and does nothing else; a held `y` leaves it open;
   `confirmQuit`'s `y` quits; the rest go to `setupConfirm` on Setup, `screenConfirm` elsewhere); then `?` opens Help
   unless a text box is open (`setupEditing`, `screenTextBox`); then `setupKey` on Setup, `screenKey` elsewhere.
3. `flash.Progress`: section 3.2.
4. The frame's scans and battery readings.
5. `screenMsg` (builder C's messages), then `setupResult` (builder D's).

### 8.2 View (frame.go, the kit's layout)

Line 1 the status line; then the body, padded or cut from the bottom to `max(h−2−len(message), 1)` lines; then the
message (the question while asking, else `wrap(m.note, w)`); the last line `cut(hint, w)`, the hint `"y yes  any other
key no"` while asking. Too many lines drop the top ones. `m.quit` draws nothing (S8). Each screen's view returns
`(body, hint)` for exactly `w×h`, fitting its own body (8.9) and picking its hint with `fitHint` from the candidates in
section 9 (all measured to fit 40 columns). Body text is always wrapped (`wrap`, `indented`), never cut (S11); only
one-line labels are cut with "…". Style after wrapping, line by line: `grey` for secondary text, `heading()` (reverse
video) for group headings (owner question 12), `reverse` for a text box's cursor; the selected row starts `▸ ` (the
SPEC mock-ups; others `  `). `m.note` is one short sentence: anything longer goes in the body.

### 8.3 The status line (S13; frame.go)

Title left, cut to fit (Help's is "Help"); on the right the battery on the LCD (`Options.ShowBattery` and a reading of
0–100: `"87% ●"`; read every 30 s in a Cmd, never in View) and the board's mark:

- `…` while `m.busy > 0` (a probe, ReadNode, a flash or setup Job: board work only; a catalog fetch or download shows
  its own elapsed time instead);
- else, on a screen about one board, `●` while that board is listed in the last scan (`listed`: `usbid.SameBoard`, or
  the same tty, identity and socket for a port SameBoard can't follow) and `○` when it isn't or is gone: Board's
  selected port; `flow.port` from Choose board to Flashing; the Outcome's `Board` on Result (nil: `○`, the mock's
  "Flashing stopped … ○"); on Setup its node's port, then its Outcome's `Board`;
- on a screen about no board (Update catalog, Saved files, Board with no port), `●` when any port is listed, `○` when
  none is.

The frame scans sysfs every second on every screen (`Backend.Scan`, which opens nothing: S23 doesn't apply), so the
mark follows the board with no key pressed (S16). Titles: Board `m.name`; Choose "Which board is on ttyACM0?"; Role
"<board>"; Version "<role> · <board>"; Notes "Notes · <version>"; Confirm "Flash this?"; Download "Downloading <role>
<version>"; Flashing "Flashing <role> <version>"; Result the Outcome's `Title`; Setup `setupTitle()`; Update "Update
the catalog"; Files "Saved files". A role is named `Title`, plus " · " and `SubTitle` when there is one ("Companion ·
Bluetooth"); never by key.

### 8.4 Ticks (S17)

Builder C: `m.after(time.Second, tickMsg{gen})` while something timed is on screen (probing, Prepare pending, an
update, a download, a flash), re-armed on each tick only while it still is, the generation dropping old ticks. The
tick only redraws; the view computes every time from the clocks (3.2). Builder D runs its own ticks for Setup (its own
message type, `setup…`).

### 8.5 Scans and probes

The frame scans every second (8.3) and calls `afterScan(prev)` (C). Builder C, on Board and Choose board only: a port
is keyed by tty name and identity; each new one is probed (all of them: the backend answers bootloaders without
opening them), one probe at a time in a queue, each with a context the model cancels when the port goes, when the
owner leaves Board for anything but Choose board, at `cancelProbes` (before a flash or setup, at a quit). A port that
went away drops its lines at once (S16); an answer for a port no longer listed is dropped. `r` rescans (`rescan`) and
probes every port again. With no catalog, ports are still listed and probed. On Confirm, `afterScan` notices
`flow.port` gone (8.9, 9.6). Tests advance the driver's clock a second at a time to see each scan.

### 8.6 Help (S19; frame.go)

`?` on any screen (outside a text box) opens Help over it: `heading("Keys")`, the screen's keys (`setupHelp` or
`screenHelp`: "key" padded to 15 runes, then what it does, wrapped by `wrapIndent`), Ctrl+C, then the footer, each its
own entry so `wrap` never joins them:

```
Ctrl+C         quit, from anywhere (asks first while flashing or setting up)

<Name> <Version> for CardputerZero
Catalog and files: flasher.meshcore.io, zephcore.meshcore.dev; GitHub when a MeshCore file isn't on the flasher site. A catalog update downloads about 1.5 MB.
Looks for boards on USB every second, reading only what Linux lists; it opens a port only to ask a board on the Board screen what it runs.
Keeps its settings in ~/.config/<Name> and its catalog, files and screen.log in ~/.cache/<Name>.
Reads the messenger's radio from ~/.config/mesh/config.json; never writes it.
Serial ports need the dialout group. Run these once:
<Scan.Hint's lines, else SPEC 9.3's two: sudo usermod -aG dialout void / sudo reboot>
```

j/k scroll `scrollStep` lines (2: owner question 15), Space/b a page. Hint: `"j/k scroll  space page  any other key
back"` (42), at 40 `"j/k scroll  space page  other keys back"` (39). The Help hint has no "? help" (TestHintsFit
exempts it). `TestHelpNamesTheProgram` runs with `Name: "meshflash-demo"`.

### 8.7 Shutdown and quitting (frame.go)

`quitNow` (Ctrl+C, Ctrl+C then `y`, Esc on Board): `SaveUnsent`, `cancelProbes`, cancel the Job, `m.quit = true`,
`tea.Quit`. `Shutdown` (called by the app after Run, however the program ended): `SaveUnsent`, `closeSetup`,
`cancelProbes`, cancel the Job and return it. `Running` returns the Job without stopping it (owner question 19). Esc
held 3 s on the LCD and the launcher's SIGTERM reach the model only as one Esc press (which on Flashing opens "Stop
now?", and in a Setup text box cancels it: 10.6) and then the end of Run: what follows is the app's (7.1; owner
question 9).

### 8.8 A held y (owner question 5)

The LCD's keyboard repeats a held key (every 33 ms after 250 ms) and so does the CLI app's terminal. The frame marks a
`y` that comes less than `heldYGap` (0.5 s) after the previous key, itself a `y`, as held (`m.yHeld`): it answers no
question (the question stays) and starts nothing. Builder C ignores it on Confirm's `y`, builder D on Setup's `y`.
Release and press again. Confirm also ignores `y` for 0.5 s after it reappears from a download, and until that
download's new Prepared has arrived. Test on the driver's clock: `y`, then `y` every 33 ms for 2 s, nothing flashed;
0.6 s with no key, `y`, and it flashes once.

### 8.9 Bodies longer than the screen

At 40×10 there are eight body lines, fewer with a message. So:

1. **Step lists** (Flashing, Download, Setup's sending and end) use `fitSteps`: the running or failed step always in
   view, the oldest done steps folded into "✓ 3 earlier steps"; the screen keeps its header ("<board> on ttyACM0")
   and its footer ("Don't unplug the board.") outside the lines it gives `fitSteps`.
2. **Result and Setup's done and failed views** scroll with j/k (`scrollStep`) and Space/b, with `State`, `Recovery`
   and the retry line pinned at the bottom: the Summary scrolls in the space above them (when they alone don't fit,
   everything scrolls). Their hint gains "j/k more" through `fitHint` when the body doesn't fit.
3. **The Setup form** scrolls to keep the focused row and its problem in view (`window`).
4. **Lists** (Board's ports, Choose board, Role, Version, Saved files) scroll to keep the selection in view.

Goldens: `TestResultUnpluggedESP32`, `TestFlashingNRF52Retry`, `TestSetupSendingScrolled`, `TestSetupFormScrolled`,
`TestSetupFailedLate`, `TestResultScrolls`.

## 9. The screens (builder C)

The state machine (SPEC §3 and §5; every screen also has `?` Help and Ctrl+C):

| From | Key or event | To |
| --- | --- | --- |
| Board | `enter` on a port (needs a catalog) | Choose board |
| Board | `u` | Update catalog |
| Board | `s` on a port whose probe gave `NoProblem` | Setup (`cancelProbes`, then `openSetup(port, remembered board, false)`) |
| Board | `c` | Saved files |
| Board | `r` | Board (rescan, probe again) |
| Board | `esc` | quit |
| Choose board | `enter` on a board that `Fits` the port (remembered for the port's `Key`) | Role |
| Choose board | `f` | the find box (on Choose board) |
| Choose board | `esc` | Board |
| Role | `enter` | Version |
| Role | `esc` | Choose board |
| Version | `enter` | Confirm |
| Version | `n` | Notes |
| Version | `d` | Download, then back to Version |
| Version | `esc` | Role |
| Notes | `esc` | Version |
| Confirm | `u`, `w` (ESP32) | Confirm (kind picked, Prepare again) |
| Confirm | `y`, file not saved | Download, then back to Confirm (owner presses `y` again) |
| Confirm | `y`, saved, battery under 30% or unknown | the low-battery question; `y` → (the `Ask` question, if any; `y` →) Flashing |
| Confirm | `y`, saved, `Prepared.Ask` | the `Ask` question; `y` → Flashing |
| Confirm | `y`, saved | Flashing |
| Confirm | `esc` | Version |
| Download | done, failed, or `esc` (stops it) | where it came from; on failure the Summary shown in that screen's body |
| Flashing | the Job's `Done` | Result, in the same Update (S16) |
| Flashing | `esc` → `y` | the Job stops → Result |
| Flashing | Ctrl+C → `y` | the Job stops, meshflash quits |
| Result | `s` (Outcome.Setup) | Setup (`openSetup(*Outcome.Board, board, true)`) |
| Result | `r` (Outcome.Retry) | Confirm (owner question 4) |
| Result | `enter`, `esc` | Board |
| Setup | (builder D's, section 10) | Board |
| Update catalog | `esc` while fetching (stops it), `enter`/`esc` after | Board, the catalog reloaded on success |
| Saved files | `x` → `y` | Saved files (the file deleted) |
| Saved files | `esc` | Board |

Each screen below gives its state (builder C's type in `state.go`'s field), messages, keys (S18), hints at 45 and 40
columns (S12), Help entries (S19), S16/S17, goldens and failure states. Goldens are
`internal/ui/testdata/<Test>/{45x10,40x10,53x14}.golden` through `eachSize` and `check` (S10, S28); goldens use the
fake backend and `testCatalog`. States with an answer still out are captured with the driver's `hold` (section 15).
The mock-ups in SPEC §4 are the targets, "close to, not exactly".

### 9.1 Board

- **State** `boardScreen`: the ports (from `m.scan`) with each one's `Node` (or probing since when, or queued), the
  selection, the probe queue and its cancel.
- **Messages**: `probedMsg{key, node}`, `catalogMsg{cat, err}`, `tickMsg`; the frame's scans through `afterScan`.
- **Body**: per port, `"▸ " + tty + " · " + cut(Label, room) + " (" + VIDPID + ")"` so only the label is cut (Label
  "": `"▸ ttyACM0 (1a86:7523)"`); under it, indented and wrapped: "Could be: A, B, C." (≤ 3 candidates, catalog
  names) or "Could be one of 27 boards." (> 3) or nothing; then by `Node.Problem`: `NoProblem` "Running repeater
  v1.17.1, “Hilltop 2”." (the name when `HasCurrent`: owner question 1); `Silent` nothing; probing "Asking it what it
  runs… (0:01)"; queued "Waiting to ask it…"; `Stuck` "It echoes but doesn't answer: press its reset button (or unplug
  it if it has no battery) and press r."; `NotSettable` "Running <role> <version>: <Reason>"; `InBootloader` "In its
  bootloader, ready to flash." when Reason is "", else the Reason; `Busy` "Another program has this port open.";
  `NoPermission` the dialout block below; `Failed` "Couldn't ask it: <Reason>."; `Gone` dropped with the port. A scan
  error: "Can't read the USB ports: <err>." first. The list scrolls to keep the selection in view. The last body line:
  "Catalog from 24 Nov: newest v1.18.0" (newest MeshCore community version), "No catalog saved. Join Wi-Fi and press
  u.", or "The saved catalog is damaged. Join Wi-Fi and press u."
- **No board**: "Plug a LoRa board into the USB-A port." **No permission** (any probe `NoPermission`, or no port
  opened yet and `Scan.Dialout` false): "Serial ports need the dialout group. Run these once:" and `Scan.Hint`'s lines
  (else SPEC 9.3's two), each on its own line, never wrapped together.
- **Keys**: `enter` choose (no catalog: note "Press u to fetch the catalog first."); `u`; `s` (not `NoProblem`: note
  "s sets up a repeater or room server; this board didn't answer as one."); `c`; `r`; `j`/`k`/↑/↓; `esc` quits.
- **Hints**: `"enter choose  u update  c files  ? help"` (39); a settable port selected `"enter choose  s set up  u
  update  ? help"` (40); no ports `"r rescan  u update  c files  ? help"` (35); no catalog `"u update  c files  r
  rescan  ? help"` (35).
- **Help**: enter, u, s, c, r, j/k, esc (quit).
- **S16**: a port's lines go the moment a scan drops it; "Asking…" goes when its answer comes; the dialout block goes
  once a probe opens a port. **S17**: "Asking it what it runs… (0:01)", the `…` mark.
- **Goldens**: `TestBoardNoBoard`, `TestBoardNoCatalog`, `TestBoardDamagedCatalog`, `TestBoardNoPermission`,
  `TestBoardProbed` (the mock: a Xiao running repeater v1.17.1 “Hilltop 2” and a CP2102), `TestBoardManyMatches` (a
  Heltec v4: "Could be one of 27 boards."), `TestBoardProbing` (held), `TestBoardQueued` (held), `TestBoardStuck`,
  `TestBoardBootloader`, `TestBoardBootloaderStale`, `TestBoardBootloaderUnconfirmed`, `TestBoardBusy`,
  `TestBoardNotSettable`, `TestBoardZephCoreNoSetup`, `TestBoardProbeFailed`, `TestBoardScanError`, `TestBoardScrolls`.

### 9.2 Choose board

- **State** `chooseScreen`: the rows (headings and boards), the selection, whether the owner has moved it, the find
  text and whether its box is open (`editor`).
- **Rows** (SPEC §4 and the owner's answer): `heading("The board says: <Info.Board>")` then the catalog board of that
  name (`cat.Find`), if any; `heading("USB 303a:1001 matches:")` ("also matches:" after a board the node named) and
  the boards the USB table is sure of (`!Unverified`), catalog order, the named one left out; `heading("Might also be
  (unverified):")` and the rest of the candidates; `heading("All boards:")` and every other catalog board, catalog
  order. A board that doesn't `Fits` the port (download only, ESP32-C6, the other chip family) is grey, its name then "
  · " and the short tag ("Raspberry Pi Pico W · download only", "RAK WisBlock 4631 · nRF52"), and Enter on one gives
  the `why` sentence as the note instead.
- **While the probe runs** (it carries on from Board): "Asking it what it runs… (0:01)" first. When the answer comes,
  "The board says:" is added at the top and the cursor stays on the same board; it moves to the named board only if
  the owner hasn't moved it.
- **Cursor**: on the board remembered for the port's `Key` (`m.cfg.Boards`) when it is listed; else on the only match
  when the node's board and the sure matches come to exactly one; else on the first board. Never chosen for the owner.
- **Keys**: `enter` pick (remember it: `store.RememberBoard(Key, name)` and `m.cfg`, when `Key != ""`); `f` find: a
  one-line box (`newEditor`) filtering every group as typed (case-insensitive, by name), Enter keeps the filter, Esc
  clears it; `j`/`k`; `esc` back.
- **Hints**: `"j/k move  enter pick  f find  ? help"` (36); the box open `"enter keep  esc clear"`.
- **Help**: j/k, enter, f, esc; inside the box: Enter keep, Esc clear, Backspace.
- **Goldens**: `TestChooseBoardSays` (Heltec v4 saying "Heltec V4"), `TestChooseRemembered`, `TestChooseOnlyMatch`
  (a RAK 4631), `TestChooseFind` (box open, "xiao" typed), `TestChooseFindNoMatch`, `TestChooseUnknown` (a CH340: "All
  boards" only, the nRF52 boards grey), `TestChooseDownloadOnly` (Enter on a Pico W: the note), `TestChooseC6`,
  `TestChooseGreysOtherFamily` (a Xiao port: the ESP32 boards grey), `TestChooseProbing` (held, then the answer).

### 9.3 Role

- **State** `roleScreen`: the rows (headings, roles), the selection, the scroll offset.
- **Body**: the selected role's notices first (`Firmware.Notices`: plain text, links already dropped, the T1000-E
  LoRaWAN warning included; grey; wrapped, never cut), then the roles in three groups under `heading()`: "MeshCore",
  "ZephCore · third-party port", "Ripple · closed source, third-party". The body scrolls to keep the selection
  visible, the notices scrolling off the top as it moves down.
- **Keys**: `enter` pick, `j`/`k`, `esc` back. **Hint** `"j/k move  enter pick  esc back  ? help"` (38).
- **Goldens**: `TestRoleGroups` (Seeed Studio Wio Tracker L1 Pro: MeshCore, ZephCore and Ripple),
  `TestRoleT1000ENotice`, `TestRoleZephCoreOnly` (Seeed Studio Xiao C6, reached only in this test: Choose refuses it).

### 9.4 Version

- **State** `versionScreen`: the selection, the saved files (`SavedFiles` when it opens and after a download), a
  download's failure Summary (shown first in the body until the next key).
- **Rows**: `▸ v1.18.0` and, padded to a column (not through `wrap`: brief P5), "saved · 361 KB" when the file is
  saved; for an ESP32 version per owner question 2. Newest first, as the catalog gives them. A saved file matches by
  `Entry.Name == File.Base()` and `Entry.URL` one of the file's `URL` and `Fallback`.
- **Keys**: `enter` Confirm; `n` Notes; `d` download only (owner question 3 for ESP32; already saved: note "Saved
  already, 24 Nov."); `j`/`k`; `esc` back. **Hint** `"enter next  n notes  d download  ? help"` (39).
- **Goldens**: `TestVersionSaved` (Xiao nRF52 WIO, repeater, v1.18.0 saved), `TestVersionESP32Saved`.

### 9.5 Notes

- Release notes (`Version.Notes`) through `plain`, wrapped line by line, a list item ("* ", "- ") with a hanging
  indent; long URLs break. "No release notes." when empty (Ripple's are blanked). `j`/`k` `scrollStep` lines,
  Space/`b` a page, `esc` back. **Hint** `"j/k scroll  space page  esc back  ? help"` (40). **Goldens**: `TestNotes` (a
  long one, scrolled), `TestNotesLink` (v1.18.0's single link), `TestNotesEmpty`.

### 9.6 Confirm

- **State** `confirmScreen`: the latest `preparedMsg` and its generation, whether a `y`'s battery reading is out (and
  its generation), when it (re)appeared, a download's failure Summary.
- **Messages**: `preparedMsg{gen, req flash.Request, p flash.Prepared, battery int, hasBattery bool}` (Prepare and a
  battery reading, in one Cmd), `yBatteryMsg{gen, percent, ok}` (the reading at `y`), `tickMsg`.
- **Races**: every Prepare (Confirm opening, `u`, `w`, after a download) bumps the generation. A `preparedMsg` that
  isn't the latest generation, or whose `req` (Device.Name, Version.Name, Kind) differs from `flow`, is dropped. `y`
  does nothing until the latest one has arrived, and the Request flashed is that message's `req`, refused (logged)
  unless `p.Flash.Kind == flow.kind`. So the screen never shows one kind and writes another (`TestConfirmKindRace`:
  two answers released in reverse order; shown and flashed kind both Wipe).
- **Body** (the mock), in order: "<board> on ttyACM0 (repeater 1.17.1)" (the probe, when it answered; after `r` from a
  failed flash `flow.node` is dropped and the Outcome's `State` is shown on the next line instead); the file line,
  saved "Repeater v1.18.0 · 361 KB · update" (or "· full wipe"), not saved "Repeater v1.18.0 · update · not saved yet"
  (sizes show once known: the catalog has none), no kind yet "Repeater v1.18.0"; what the kind does: update "Keeps
  identity, contacts and pairing.", full wipe "Erases the whole chip: identity, contacts and pairing go.", none yet
  "Press u to update or w for a full wipe." (ESP32 only; nRF52 is always an update); "File saved 24 Nov, SHA-256
  97539d20… ✓" (Prepared.Saved), or "Not saved yet: y downloads it first (Wi-Fi needed).", or while Prepare is out
  "Checking the file… (0:01)"; `Prepared.Notes`; a failed download's Summary; under 30% "Battery 24%. Flashing takes
  about a minute; plug the Zero in if you can." (unknown: "The battery level can't be read."); when `flow.port` isn't
  listed, "The board isn't plugged in now." (and `y` does nothing). A `Prepared.Problem` replaces the file check line
  and `y` does nothing.
- **Keys**: `u`, `w` (ESP32; on nRF52 note "An nRF52 has only an update."); `y`; `esc` back. `y` with no kind on an
  ESP32: note "Press u to update or w for a full wipe first." `y` held (8.8), while a `y`'s reading is out, or before
  the latest Prepared: nothing. `y` when saved: a Cmd reads the battery (`yBatteryMsg`), then: under 30% or unknown
  (percent < 0) → `confirmLowBattery`; else `Prepared.Ask` → `confirmUnchecked`; else Flash. `y` to the low-battery
  question → `Ask`'s question if set, else Flash. Flash is called in Update (`canStart`, `watch`).
- **Hints**: no kind `"u update  w full wipe  esc back  ? help"` (39); saved `"y flash  u update  w full wipe  ? help"`
  (38); not saved `"y download  u update  w full wipe  ? help"` (41), at 40 `"y download  u update  w wipe  ? help"`;
  nRF52 `"y flash  esc back  ? help"`, `"y download  esc back  ? help"`; refused, preparing or board gone `"esc back  ?
  help"`.
- **Questions**: `confirmLowBattery` "Battery 24%. Flash anyway?" / "Battery level unknown. Flash anyway?", asked only
  when `y` would flash (not when it downloads); no battery (a PC), no question. `confirmUnchecked`: `Prepared.Ask`.
- **Goldens**: `TestConfirmESP32NoKind`, `TestConfirmESP32Update`, `TestConfirmESP32Wipe`, `TestConfirmNRF52`,
  `TestConfirmNotSaved`, `TestConfirmPreparing` (held), `TestConfirmNoProbe`, `TestConfirmNotes`, `TestConfirmLowBattery`
  (24%, the warning), `TestConfirmLowBatteryAsks` (the question), `TestConfirmBatteryUnknownAsks`, `TestConfirmUnchecked`,
  `TestConfirmRefused` (a SoftDevice mismatch), `TestConfirmBoardGone`, `TestConfirmAfterFailedFlash` (`r`: State
  shown, kind cleared). Tests: `TestConfirmKindRace`, `TestConfirmYWaitsForPrepare`, `TestEnterTwiceFlashesOnce`,
  `TestHeldYFlashesNothing`, `TestConfirmYAfterDownloadWaits`.

### 9.7 Download

- Title "Downloading <role> <version>"; the steps through `fitSteps`: per file its name and "212 of 361 KB · 59%
  (0:12)" (or "212 KB so far" with no total), then "From flasher.meshcore.io" or "From GitHub (the flasher site didn't
  have it)"; "Wi-Fi needed.". `esc` stops it (no question: nothing saved changes). Done: back where it came from
  (Confirm runs Prepare again and shows the SHA-256; Version shows "saved"). Failed: back, with the Outcome's Summary
  shown in that screen's body ("Couldn't download: no network, or the server didn't answer."). **Hint** `"esc stop  ?
  help"`. **Goldens**: `TestDownloadProgress`, `TestDownloadUnknownSize`, `TestDownloadGitHub`, `TestDownloadFailed`
  (the Confirm it returns to). S16: `TestDownloadLeavesAtDone`.

### 9.8 Flashing

- **Body** (the mock): "<board> on ttyACM0"; `fitSteps(picture, w, room, m.now()−m.jobSeenAt)`: "✓ Into the
  bootloader (1200-baud touch)", "✓ Erased (10 s)", "▸ Writing 212 of 361 KB · 59% (0:38)"; then "Don't unplug the
  board.". Once stopping: "Stopping…" and the running step's Note in its place.
- **Keys**: `esc` → `confirmStop`, worded by the latest picture's `Risk`: `RiskNone` "Stop now? Nothing has been
  written yet: the board keeps its firmware.", `RiskErased` "Stop now? The board will have no working firmware until
  it's flashed again.", `RiskWritten` "Stop now? It's already written; meshflash just won't check it."; Ctrl+C →
  `confirmQuit` (the same, "…and meshflash quits."). Nothing else. **Hint** `"esc stop  ? help"`; stopping `"? help"`.
- **S16**: the picture with `Done` moves to Result in the same Update; "Don't unplug the board.", "Stopping…", Help and
  the question, if open, go with it (frame). **S17**: the running step's time and bytes; an erase shows "(0:04, about
  10 s)" from `Expect`.
- **Goldens**: `TestFlashingWriting` (the mock), `TestFlashingErasing`, `TestFlashingStopAsks`, `TestFlashingStopBeforeWrite`
  (`RiskNone` words), `TestFlashingStopping`, `TestFlashingQuitAsks`, `TestFlashingNRF52Retry` (8 steps at 40×10:
  folded), `TestFlashingConnectAttempt` ("attempt 2 of 4").

### 9.9 Result

- **Body**: the Outcome's `Summary` paragraphs, then `State`, then `Recovery`, each wrapped; when `Retry`, "Press r to
  try again: the file is saved, so no Wi-Fi is needed."; on success with `Setup`, "Press s to set it up." Scrolls as
  8.9 says, with State, Recovery and the retry line pinned.
- **Keys**: `s` (when `Setup`), `r` (when `Retry`: back to Confirm with `flow.node` dropped and the State shown there;
  on an ESP32 the kind is cleared, owner question 4), `enter`/`esc` back to Board, `j`/`k`/Space/`b` when it scrolls.
- **Hints**: `"s set up  enter done  ? help"`, `"enter done  ? help"`, `"r retry  esc back  ? help"`; scrolling `"s set
  up  j/k more  enter done  ? help"` (38), `"r retry  j/k more  esc back  ? help"` (35).
- **Goldens**: `TestResultESP32` (MD5 matches), `TestResultNRF52` (came back running v1.18.0, `s`),
  `TestResultUnplugged` (the mock, `○`), `TestResultUnpluggedESP32` (Summary plus esp's Recovery: 9 lines at 40),
  `TestResultStopped`, `TestResultNotFlashed`, `TestResultMayHaveWorked`, `TestResultNoConsole` (owner question 7),
  `TestResultMD5Mismatch`, `TestResultWrongVersion`, `TestResultScrolls`.

### 9.10 Update catalog

- "Fetching the catalog… (0:03)"; then "72 boards, newest v1.18.0, 1.6 MB" and "(70 MeshCore boards and 2 that only
  ZephCore has)"; on failure "The saved catalog stays." and why: "No network, or the server didn't answer." for
  `catalog.ErrUnreachable`, "flasher.meshcore.io answered HTTP 503." for a `*catalog.FetchError` with a status, the
  error's words otherwise. `esc` while fetching stops it (the saved catalog stays). When the answer comes, Help closes
  (as at a Job's Done). **Hints** fetching `"esc stop  ? help"`, done `"enter done  ? help"`, failed `"u try again  esc
  back  ? help"`. **Goldens**: `TestUpdateFetching` (held), `TestUpdateDone`, `TestUpdateOffline`, `TestUpdateHTTPError`.
  S16: `TestUpdateMessagesKeepUp` ("Fetching…" goes when the answer comes, with no key).

### 9.11 Saved files

- Each file: its name (cut with "…"), then grey and wrapped with `indented` (never cut) "<board> · <role> <version> ·
  361 KB · 24 Nov" (the first catalog board whose version lists it, "and 2 more boards" when several do; just the size
  and date when none does). `x` → `confirmDelete` ("Delete <name>? It downloads again when needed.") → `y` →
  `DeleteFile` and the list again. Empty: "No files saved yet. d on a version downloads one." **Hints** `"j/k move  x
  delete  esc back  ? help"`, empty `"esc back  ? help"`. **Goldens**: `TestFilesList`, `TestFilesEmpty`,
  `TestFilesDeleteAsks`.

### 9.12 Help, messages, S16 and S17 for every screen

A hint is the same at 45 and 40 columns unless a 40-column one is given above: every hint in this file was measured
and fits 40. Help lists the screen's keys (S19), then the footer of 8.6.

| Screen | Messages it takes (besides keys and the frame's) | Help entries | S16: what goes by itself | S17: what shows time |
| --- | --- | --- | --- | --- |
| Board | `probedMsg`, `catalogMsg`, `tickMsg`; scans via `afterScan` | enter choose this port's board; u update the catalog (Wi-Fi, about 1.5 MB); s set up a repeater or room server, no flash; c saved files; r look for boards again; j/k move; esc quit | a port's lines when it goes; "Asking…" when answered; the dialout block once a port opens | "Asking it what it runs… (0:01)", `…` |
| Choose board | `probedMsg`, `tickMsg` | j/k move; enter pick this board; f find by name; esc back | "Asking…" when answered | "Asking it what it runs… (0:01)" |
| Role | (none) | j/k move; enter pick this role; esc back | | |
| Version | `savedFilesMsg`, the download's `flash.Progress` | j/k move; enter flash this version; n release notes; d download only (Wi-Fi); esc back | "saved" appears when a download ends | |
| Notes | (none) | j/k scroll; space/b a page; esc back | | |
| Confirm | `preparedMsg`, `yBatteryMsg`, `tickMsg`, the download's `flash.Progress`; scans via `afterScan` | u update (ESP32: keeps identity, contacts, pairing); w full wipe (ESP32: erases everything); y flash (downloads first if needed); esc back | "Checking the file…" when Prepared comes; "Not saved yet" when the download ends; "isn't plugged in now" when the board comes back | "Checking the file… (0:01)" |
| Download | `flash.Progress`, `tickMsg` | esc stop the download | the screen itself, when the Job ends | bytes, percent, "(0:12)" |
| Flashing | `flash.Progress`, `tickMsg` | esc stop (asks y: says what stopping leaves); Ctrl+C quit (asks y) | the screen, "Don't unplug", Help, the question: all at `Done` | the steps' times and bytes |
| Result | (none) | s set it up; r try again from the saved file; j/k scroll; enter, esc back to the boards | | |
| Update catalog | `updatedMsg`, `catalogMsg`, `tickMsg` | esc stop; u try again; enter back | "Fetching…" when it ends | "Fetching the catalog… (0:03)" |
| Saved files | `savedFilesMsg`, `deletedMsg` | j/k move; x delete (asks y); esc back | the file's row when deleted | |
| Setup (D) | `setup…` messages, the setup's `flash.Progress` | section 10.4 | "Reading…" when read; the steps at `Done` | "Reading the node's settings… (0:02)", the steps' times |

Goldens of Help opened on each screen with keys of its own (S19): `TestHelpBoard`, `TestHelpChoose`, `TestHelpRole`,
`TestHelpVersion`, `TestHelpNotes`, `TestHelpConfirm`, `TestHelpDownload`, `TestHelpFlashing`, `TestHelpResult`,
`TestHelpUpdate`, `TestHelpFiles` (C), `TestHelpSetup` (D), `TestHelpScrolledToTheEnd` (the footer) and
`TestHelpNamesTheProgram` (C).

## 10. Setup (builder D)

### 10.1 What D owns

`setup.go` (the hook methods and `setupScreen`), `presets.go` (the preset list) and any `setup*.go`. The text box is
the fixed `editor.go` (`newBytesEditor`: Name 23 bytes, passwords 15 bytes; a paste clamped at the limit; `Clear`). D's
tests: `setup_test.go`, `editor_test.go` (the fixed editor's tests: keys, wrapping, the byte limit, `Clear`),
`setup_e2e_test.go`, goldens under `testdata/TestSetup*`, `testdata/TestEditor*` and `testdata/TestHelpSetup`. Every
top-level name D declares starts with `setup`, `preset` or `pick` (section 11).

### 10.2 States and hints

`setupScreen` has a mode:

| Mode | Body | Hint | Esc |
| --- | --- | --- | --- |
| reading (ReadNode under way, `m.busy` held) | "Reading the node's settings… (0:02)" | `"esc back  ? help"` | cancels, to Board |
| read failed | the Node's Problem in words | `"enter back  ? help"` | to Board (enter too) |
| form | the rows (10.3), scrolled to the focused one | `"enter edit  p presets  y send  ? help"` (37) | saves the draft, to Board |
| editing (a text box) | the form, the row's box open | `"enter keep  esc cancel  tab next"` (32) | cancels the edit (10.6) |
| picking (a pick list) | the allowed values | `"j/k pick  enter keep  esc cancel  ? help"` (40) | back to the form |
| presets | `nodecli.Presets` | `"j/k move  enter use  esc back  ? help"` (37) | back to the form |
| sending (the Job) | `fitSteps` (the password line shows `••••`) | `"esc stop  ? help"` | `confirmStopSetup` |
| done | the Outcome (8.9 scrolling) | `"enter done  ? help"`; scrolling `"j/k more  enter done  ? help"` | to Board |
| failed | the Outcome, State and Recovery pinned | `"r retry  esc back  ? help"`; scrolling `"r retry  j/k more  esc back  ? help"` | to Board |

`r` on a failed setup goes back to the form with its values kept and both passwords empty (Help: "r back to the form
to send again (type the password again)"); `y` is only ever "send". `confirmStopSetup` is worded by the picture's
`Risk` (`state.go`); Ctrl+C while sending asks `confirmQuit` (owner question 16), worded by `setupConfirmText` as the
same question ending "…and meshflash quits.".

### 10.3 The form (SPEC §4's table, four radio rows)

Rows, one each: Name, Frequency, Bandwidth, Spreading factor, Coding rate, TX power, Path hash, Latitude, Longitude,
Local advert, Admin password, and on a room server Guest password. Each `▸ Name        Hilltop 2`; a problem
(`nodecli.Check`, or `CheckRadio` on a radio varying only that row, since `FieldError.Field` is "Radio" for all four:
brief G8) wrapped in grey under its row; the path hash row "needs v1.14+" on older firmware; the messenger file's
note, when it was skipped, as the first line; "The Zero's clock isn't synchronised: the node's clock won't be set."
when `ClockSynced` is false. The form scrolls to keep the focused row and its problem in view (8.9).

Both password rows show "••••" whatever the length (empty: nothing, with the "type one" problem); their text box shows
one `•` per character typed (the editor's mask). The guest password's default is the node's (`Node.Current`, from
ReadNode only), so it is masked from the start and never reaches a golden (`TestNoGoldenHoldsThePassword` checks the
fake room server's guest password too).

Defaults, in SPEC's order: Name the node's; the radio the messenger's (`MessengerRadio`, when its note is "") else the
last sent (`m.cfg.Radio`, when `CheckRadio` takes it) else the node's; TX power and path hash per owner question 6
(SPEC as written: the node's); location and local advert the node's; admin password empty; guest password the
node's. Then the draft for the port's `DraftKey` (owner question 13), when there is one, over them (typed text, so a
half-typed value comes back; a row in `Cleared` comes back blank). A draft whose `Role` isn't the node's role applies
only the rows both forms have, with the note "Restored what was typed for this board as a <role>." A draft kept by USB
socket says "Restored what was typed for the board on this USB socket." `p` lists `nodecli.Presets`; Enter applies one
(`Preset.Apply`: the four radio rows and the path hash). Frequency, TX power, latitude, longitude, local advert, name
and passwords are text boxes (Enter keep, Esc cancel restoring the old value, Tab next field, Backspace: S21);
bandwidth (`nodecli.Bandwidths`), spreading factor (5–12), coding rate (5–8, shown 4/5–4/8) and path hash (1–3) are
pick lists. TX power checks `nodecli.CheckTxPower(dbm, device, info.Board)` and says the limit (`TxLimit`). On screens
"-9 dBm" uses an ASCII hyphen (section 13).

### 10.4 Keys

Form: Tab/`j`/`k`/↑/↓ field, `enter` edit, `p` presets, `y` send (refused with the problems listed while
`nodecli.Check` finds any; a held `y` does nothing: 8.8), `esc` back (saves the draft). Help entries: form Tab/j/k
field, enter edit, p presets, y send, esc back (keeps what you typed); text box Enter keep, Esc cancel, Tab next,
Backspace; sending esc stop (asks y); failed r back to the form; done enter, esc.

### 10.5 Sending

`y`: save the draft first (a kill mid-send keeps it), then, when `canStart()`, `Backend.Setup(context.Background(),
SetupRequest{Node, Settings})` in Update and `m.watch(job)`. Clear both password fields (`editor.Clear`, and any value
kept to restore on Esc) as soon as the request is built, after sending, and in `closeSetup`. On `Done`
(`setupProgress`): save `Outcome.Sent` whenever it is set (`store.SaveRadio(SavedRadio(*Sent))` and `m.cfg.Radio`: the
node took that radio even if a later step failed); OK → delete the draft, show the Summary; failed → keep the draft,
show Summary, `State` and `Recovery`.

### 10.6 Drafts (S7)

`saveSetupDraft`: nothing for a port without a `DraftKey`; the draft (`store.Draft`, every row's text, `Cleared`, no
passwords) saved only when it differs from the form's defaults, else the board's draft deleted. It acts only in the
form, editing, picking and presets modes (never reading, sending or done) and may run twice in a row. It is called
from `SaveUnsent` (a quit, an Esc hold, SIGTERM through `Shutdown`), on Esc out of the form, and on `y` before sending.

An Esc hold reaches the model as one Esc tap first, and in a text box Esc cancels the edit, which would throw away the
half-typed line before the SIGTERM's `Shutdown` saves the draft. So when Esc cancels a text box, the text it held
stays that row's draft value for 3.5 s on the screens' clock (`setup` state: row, text, when); `saveSetupDraft` writes
it if it runs within that window; any other key drops it. The screen shows the old value throughout, so S21's cancel
is unchanged. Tests: `TestSetupDraftKeptThroughEscHold` (open Name, type "Hill", `esc`, `d.m.Shutdown()`: drafts.json
has name "Hill"), and `TestSetupCancelledTextDropped` (the same with a key, or 3.5 s, between).

### 10.7 Goldens and tests

Goldens: `TestSetupReading` (held), `TestSetupReadFailed`, `TestSetupForm` (repeater v1.18.0),
`TestSetupFormRoomServer`, `TestSetupFormProblems`, `TestSetupFormScrolled`, `TestSetupEditName`,
`TestSetupEditPassword` (masked), `TestSetupPickBandwidth`, `TestSetupPresets`, `TestSetupMessengerNote`,
`TestSetupClockNotSynced`, `TestSetupOldFirmware` (v1.13.0: "needs v1.14+"), `TestSetupDraftRestored`,
`TestSetupDraftRoleChanged`, `TestSetupSending`, `TestSetupSendingScrolled`, `TestSetupStopAsks`, `TestSetupDone`,
`TestSetupFailed` (a StepError's State), `TestSetupFailedLate` (scrolled, State pinned), `TestSetupNotARepeater`,
`TestHelpSetup`.
Tests: `TestSetupDraftKept` (Esc, Ctrl+C, `SaveUnsent`; reopening restores it), `TestSetupDraftKeptThroughEscHold`,
`TestSetupCancelledTextDropped`, `TestSetupDraftNotAfterSend` (a quit after "Set up" brings back no draft),
`TestSetupDraftBySocket` (a CP2102 board, owner question 13), `TestSetupDraftHasNoPassword` (`drafts.json` bytes),
`TestSetupPasswordCleared` (reopen the password box after sending and after Esc: empty), `TestSetupGuestMasked`,
`TestSetupSendsOnce` (hold, `y`, `y`, releaseAll: one "Setup" call), `TestSetupHeldYSendsNothing`,
`TestSetupStopNeedsYes` (S20), `TestSetupSavesRadio` (also after a failure past `set radio`), `TestSetupDefaultsOrder`
(messenger, last sent, node), `TestSetupTxLimit`, `TestSetupReadingGoes` (S16: the answer with no key replaces
"Reading…"), `TestSetupHintsFit` (every Setup state at the three sizes, the rule of `TestHintsFit`),
`TestNoGoldenHoldsThePassword` (walks `testdata/` for the tests' admin and guest passwords), `TestEditorKeys`,
`TestEditorWraps`, `TestEditorByteLimit`, `TestEditorClear`.

## 11. Who owns what

| Builder | Owns (writes and may change) | Must not change |
| --- | --- | --- |
| A | `internal/store/store.go` (bodies), `internal/store/*_test.go` | `internal/store/types.go`; the signatures in `store.go` |
| B | `internal/flash/backend.go` (bodies, unexported fields), any new `internal/flash/*.go` and `*_test.go`; everything under `internal/flash/fakeworld/` (bodies of `world.go`, new exported additions, new files, `fakeworld/catalog/` copies) | `internal/flash/types.go`, `job.go`, `rules.go`; the signatures in `backend.go` and the exported API of `world.go` |
| C | `internal/ui/model.go` (all but the hook signatures), new files `internal/ui/{board,choose,role,version,notes,confirm,download,flashing,result,update,files,catalogview,ctext}.go`, tests `internal/ui/{board,choose,role,version,notes,confirm,download,flashing,result,update,files,help,styles,keys,frame,e2e}_test.go`, goldens of tests not named `TestSetup*`, `TestEditor*`, `TestHelpSetup`, `TestE2ESetup*` | the fixed files (`backend.go`, `state.go`, `hooks.go`, `frame.go`, `steps.go`, `editor.go`, `text.go`, `driver_test.go`, `fakebackend_test.go`), `setup.go` and D's files |
| D | `internal/ui/setup.go` (all but the hook signatures), new files `internal/ui/presets.go` and `internal/ui/setup*.go`, tests `internal/ui/{setup,editor,setup_e2e}_test.go`, goldens `testdata/TestSetup*`, `testdata/TestEditor*`, `testdata/TestHelpSetup`, `testdata/TestE2ESetup*` | the fixed files, everything C owns |
| E | `internal/app/*` (bodies of `app.go`, new files, tests), `cmd/meshflash/*` (bodies, `packaging_test.go`), `packaging/*`, `Makefile` | `Program`, `Env`, `Fake`, `Main`'s signature, `Version`'s name |
| Integrator | `README.md`, `CHANGELOG.md`, `SPEC.md` (its changes, section 14, and the owner's answers), `CLAUDE.md`'s Commands, `THIRD_PARTY_NOTICES` (the kit's copied code as the owner's own), the fixed files | |

Names in package `ui`: every top-level name builder D declares, in code or tests (types, message types, funcs, vars,
consts, test helpers), starts with `setup`, `preset` or `pick` (tests `TestSetup…`, `TestEditor…`, `TestE2ESetup…`,
`TestHelpSetup`, `TestNoGoldenHoldsThePassword`); builder C never uses those prefixes; nobody declares a name starting
with `frame` or one a fixed file declares. Shared helpers live only in the fixed files. Only builder D's code reads or
writes `m.setup`; only builder C's reads or writes the other screen fields and `m.flow`. What D may read and write of
the model is in `hooks.go`.

Review items for C and D (no test can catch them): every input a Cmd needs is captured before the Cmd is returned
(a Cmd that reads `m` races with the next Update, and the driver runs Cmds alone); `Download`, `Flash` and `Setup`
only in Update after `canStart()`; no `tea.Tick` (the driver fails a Cmd that takes real time over the fake backend).

## 12. Order and integration

1. The owner answers section 16 in chat; the integrator writes the answers into SPEC §11 and adjusts the one
   function or constant each touches.
2. Each builder gets their own copy of the tree as the contract stands: a git worktree from a contract commit once
   the owner allows committing, else a `cp -r` snapshot into the scratchpad (`…/scratchpad/phase2/tree-A` to
   `tree-E`). Each builds and tests in their copy and hands back only the files section 11 gives them, compiling
   (`go build ./... && go vet ./...` pass in their copy).
3. All five start at once. A, B, D and E need nothing else; C's and D's goldens need only the fake backend,
   `testCatalog` and the fixed frame.
4. As each part lands, its own tests pass: A's, B's (backend and world), C's and D's goldens and unit tests (over the
   fake backend and a real store, so A's part makes the store tests meaningful), E's. A golden made before the other
   screen builder's part lands may change at integration only where that builder's code is on screen (it never is on
   the other's screens: the frame is fixed).
5. The integrator merges the file sets, runs the end-to-end tests (C's and D's `TestE2E…`) over B's world, fixes the
   seams, regenerates goldens only on purpose (`go test ./internal/ui -update`, reading `git diff internal/ui/testdata`),
   runs `make test`, builds the demo, and does the PC run into a file: `dist/meshflash-demo --screen --framebuffer
   <scratchpad file> --keyboard <fifo>` (never the release binary, which would scan the PC's real ports; the demo
   refuses `--screen` without `--framebuffer`), with the owner's choice of font (owner question 11), looks at the
   frames, and shows the owner the icon.
6. Docs move with it (S37): README (installing, keys, `make run-small`), CHANGELOG, SPEC's changes, CLAUDE.md's
   Commands.

Done when: `make test` passes (`gofmt`, vet on host, arm64 and `-tags demo`, `go test -race ./...`, the no-fakes
check); every golden of sections 9 and 10 exists at the three sizes; the end-to-end tests of section 15 pass; the demo
runs at 45×10 (`make run-small`) and into a framebuffer file; the icon is shown to the owner; nothing touched a real
board, the LCD or the network.

## 13. Decided without asking (technical defaults; the owner can overrule any)

- **Hints** in the SPEC mock-ups' style: lowercase keys, two spaces between (`"enter choose  u update  c files  ?
  help"`), every one measured to fit 40 columns.
- **Exclusive opens** for probe, flash and setup, so ModemManager or a second meshflash can't open the port mid-flash;
  a port another program holds says so.
- **Probes one at a time**, on Board and Choose board only.
- **Sizes** in decimal units (361 KB, 1.3 MB); durations "10 s" under a minute, "1:05" above; a percentage rounded but
  never 100 before the end; a finished step's time shown from 5 s.
- **screen.log on every run**, in the terminal too (SPEC §8 lists it with no mode); the kit opened it only for
  `--screen`.
- **An unreadable config.json** (not a damaged one) doesn't stop meshflash: it starts without the remembered boards
  and radio and says so in screen.log.
- **The demo build** is a separate binary, `meshflash-demo`, with its own folders, a fake world and the committed
  catalog; the release binary links no fake (checked by `make test`). `make run-small` runs it.
- **Store writes** are made from Update, synchronously (small files), as the kit's are.
- **Shutdown** waits at most 1.5 s for a running Job after the program ends, then writes how it ended to screen.log.
- **After a download from Confirm**, Confirm shows again with the SHA-256 and `y` flashes: SPEC §4 says it "shows
  again", and nothing is written without a `y` on a screen that shows the checked file.
- **ESP32 post-flash**: the MD5 is the check (SPEC §4); no `ver` probe after an ESP32 flash, only waiting for the
  board to come back so Result knows where it is.
- **Every `y` question ignores a held `y`** (8.8), not only the low-battery one.
- **Download, Flash and Setup are started in Update** (they return at once), so no key can slip between starting a
  Job and watching it.
- **A battery that can't be read** asks as under 30% does (the reading failing is not "no battery").
- **The tty back at 115200 after a touch** (4.1), and the family check before any open (`Fits`).

## 14. SPEC changes needed

To be written into `SPEC.md` (with a line under "Changes to this spec") once the owner has answered section 16:

1. §4 Version: "`n` (or Enter on a version) shows its release notes" contradicts §3 and §5 (Enter goes to Confirm).
   Enter goes to Confirm; `n` shows the notes (question 14).
2. §4 Confirm: after the download, Confirm shows again with the SHA-256 and `y` flashes; the low-battery question is
   asked only when `y` would flash, and also when the battery can't be read; a held `y` answers nothing (question 5);
   an unsaved file reads "Repeater v1.18.0 · update · not saved yet"; the board's absence is said; an nRF52 board whose
   SoftDevice the catalog doesn't name asks a second `y` (question 20).
3. §4 status line: define the mark (question 14): `…` busy with a board; `●` while the board the screen is about is
   listed with its USB identity ("identified"), `○` when it isn't; on screens about no board, whether any port is
   listed. §2's USB-identity row: sysfs is read every second on every screen, never opening a port.
4. §4 Board: an nRF52 in its bootloader isn't opened ("In its bootloader, ready to flash.", or why a double press is
   needed first); another program holding a port says so; `s` works only on a port that answered as a repeater or room
   server (never ZephCore), else a note says why; the bridge is named by its chip family ("CP210x", not the mock's
   "CP2102").
5. §4 Choose board: "All boards" lists the boards not already above; boards meshflash can't flash on this port
   (download only, ESP32-C6, the other chip family) are listed grey with why, and Enter on one says so (question 14).
6. §4 Role: a role is shown as the catalog's title and subtitle ("Companion · Bluetooth"); the selected role's notices
   are shown above the list (question 14).
7. §4 Setup, §5 keys: Esc while a setup is being sent asks `y`, worded by what was sent; Ctrl+C asks too (question
   16); a failed setup offers `r` to go back to the form with its values (passwords typed again); the form scrolls;
   both password rows show `••••` (question 14).
8. §4 Update catalog: Esc while fetching stops it; the saved catalog stays (question 14).
9. §4 Result: Esc goes back to Board, as Enter does; `r` per question 4; State and Recovery are pinned and the rest
   scrolls; "Stop now?" on Flashing says what stopping leaves (nothing written yet, no working firmware, or already
   written).
10. §4 Flashing and Setup at 40×10: the oldest done steps fold into "✓ 3 earlier steps" (question 14).
11. §5: `j`/`k` scroll two lines in Notes, Help and Result (question 15).
12. §6.1: every open for probe, flash and setup is exclusive (TIOCEXCL); a tty touched at 1200 baud is left at
    115200; the board picked is matched to the port's chip family before anything touches it.
13. §6.3: a retry after a failure past START (or a stopped DFU) waits up to 60 s for a fresh bootloader session (reset
    pressed twice) and never sends START into the old one (questions 17, 18).
14. §8: the formats of `config.json` and `drafts.json` (`internal/store/types.go`, with `cleared`); drafts kept per
    board by serial or, for a board without a useful serial, by USB socket (question 13); a damaged `config.json`
    reads as empty and an unreadable one is skipped with a line in screen.log; nothing is remembered on Choose board
    for a board without a useful serial number.
15. §8: `screen.log` is written on every run, in the terminal too.
16. §9.1: the packages `internal/store`, `internal/app`, `internal/flash/fakeworld`, `cmd/meshflash`; the demo build
    (`-tags demo`, `meshflash-demo`, its own folders, a fake world and the committed catalog, never in
    `dist/meshflash`); `make run-small`, `make demo`, `make icon`.
17. §4 Setup: TX power's floor written "-9 dBm" with an ASCII hyphen on screens (S15: U+2212 isn't among the glyphs
    checked on the device's font).
18. §2: "Help says … each file's size" becomes: sizes are shown once known (downloading, saved); the catalog lists
    none.
19. §10: the screens' tests use the test driver's own clock, timers and Job waits; no real sleeps but the SIGTERM
    test's 200 ms in a child process (the kit's).
20. §4 Help: the footer as in section 8.6 (the messenger file it reads; "Run these once").
21. The owner's answers to section 16, in §11's table, and S14's headings in reverse (question 12).

## 15. Test plan

| Test | Where | Builder |
| --- | --- | --- |
| Goldens of every screen state in sections 9 and 10, at 45×10, 40×10 and 53×14, through the LCD parser (`check`) | `internal/ui` | C, D |
| `TestHintsFit`: every screen but Setup in every state at the three sizes: the hint uncut, with `? help` outside text boxes, questions and Help (Help's own hint has none: exempt) | `internal/ui` | C |
| `TestSetupHintsFit`: the same for every Setup state | `internal/ui` | D |
| `TestStyles`: under `termenv.ANSI`, a heading reversed (`\x1b[7m`), the text box cursor reversed, secondary text grey (`\x1b[90m`), nothing 256-color or true color, no bold/dim/underline | `internal/ui` | C |
| `TestGlyphs`: every rune in `testdata/**/*.golden` is ASCII, a curly quote, a dash, or one of `…▸●○·¢•→✓✎` (S15) | `internal/ui` | C |
| `TestStatusLineBattery`, `TestResize` (down to 30×6), `TestHelpScrolls`, `TestHelpNamesTheProgram` | `internal/ui` | C |
| `TestScanEverySecond` (the frame's timer re-arms on every screen; Scan is its only call; no probe off Board and Choose), `TestStatusMarkFollowsTheBoard` (unplug on Confirm with no key: `○`, "isn't plugged in now", `y` does nothing) | `internal/ui` | C |
| S16 `TestFlashingMessagesKeepUp` (a `Done` picture with no key moves to Result in the same Update; "Don't unplug the board.", an open question and Help are gone), `TestBoardPortGoneDropsItsLines`, `TestUpdateMessagesKeepUp`, `TestDownloadLeavesAtDone`, `TestSetupReadingGoes` | `internal/ui` | C, D |
| S20 `TestDeleteNeedsYes`, `TestStopNeedsYes` (Esc on Flashing: `n` and `?` do nothing; `y` cancels once), `TestQuitWhileFlashingAsks`, `TestLowBatteryAsksTwice` (and the held-`y` rule: `y` every 33 ms for 2 s, nothing; 0.6 s, `y`, flashed once), `TestSetupStopNeedsYes` | `internal/ui` | C, D |
| `TestEnterTwiceFlashesOnce` (`hold`, `y`, `y`, `releaseAll`: exactly one "Flash" in `fb.called()`), `TestConfirmKindRace`, `TestConfirmYWaitsForPrepare`, `TestLateAnswers` (held: a probe answer for a port gone; a stale `preparedMsg`; a picture of an old Job), `TestRememberBoard`, `TestNothingFetchedUnlessAsked` (`UpdateCatalog` and `Download` only after `u`, `d`, `y`) | `internal/ui` | C |
| `TestColorProfile`, `TestASIGTERMAfterQuittingIsAbsorbed`, `TestScreenLogFreshEachRun`, `TestVersionFlag`, `TestShutdownIsBounded`, `TestShutdownSavesSentRadio`, `TestDemoDepsWired`, `TestNoRealDepsInTests`, `TestBatteryUnreadable` | `internal/app` | E |
| `TestHomeScreenEntry`, `TestIcon` | `cmd/meshflash` | E |
| Store tests (section 6) | `internal/store` | A |
| Backend and world tests (4.3); `TestCatalogCopies` (fakeworld's catalog files equal the originals) | `internal/flash`, `fakeworld` | B |
| E2E `TestE2EFlashHeltecV4Update`: Board → Choose board → Repeater → v1.18.0 → `u` → `y` (download) → `y` → Flashing → Result "Flashed", MD5 checked; the fake ESP32's flash holds the image at 0x10000, nothing erased | `internal/ui` | C |
| E2E `TestE2EFlashHeltecV4Wipe`: the same with `w`: the chip erased, the merged image at 0x0 | `internal/ui` | C |
| E2E `TestE2EFlashRAK4631`: the 1200-baud touch, the bootloader on USB, DFU, STOP, back on USB, `ver` says v1.18.0, the tty back at 115200; Result offers `s` | `internal/ui` | C |
| E2E `TestE2EUnplugMidFlash`: ESP32 and nRF52 unplugged while writing, during the bootloader wait, during the erase wait and after STOP: Result with the State and Recovery, `○`; the screen is never left on Flashing | `internal/ui` | C |
| E2E `TestE2ERetryAfterStopped` (4.4) | `internal/ui` | C |
| E2E `TestE2EWrongFamilyRefused`: a RAK picked for a CH340 port can't be chosen; Prepare refuses it all the same; nothing opened | `internal/ui` | C |
| E2E `TestE2ENoCatalog`: start with none: the Board says so; `enter` says press u; nothing fetched | `internal/ui` | C |
| E2E `TestE2ECatalogUpdate`: `u` through `httptest` (`newWorldDriverOver`): "72 boards, newest v1.18.0"; then offline and a 503: the saved catalog stays | `internal/ui` | C |
| E2E `TestE2ELowBattery`: battery 24%: the warning, the second question, `n` doesn't flash; unreadable: the same | `internal/ui` | C |
| E2E `TestE2ESetupRepeater`: Board `s` on a Heltec v4 running repeater v1.17.1 → the form → a name, a preset, the admin password → `y` → each step ✓ → the reboot → "Set up"; the fake node stores the settings; `config.json` has the radio; the draft is gone; `noSecret(password)` (screen, screen.log, drafts.json, config.json) | `internal/ui` | D |
| E2E `TestE2ESetupAfterFlash` (Result `s`, firstBoot), `TestE2ESetupUnplugged` (a StepError's State shown), `TestE2ESetupDraftThroughQuit` (half-typed, Ctrl+C, reopened; and half-typed in a text box, `esc`, `Shutdown`, reopened) | `internal/ui` | D |
| `TestNoGoldenHoldsThePassword` | `internal/ui` | D |

Everything in virtual time: the world's `VirtualClock` for the boards and the backend, the driver's clock for the
screens; no real sleeps; `-race` (`make test`). No test touches the network (in-memory HTTP or `httptest` on the
loopback interface) or a real serial port. The driver's patterns:

- **An answer still out**: `d.hold(true)`, the key, `d.release(i)` for the Cmds that should answer (the test knows its
  screen's order: `release(0)` is a tea.Batch's first child once the batch itself has been released), `check`, then
  `d.releaseAll()`. Example: hold, `r`, release the scan, `advance(time.Second)`, check "Asking… (0:01)".
- **Late answers**: hold, scan without the port, release the held probe: its answer is dropped.
- **Time**: `advance` a second at a time to see each scan and tick (a timer re-armed during `advance` is due after it).
- **End of test**: the driver stops every Job it watched and, over a world, waits for it before the folders go.

## 16. Questions for the owner

Asked in chat, multiple choice, the recommendation first, before the builders start; the answers go into SPEC §11.
Each answer is a change to one function or constant; the contract and this file describe the recommended option.

1. **The node's name on the Board screen.** The mock-up shows “Hilltop 2”, but the probe as written asks only `ver`,
   `get role` and `board`. (a, recommended) Read the name with read-only `get`s right after a repeater or room server
   answers (about half a second more; never the guest password). (b) Leave the name off the Board screen.
2. **"saved" on the Version list for an ESP32 version**, which has two files. (a, recommended) Say which: "update
   saved · 361 KB", "wipe saved · 1.3 MB", "both saved". (b) "saved" when either is. (c) Only the update file counts.
3. **`d` (download only) on an ESP32 version.** (a, recommended) Download both files, so update or full wipe works
   offline later. (b) Ask `u` or `w` first. (c) The update file only.
4. **`r` after a failed flash.** (a, recommended) Back to Confirm with the board and version as they were; on an ESP32
   the kind is cleared, so you press `u` or `w` again, then `y` (no preselection, as you chose); an nRF52 goes straight
   to `y`. (b) Back to Confirm with last time's kind kept, so `y` alone flashes again.
5. **A held `y`.** The keyboard repeats a held key, so one long press could answer both "Flash this?" and "Battery
   24%. Flash anyway?". (a, recommended) A `y` that comes less than half a second after the previous key, itself a
   `y`, is ignored and the question stays, so a held `y` never answers anything: release and press again. The same for
   Confirm's and Setup's `y`. (b) The second question takes another key ("f flash anyway"). (c) Leave it.
6. **TX power and path hash defaults on the Setup form.** SPEC §4 says the node's current values, but §8 saves the
   last TX power and path hash sent and §8.4's file may give `tx_dbm`, and nothing reads them. (a, recommended) TX
   power: the messenger's, else the last sent, else the node's, capped at the board's limit with a note; path hash:
   the preset, else the last sent, else the node's. (b) The node's current values, as §4, and stop saving them. (c)
   As written.
7. **An nRF52 flash of firmware meshflash can't ask its version** (companion, KISS, ZephCore, Ripple). (a,
   recommended) "Flashed" when the bootloader took the image and left; Result says whether the board came back on
   USB and that meshflash can't ask this firmware its version. (b) "Flashed" only when it comes back with the same
   serial number within 15 s, else a failure. (c) Always "written, not checked", as a warning.
8. **The two ESP32-C6 boards from ZephCore** (LilyGo T-Lora C6, Seeed Studio Xiao C6), which meshflash can't flash
   (§6.2). (a, recommended) Listed grey, marked "ESP32-C6", and refused before any download. (b) Left out of the list.
   (c) Listed, refused only once the file is downloaded and its header read.
9. **Holding Esc (or the launcher's SIGTERM) while flashing**: there are 3 s before SIGKILL and no way to ask. (a,
   recommended) Stop the flash the safe way at once, write the board's state and how to recover it to screen.log,
   and exit. (b) As (a), and remember it in `config.json` so the next start's Board screen says the flash was cut
   short.
10. **The `--screen-test` page.** The copied `internal/screen` says "Loom screen test" and shows Loom's glyphs, without
    the `✓` meshflash uses (S15: the page shows every glyph the app uses). (a, recommended) Change the copy to name
    meshflash and show its glyphs, recorded as a departure from the kit's copy. (b) Leave `internal/screen` as the
    kit's.
11. **The font for the PC run into a file** (there's no PSF font here; the device has Terminus 6×12). (a,
    recommended) A stand-in 6×12 font made in the scratchpad from a bitmap font fetched through the Go module proxy,
    so the PC frames are 53×14 like the LCD; the real Terminus first shows in Phase 3. (b) You copy the device's
    `/usr/share/consolefonts/Uni2-Terminus12x6.psf.gz` into the session. (c) A synthetic block font: the plumbing is
    checked, the frames can't be read.
12. **The selected row and group headings.** S14 says reverse video for the selection and for headings; the SPEC
    mock-ups mark the selected row with `▸`. (a, recommended) `▸` on the selected row, as in the mock-ups and Loom's
    library, and group headings ("MeshCore", "All boards:", Help's "Keys") in reverse video. (b) `▸` and the selected
    row in reverse too; headings grey. (c) `▸` only; headings grey.
13. **Unsent setup forms for boards with no useful serial number** (a CH340 board, or a CP2102 saying "0001" like a
    Heltec v3): SPEC §8 keeps drafts by serial number, so these would lose a half-typed form, against S7. (a,
    recommended) Keep their draft by USB socket and identity ("usb 1-1.2 10c4:ea60"); the form then says "Restored
    what was typed for the board on this USB socket." (b) Keep it in memory for this run only. (c) None, written into
    SPEC as a departure from S7. (Choose board still remembers boards by serial number only.)
14. **The other screen and key details below** (SPEC §14 items 1, 3–10). (a, recommended) Approve them as written.
    (b) Name the ones to change.
    - Enter on Version goes to Confirm and `n` shows the notes (SPEC §4 also says Enter shows the notes).
    - The status mark: `…` while meshflash talks to a board; else `●` while the board the screen is about is plugged
      in (Board: the selected port; Choose board to Flashing: the one picked; Result and Setup: the board as it is
      now) and `○` when it isn't; on Update catalog and Saved files, whether any board is plugged in. To keep it true
      meshflash reads Linux's USB list every second on every screen (opening nothing).
    - Choose board lists the boards meshflash can't flash on this port in grey with why ("download only", "ESP32-C6",
      "nRF52" on an ESP32's port), and Enter on one says why.
    - Role shows the catalog's title and subtitle ("Companion · Bluetooth"), and the selected role's notices above
      the list.
    - Setup: Esc while sending asks `y`; a failed setup offers `r` to go back to the form (passwords typed again);
      both password rows show `••••`.
    - Result: Esc goes back to Board, as Enter does.
    - Update catalog: Esc while fetching stops it; the saved catalog stays.
    - Long text at 40×10: step lists fold their oldest steps into "✓ 3 earlier steps"; Result and Setup's end scroll
      with `j`/`k`, what to do now kept in view.
    - The Board names a USB bridge by its chip family, "CP210x" (the mock says "CP2102").
    - Confirm for a file not yet saved: "Repeater v1.18.0 · update · not saved yet" (the size shows once known).
15. **`j`/`k` in Notes, Help and Result.** (a, recommended) Two lines at a time, as in Loom, where you found one line
    slow going. (b) One line, as SPEC §5 says.
16. **Ctrl+C while a setup is being sent.** (a, recommended) It asks `y` first, as Esc does ("Stop now? The node keeps
    what it was sent so far, and meshflash quits."). (b) It stops the setup and quits without asking (SPEC §5 asks
    only while flashing).
17. **Retrying an nRF52 flash that stopped part-way.** meshflash must never send START into the old bootloader
    session, so a retry needs a fresh one (reset pressed twice). (a, recommended) No change to the finished Phase 1
    code: Result says "Press r; meshflash then asks you to press reset twice quickly.", and after `r` and `y` the
    Flashing screen asks for the double press and waits for it (if you pressed twice earlier and meshflash didn't
    see it, it asks again). (b) meshflash notices a double press made at any time, also on the Result screen before
    `r`: this needs a small addition to Phase 1's USB scan (reading Linux's device number for the board, which changes
    every time it re-appears), which needs your OK.
18. **An nRF52 already in its bootloader when meshflash starts.** meshflash can't tell a fresh session (you pressed
    reset twice) from one left by a flash that stopped part-way before a restart. (a, recommended) Treat it as fresh
    only if meshflash saw it appear in this run; otherwise ask you to press reset twice when you flash it. (b) Remember
    stopped flashes in `config.json` across restarts, and trust any other bootloader.
19. **If meshflash crashes, or the CLI app's terminal closes, mid-flash.** (a, recommended) Let the flash finish with
    no screen (up to about 3 minutes) and write how it ended to screen.log. (b) Stop it the safe way at once, as a quit
    does.
20. **nRF52 boards whose SoftDevice the catalog doesn't name** (T-Echo, Wio Tracker L1 EINK, ThinkNode M1, Nano G2
    Ultra): if the file needs another SoftDevice, the bootloader refuses it and erases the app (an OTAFIX board can be
    left in Bluetooth DFU). (a, recommended) Confirm says so and asks a second `y`, as for a low battery. (b) The note on
    Confirm only.

## 17. Review notes

Three reviews (SPEC and standards, safety, testability) of the first draft, 24 Nov 2026. Every blocker and major is
fixed in this file and the contract; the cheap minors too. Where the fix differs from the one suggested, or isn't
taken, why:

- **D couldn't test alone** (testability blocker): taken as the first suggestion, a fixed working frame (`frame.go`,
  `steps.go`, `editor.go`, `text.go` all the integrator's), rather than producing D's goldens at integration. C's
  Update and View moved into the frame; C now fills hooks.
- **Two Jobs from one press** (safety and testability majors): taken as the alternative both offered, calling
  `Download`, `Flash` and `Setup` in Update. It closes the window without a "starting" flag; the remaining window (the
  battery reading at `y` on Confirm, in a Cmd) is closed by C's own pending flag and tested with `hold`.
- **The status mark** (spec major): the frame scans every second on every screen, not only on the screens that name a
  board. It is one rule instead of a list, it costs a sysfs read a second, and it keeps the mark true on Update and
  Saved files too. S23 concerns the network and doesn't apply.
- **The held y** (spec major): the rule is "less than 0.5 s after the previous key, itself a y", applied to every
  question, not only the three places named: a deliberate y never follows another y that fast, and one rule is easier
  to test and to explain.
- **The Result repeating itself** (spec minor): the rule is on builder B (State is one sentence, never repeated at the
  start of Recovery) rather than a prefix comparison in the screens, which would be the screens interpreting protocol
  text.
- **The password box hiding its length** (safety minor): the rows hide it ("••••" whatever the length); the open text
  box shows one `•` per character, as the kit's editor does, since typing blind into a box that never changes is worse
  than showing how many characters were typed.
- **The slow-Cmd check** (testability minor): 500 ms rather than 250 ms, so a loaded machine running packages in
  parallel never trips it; a `tea.Tick` of a second still does.
- **The battery** (spec minor and testability minor disagreed): read only in Cmds, never in View, as the frozen rule
  says; the frame keeps a reading updated every 30 s for the status line, and Confirm reads its own, fresh, in its
  Prepare Cmd and at `y`.
- **`setupConfirm` writes** (testability major): D asks through the frame's `ask`, not by writing `m.confirm`; the
  frame drops stop and quit questions at a Job's Done for both builders.
- **`Prepared.Ask`, `Progress.Risk`, `Port.DraftKey`, `Draft.Cleared`, `World.At`, `World.SetBatteryUnreadable`** are
  additions to the fixed types, made now so no builder has to stop for them.
- **The nRF52 stale session** (safety blocker): the mechanism is written (4.4) and works without any Phase 1 change;
  the owner decides whether `usbid` gains `DevNum` (question 17) and what happens across a restart (question 18). The
  review recommended `DevNum`; this file recommends the wait instead: Result tells the owner to press `r` first, so the
  usual path is one double press either way, it changes no finished Phase 1 code, and it leaves one path to test. The
  cost is a second double press when the owner pressed early and the every-second scans missed it.
- **Questions 1 and 8 before B and C start** (testability minor): every question in section 16 is asked before
  anyone starts (section 12, step 1).
- Not taken: nothing else. A stray macOS arm64 binary named `meshflash` sits untracked at the repository root; it is
  no part of the contract, and the integrator removes it and adds `/meshflash` to `.gitignore`.
