// Package nodecli sets up MeshCore repeaters and room servers over their USB serial console
// (docs/meshcore-flashing.md section 7; SPEC.md 4 "Setup" and 7).
//
// A setup is four calls, on a port the caller opened raw at 115200 with DTR and RTS asserted (Linux asserts both
// on open; an nRF52 prints nothing while DTR is low):
//
//  1. Probe asks `ver`, `get role` and `board`, sending nothing that changes the node; a board that isn't running
//     a repeater or room server gives ErrNotCLI in about 2 s, and one that echoes but never answers (left in
//     `region load` mode) ErrStuck. ReadSettings then reads the node's current values for the form's defaults.
//  2. Plan checks the owner's Settings (Check: each problem names its field and the fix) and turns them into the
//     version-gated Steps of 7.10, each with its Display line (passwords as ••••).
//  3. Run sends them one at a time, each command ending in CR and waiting up to 5 s for its "  -> " reply, reads
//     every value back and compares it (numbers numerically), and ends with `reboot`, which applies the radio. It
//     stops at the first wrong reply with a *StepError naming the step and saying what the node is left with.
//  4. The caller closes the port, waits for the board to come back and reopens it; Confirm checks `ver` and
//     `get radio`.
//
// Presets are MeshCore's 27 regional radio presets, built in; ReadMessengerRadio reads a messenger's radio
// settings (SPEC.md 8.4); TxLimit is a board's TX power limit; FakeNode is a fake repeater or room server for
// serialport.Fake, per role and version, for these tests and those of the packages that drive a setup.
//
// Passwords are a Secret: sent to the node and nowhere else. fmt prints them as ••••, JSON as "", a Step prints
// its Display and its JSON masks a Secret step's password (spaces and all), and no Display, Progress, error or
// Problem quotes one; a reply that holds a password is never quoted either. (Only the owner's own text can: a name
// typed to contain the password shows in "Name: …", as it does in every advert the node sends.)
//
// # The reference
//
// The web flasher has no setup automation: its console sends what the owner types, with CR LF (lib/console.js:67-76).
// The reference for setup is MeshCore's own "Repeater Setup" tool that the flasher opens, config.meshcore.io
// (aff42ad8, MIT, Copyright (c) 2026 Rastislav Vysoky): lib/serial-cli.js for the line protocol and src/gui.js for
// the version parsing and presets. conn.go follows serial-cli.js; testdata/configtool records that file, run
// unmodified in Node, on FakeNode's byte streams, and TestConfigTool checks that nodecli writes the same bytes and
// takes the same replies. The command set, replies, limits and per-version behaviour come from MeshCore's source
// at the release tags repeater-v1.11.0 to v1.18.0 (MIT), cited in docs/meshcore-flashing.md 7; ftoa.go ports its
// float printing for the fake.
//
// # Departures from the config tool
//
//   - The reply must come after this command's echo. serial-cli.js falls back to the start of its buffer when no
//     echo has arrived yet, so a stale reply (a second answer to a repeated `ver`, an earlier command's) can be
//     taken for this command's; findReply never does (TestConfigTool's "stale reply" case). It looks for the
//     command and its CR, which both roles echo; serial-cli.js tries CR LF first, which on a room server finds the
//     reply's own line end for a bare CR (the escape below) and never resolves.
//   - Replies are not trimmed (the config tool trims them): a blank value reads "> ". Names and passwords with a
//     space at either end are refused anyway.
//   - Every command is at most 128 bytes and holds no CR, LF or NUL, refused before anything is written: `set radio`
//     copies its argument into a 132-byte stack buffer with strcpy (7.10). The config tool has no cap.
//   - The region-load escape, in Run only: when a command times out with no "  -> " after its echo, one bare CR is
//     sent and, if it is answered, the command once more. A node left in `region load` mode swallows every line
//     until a blank one (7.10); the config tool would time out on every command. Ending the mode is a change (the
//     node then routes with the empty table `region load` started, until it restarts; nothing is saved, and setup's
//     reboot loads the saved table), so Probe, ReadSettings and Confirm never send it: Probe gives ErrStuck, and
//     the owner restarts the node. docs/meshcore-flashing.md 7.10 says the map is left unchanged; MeshCore's
//     RegionMap::resetFrom and handleCommand say otherwise (see conn.command).
//   - Version gates on everything setup sends: path.hash.mode only on v1.14.0+ (the config tool also gates it); the
//     frequency floor is 300 MHz before v1.15.0; nodes older than v1.11.0 or with another role are refused
//     (ErrUnsupported). Newer versions than v1.18.0 are taken to behave as v1.18.0.
//   - Checks the config tool doesn't make (7.5-7.7): names 1-23 bytes (it allows 24 or 32) with none of
//     [ ] / \ : , ? * on any version and no space at either end; only the bandwidths the radios take; TX power at
//     most the board's limit (TxLimit) and at least -9; passwords 1-15 bytes with no space at either end, the admin
//     password required, and the guest password required on a room server (a repeater's is sent only when given);
//     latitude, longitude; the local advert interval off or 60-240 in even minutes (the node keeps 2-minute steps
//     and would round an odd one down).
//   - The radio is sent as "869.618,62.5,8,5": the frequency as the shortest decimal of the float32 the node keeps,
//     the bandwidth as the list writes it (the config tool sends "62.5.0", harmless only because strtof stops at
//     the second dot, 7.9).
//   - Every value is read back and compared after setting (the config tool re-reads but doesn't compare): numbers
//     numerically (frequency within 0.0005 MHz, bandwidth 0.01 kHz, location 0.0001 degrees), TX power modulo 256
//     (v1.11.0, v1.12.0 and v1.18.0 print a negative setting as 256+n, 7.4). The admin password, which can't be
//     read back, is compared with the node's "password now: …" and dropped.
//   - `time` is sent only when the Zero's clock is synchronised (Settings.ClockSynced; SPEC.md 11), with the time of
//     sending; "(ERR: clock cannot go backwards)" is a note, not a failure. The config tool sends `time` on every
//     connect, which would also make probing change the node; Probe sends only `ver`, `get role` and `board`.
//   - Setup always ends with `reboot` (the radio needs it); the config tool asks first. Confirm then checks the
//     firmware and radio after the reboot, which the config tool doesn't.
//   - `ver` is sent every second until a version comes back (7.10), and the input is dropped for 250 ms first; the
//     config tool sends each command once. An answer that isn't a version is asked again rather than taken as "not
//     the CLI" at once: a stray byte in the node's line buffer turns the first `ver` into "xver" ("Unknown
//     command"). The 100 ms gap between commands is the config tool's.
//   - Run, Probe and Confirm take a context, so the owner can stop them between reads (at most 100 ms apart). Run
//     stops only between commands: nothing is written once the context ends (it is looked at again after the gap
//     before a command), and a command already written has its reply read for StopGrace (1 s) at most, so a stop
//     never leaves meshflash unsure what the node took unless the node stops answering (StepError.Maybe).
//
// # UNVERIFIED
//
// Resting on source analysis, not hardware (docs/meshcore-flashing.md 7): that a bare CR outside region-load mode
// only draws "Unknown command" (the firmware's empty command falls through every match; source inference); how long
// a node takes to answer after a reboot (ConfirmWait, 20 s) and after a full ESP32 erase (the caller waits longer);
// whether opening the port resets an ESP32 with an auto-reset circuit (Probe's settle and findReply's skipping of
// noise cope either way); that MeshCore's nRF52 core drops output while DTR is low (7.2); the build date in `ver`
// before v1.18.0 (FakeNode uses each tag's date; v1.18.0's matches the release images, TestSamples); the presets'
// licence and terms (7.8); the TX limits of amplified boards MeshCore's FAQ 7.7 doesn't list (22, the SX1262's
// maximum, as for any other board); and that an Ikoka board whose strings don't name its module should get the 2 W
// module's 9 (an inference from FAQ 7.7: the safest of its modules).
package nodecli
