// Package nrfdfu flashes an nRF52 board running MeshCore over Adafruit's serial DFU bootloader, sending the same
// bytes, with the same sleeps in the same order, as MeshCore's web flasher: lib/dfu.js at flasher.meshcore.dev
// 45a5257, itself byte-identical on the wire to adafruit-nrfutil (docs/meshcore-flashing.md section 5).
//
// A flash is four steps, each its own function so the caller (internal/flash) can identify the ports between them:
//
//  1. OpenPackage reads and checks the catalog's .zip, and Package.CheckSoftDevice checks it against the board's
//     SoftDevice, before anything touches the board.
//  2. Touch, the 1200-baud touch, sends a board running MeshCore into its bootloader. (A board already in its
//     bootloader, after a double press of reset, needs none.)
//  3. WaitForPort waits for the bootloader's port, found by the caller by USB serial number and identity.
//  4. Flash runs the DFU session on that port at 115200 baud: START, the erase wait, INIT, the firmware in 512-byte
//     DATA frames with the page-write waits, STOP. Afterwards the caller waits for the app to come back
//     (WaitForPort again) and asks it its version, since the bootloader never says whether the image was good.
//
// reference/nrf52 records dfu.js doing the same in Node against a fake bootloader, and the tests compare this
// package's bytes and sleeps with those recordings; FakeBootloader is the Go port of that fake.
//
// Ported from lib/dfu.js (MIT, Copyright (c) 2025 Rastislav Vysoky), which adapts adafruit-nrfutil's
// dfu_transport_serial.py, util.py and crc16.py (BSD-3-Clause, Copyright (c) 2015 Nordic Semiconductor ASA); the
// files that port code say so. See THIRD_PARTY_NOTICES.
//
// # Departures from the reference
//
// None changes the bytes or sleeps of a successful run; the touch adds one line change (SPEC.md 6.3):
//
//   - The package is read and checked before any port is opened; dfu.js opens the port first and checks nothing
//     (an OTAFIX bootloader zip makes it throw a TypeError with the port open). OpenPackage refuses a manifest
//     without "application" or with any other part (SoftDevice, bootloader: section 3.9 rule 3), an image that
//     isn't a whole number of words, an init packet that isn't an nRF52 one (device type 0x0052, the length its
//     sd_req list implies), and an image whose CRC-16 isn't the init packet's. CheckSoftDevice refuses a package
//     whose sd_req doesn't list the board's SoftDevice: the bootloader would refuse it only after erasing the app,
//     and an OTAFIX bootloader would then drop into Bluetooth DFU mode with no USB port (5.6). Flash checks the
//     package again, that the init packet reaches the bootloader whole, and that CheckSoftDevice has passed it for
//     the board, against the .dat it is about to send (ErrSoftDeviceUnchecked): the costliest check can't be
//     forgotten by a caller.
//   - The touch drops DTR itself, at 1200 baud, before closing, and opens the port exclusively: on Linux a close
//     drops DTR only when it is the last one, so with anything else holding the port dfu.js's open-and-close would
//     do nothing (Touch). dfu.js never touches DTR.
//   - TouchContext is the touch with a stop: its waits are cut into 250 ms steps that look at the context, and a
//     stop before DTR drops sets the line back to 115200 before the close, so the board is never touched and its tty
//     isn't left at 1200 baud. Touch itself sleeps as dfu.js does, once each.
//   - The caller finds the bootloader's port itself and waits for it (WaitForPort, every 250 ms); in the web flasher
//     the owner picks it in the browser's port chooser, twice.
//   - A frame whose acknowledgement doesn't come within 5 s is sent again, the same bytes with the same sequence
//     number, up to Tries (3) times in all; dfu.js fails at the first timeout. The bootloader acknowledges a copy
//     of a frame it already has and drops it, so nothing is written twice. Because each copy is answered, the
//     extra acknowledgements of a resent frame are skipped when they turn up before the next one.
//   - Bytes after an acknowledgement's closing 0xC0 are kept for the next one (serialport never drops bytes); dfu.js
//     drops the rest of that read. One acknowledgement arrives per frame on a good run, so this matters only after
//     a resend.
//   - A wrong acknowledgement number or a broken acknowledgement fails at once, as in dfu.js, and the first
//     acknowledgement of a session is taken whatever its number, as in dfu.js. But a frame that isn't exactly the
//     bootloader's 4-byte acknowledgement fails too, where dfu.js reads a number out of any frame: an echo of the
//     host's own frame carries the right number, and MeshCore's console echoes, so a mistaken port would take the
//     whole image into the node's command line and report success. And more than 4 KiB of bytes with no
//     acknowledgement among them fails: dfu.js's 5 s timeout restarts with every read, so a port that keeps
//     talking (an app's console rather than the bootloader) would keep it waiting for ever.
//   - Errors are a *FlashError saying how far the flash got, and its Recovery says what state the board is in and
//     how to get it back (docs/meshcore-flashing.md 10.2): before START nothing was written; after it the app is
//     erased and only a fresh bootloader session (reset pressed twice) helps. dfu.js says "Please reset the device
//     and try again." whatever happened. One failure is most likely a success: the port vanishing after STOP went
//     out (a resend of STOP whose acknowledgement was lost), because a bootloader that took a good image starts
//     the app at once. FlashError.MayHaveWorked says so, and the caller waits for the app as after a nil error.
//   - Progress counts firmware bytes the bootloader acknowledged; dfu.js counts wire bytes and reaches 100% early.
//   - The context can stop a flash between frames (the owner's "stop").
//   - Flash leaves opening and closing the port to the caller; dfu.js's dfuUpdate opens and closes it itself, which
//     the tests stand in for.
//   - The sleeps are dfu.js's exact values (9745.677… ms, 102.4 ms). setTimeout takes its delay as a whole number
//     of milliseconds (a WebIDL long), so in a browser the web flasher sleeps a fraction of a millisecond less.
//
// # UNVERIFIED
//
// From docs/meshcore-flashing.md, resting on source analysis, not hardware: that a close drops DTR only on the last
// close and that an explicit DTR drop at 1200 baud makes the touch work whoever else holds the port (5.2); that the
// USB serial number is the same in the app and the bootloader, which the caller's find relies on (5.2); that the
// bootloader's acknowledgement of START arrives within dfu.js's 5 s even though the erase blocks USB (5.4); and how
// an OTAFIX board behaves after a refused init packet (Bluetooth DFU mode, 5.6, 10.2).
package nrfdfu
