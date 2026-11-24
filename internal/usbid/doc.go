// Package usbid says which LoRa boards are plugged in, from what Linux shows in sysfs about each USB serial port,
// without ever opening the port.
//
//   - Scan lists /dev/ttyACM* and /dev/ttyUSB* with the USB identity of the device behind each one.
//   - Classify says what kind of USB serial a port is: an ESP32's own USB-Serial-JTAG, an ESP32-S3's TinyUSB serial,
//     a USB-to-UART bridge chip, an nRF52 running its app, or an nRF52 in its bootloader.
//   - Candidates gives the catalog boards that identity could be, from the table in table.go.
//   - SameBoard follows one physical board across a reset that makes it re-enumerate.
//   - DialoutStatus says whether this process may open serial ports at all, and what the owner runs if not.
//
// A USB identity only narrows the board to a set. Several boards share one VID:PID, a radio variant never shows on
// USB, and an ESP32-S3 on its own USB-Serial-JTAG looks the same in the app and in ROM download mode. So Candidates
// is never more certain than the table, rows nobody has checked on hardware are marked unverified, and the owner
// always picks the board (SPEC.md §4, "Choose board"). The facts come from docs/meshcore-flashing.md §6 and the
// research behind it (usb-ids.md); each table row names its source.
//
// Departures from the reference (the web flasher, flasher.meshcore.dev @ 45a5257). None of them changes a byte sent
// to a board:
//
//   - The reference identifies nothing. Every port picker is requestPort() with no filters (js/app.js:174,218,264,
//     296) and the owner picks the board from the catalog by hand. Suggesting boards is an extension: it orders and
//     marks the list and never picks for the owner, because a USB identity can't tell a radio variant apart and
//     flashing the wrong one gives a node that doesn't work (research §6.5).
//   - The reference reads only the PID, through WebSerial: esptool-js 0.5.4 chooses its reset by PID == 0x1001
//     (esploader.ts:140,584). Classify tells the chip's real USB-Serial-JTAG from a TinyUSB serial that borrows
//     303a:1001 by the product string, as the research recommends (§6.2), because the two need different resets.
//     Code that must send the reference's bytes keeps choosing its reset by PID; Kind is for the screens and for
//     explaining a reset that didn't work.
//   - After the nRF52 1200-baud touch the reference asks the owner to pick the bootloader's port by hand, with no
//     waiting or matching (research §5.2). SameBoard lets meshflash follow the board itself, by USB serial number or,
//     for bridges with no useful serial, by USB port (SPEC.md §6.1).
//   - DialoutStatus has no counterpart: a browser asks for each port itself, and the CardputerZero's user isn't in
//     the dialout group until the owner adds it (SPEC.md §9.3).
//
// Nothing here is ported from the web flasher, esptool-js or nrfutil. The table holds facts read from MeshCore's
// board files (MIT), the OTAFIX nRF52 bootloader's board files (MIT), arduino-esp32 2.0.17 and the vendors' pages.
package usbid
