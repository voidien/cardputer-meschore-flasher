// Package esp flashes an ESP32-family board through its ROM bootloader, sending the same bytes as MeshCore's web
// flasher: js/flash.js's flashEsp32 driving lib/esp32.js, which is esptool-js 0.5.4 unmodified
// (docs/meshcore-flashing.md section 4).
//
// The sequence is the reference's: reset into the ROM bootloader (ClassicReset, or UsbJtagSerialReset when the USB
// product ID is 0x1001), SYNC, read the chip-detect register, the same informational register reads, upload
// Espressif's legacy flasher stub v1.3.0 (stubs/, GPL-2.0-or-later, sent to the chip as data), read the flash ID,
// optionally erase the whole chip, write the image compressed in 0x4000-byte blocks, leave flash mode, and hard
// reset with flash.js's extra RTS pulse. reference/esp32/ records the reference doing it in Node, and the tests
// compare this package's bytes, line changes and sleeps with those recordings.
//
// Ported from esptool-js 0.5.4 (src/esploader.ts, src/webserial.ts, src/reset.ts, src/targets/esp32.ts,
// esp32s3.ts, esp32c3.ts; Apache-2.0, Copyright 2019 Espressif Systems (Shanghai) CO LTD), modified as listed
// below, and from the web flasher's js/flash.js (MIT, Copyright (c) 2025 Rastislav Vysoky). Each file that ports
// code says which.
//
// # Departures from the reference
//
// Each changes what happens when something fails, not the bytes of a successful run, except the added MD5 check
// and the compression (SPEC.md 6.2, docs/meshcore-flashing.md 4.10 and 4.11):
//
//   - Boot banner: after a reset the whole banner is read and set aside before the first SYNC. esptool-js reads 1
//     byte of it, so its first SYNC trips over the rest and a second SYNC follows (bug 4.10a). Here one SYNC
//     usually does; tests collapse the leading SYNCs.
//   - Reads never drop bytes: one buffer per Loader, and each command has its own deadline (esptool-js's timeout
//     values, 4.8) instead of per-chunk timers whose expiry throws away what was read (bug 4.10b). Bytes outside a
//     SLIP frame are skipped instead of aborting with "Invalid head of packet".
//   - Connecting gives up: at most ConnectAttempts resets (default 4) or about ConnectTimeout (default 10 s), then
//     ErrNoBootloader, which tells the owner to hold BOOT, press and release RESET, and release BOOT. esptool-js
//     0.5.4 passes 115200 as the attempt count and never gives up (bug 4.10c).
//   - Every reply's status is checked (4 bytes from the ROM, 2 from the stub) and a failure is a *StatusError.
//     esptool-js 0.5.4 never looks at them (bug 4.10d).
//   - Chip guard: the chip read after SYNC must be the one the image header names (ImageChip) and, when given,
//     Options.ExpectChip; otherwise ErrWrongChip, before the stub is uploaded. Only the catalog's chips (ESP32,
//     ESP32-S3, ESP32-C3) are flashed; any other chip is named and refused with ErrUnsupportedChip, where
//     esptool-js 0.5.4 would crash on an unknown magic.
//   - Image checks before the board is touched: only the two flashes SPEC.md 6.2 allows, an update (0x10000, no
//     erase) or a full wipe (chip erase, then the image at 0x0); any other address, an erase at 0x10000 (the
//     reference can erase the chip under an app-only image and leave it unbootable, 3.6) or 0x0 without the erase
//     is refused, because on the ESP32-S3 and ESP32-C3 an app image at 0x0 looks like a merged one to the header
//     check. The image header must name a chip and sit where the address needs it (ImageChip; a merged image,
//     which has a partition table at 0x8000, goes only at 0x0); the compressed stream must inflate back to the
//     image (ErrCompress); after the flash ID is read, an image that doesn't fit the detected flash size is
//     refused with ErrTooBig. ExpectChip is read as ParseChip reads it.
//   - Verify: SPI_FLASH_MD5 over what was written, compared with the MD5 of the padded image, after the post-write
//     READ_REG and before FLASH_BEGIN(0,0), exactly where esptool-js's own calculateMD5Hash option puts it (the
//     web flasher never passes it); a mismatch is ErrVerify. This is the only proof the last block was written:
//     the reference's FLASH_BEGIN(0,0) clears the stub's deferred error (4.4). The ROM's hex form of the reply is
//     decoded too (bug 4.10e).
//   - Compression: Go's compress/zlib at best compression, not pako, so FLASH_DEFL_BEGIN's block count and the
//     FLASH_DEFL_DATA payloads differ from the reference's (4.6); the stub inflates any zlib stream. Options.Compress
//     replaces it, and the tests inject the reference's own stream to compare every byte.
//   - A stub already running (every SYNC reply's value 0): esptool-js skips the upload but keeps the ROM's framing;
//     here the stub's 2 status bytes are used from then on.
//   - On a failure after the board answered SYNC, flash.js's RTS pulse is sent (best effort) so the board leaves
//     the bootloader; flash.js pulses only once writing has started.
//   - The caller owns the port: Flash doesn't close it (flash.js closes its port at the end).
//   - ErrNoBootloader says what the board printed: its boot mode, or nothing. esptool-js's regex for that can't
//     reach "waiting for download" on the banner's next line; here it can.
//   - The context can stop a flash between reads (at most 250 ms apart); the owner's "stop" uses it.
//   - Only what flash.js uses is ported: no ROM-only writing, no CHANGE_BAUDRATE (both rates are 115200), no
//     "keep" image-header patching (with "keep" esptool-js changes no byte, 4.2).
//
// A board on TinyUSB (303a:0002) reboots into the ROM on the classic reset and comes back as a new USB device,
// 303a:1001 (docs/meshcore-flashing.md 6.2): the old port goes away, so Flash stops at StageConnecting with
// serialport.ErrGone, and the caller finds the new port by its USB serial number and flashes again with
// USBProduct 0x1001.
//
// UNVERIFIED (docs/meshcore-flashing.md), all needing a check on hardware: how Chrome's Web Serial treats DTR and
// RTS on open and close (Linux raises both on open; both resets set them explicitly first, as the reference does);
// whether the ROM's MD5 hex is lowercase (compared case-insensitively; a normal flash gets the stub's raw form);
// that UsbJtagSerialReset resets TinyUSB boards at 303a:1001 (6.2: probably not, so ErrNoBootloader's advice
// applies); that the TinyUSB re-enumeration above happens as described; and whether an ESP32-S3 flashed over
// USB-Serial-JTAG stays in download mode after the reset (4.10i: if so, unplug and replug it). The boot banner is
// taken to be over after 100 ms of quiet, an assumption about real boards' timing.
package esp
