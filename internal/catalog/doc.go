// Package catalog is what meshflash offers to flash: MeshCore's catalog from flasher.meshcore.io and ZephCore's from
// zephcore.meshcore.dev, resolved into boards, roles, versions and files exactly as the web flasher's js/catalog.js
// does it; the choice of file, address and erase for a flash (Choose); and the saved copies: the catalog as fetched
// (Client.Update, Client.Load) and each downloaded firmware file with the SHA-256 taken when it was downloaded and
// checked again before every flash (Client.Download, Client.Verify).
//
// The reference is the web flasher, flasher.meshcore.dev @ 45a5257 (MIT, (c) 2025 Rastislav Vysoky):
// js/catalog.js (loadCatalog, matchReleaseFiles, roleValue, renderNotice, firmwareUrl) is ported in resolve.go, and
// js/flash.js (pickFlashFile, esp32Address) in choose.go. reference/catalog runs that JavaScript in Node over the
// saved live catalogs in testdata; the tests require the Go resolution to equal it exactly, and every Choose to pick
// the very file and address the reference picks, except the refusals below. Research: docs/meshcore-flashing.md
// sections 2, 3, 4.2, 5.1, 9.5 and 10.4.
//
// Departures from the reference, each on purpose:
//
//   - Choose never falls back. pickFlashFile takes the first flash* file when the preferred type is missing; Choose
//     refuses instead, so the git catalog's broken second "LilyGo T-Lora Pager" (an esp32 entry holding the T-Echo
//     Card's nRF52 .zip files) is refused rather than written to an ESP32 at 0x10000 (research 3.10).
//   - The ESP32 chip erase belongs to the full wipe: Wipe is the flash-wipe -merged.bin at 0x0 with EraseChip, Update
//     the flash-update .bin at 0x10000 without. The address comes from the choice, not from esp32Address's test of
//     the title, and the erase is decided for each flash; the reference's "Erase device" box stays ticked across
//     boards and erases the chip whatever file it writes (research 3.6; SPEC.md 6.2).
//   - Files are matched to the chip: an ESP32 takes only .bin (Update: not a -merged.bin; Wipe: only a -merged.bin),
//     an nRF52 only a "flash" .zip. The reference also flashes git's inline Ripple Wio Tracker L1 Pro "flash-update"
//     .zip on nRF52; the live catalog has "flash" there. nRF52 has no Wipe: the catalog's only way is its "erase"
//     formatter, which meshflash never writes. No "erase" or "bootloader" file is exposed, and Choose refuses
//     every one either catalog names, for any board, should a version list it (research 3.9).
//   - Notices are plain text: tags stripped, entities decoded, link addresses never shown, and a link that is only
//     "Read more" dropped. A notice nested in a release source (the T1000-E's "github.notice", warning not to flash
//     the LoRaWAN version) is a warning about the board: it is first in Device.Notices and on every firmware entry
//     of the board, ZephCore's merged ones too, though the catalog nests it in one entry; the web flasher never shows
//     it (research 3.10).
//   - ZephCore's catalog is merged into MeshCore's board list by name (same name ignoring case and surrounding
//     spaces, same chip family); boards only ZephCore lists come after MeshCore's. The web flasher serves it on its
//     own site. Its firmware is marked SourceZephCore and keeps its own site for downloads.
//   - Device.SoftDevice comes from the "erase" file name (softdevice_v6 → 0x00B6, _v7 → 0x0123). On a merged board
//     MeshCore's value is kept when its name says one, else ZephCore's (the Wio Tracker L1 Pro and Xiao nRF52 erase
//     files name none in MeshCore's catalog; ZephCore's say v7). The two catalogs agree on every board both name.
//   - Regexes are Go's RE2, not JavaScript's RegExp. Every pattern in the saved catalogs gives the same matches
//     (research 3.4, and the goldens here, which include anchored literal patterns with decoy names). A pattern RE2
//     can't compile (a look-around, say), or one it would read differently from JavaScript (\A, \z, \a, \p, \Q,
//     \x{…}, back-references, POSIX classes, a set starting with "]", flag groups such as (?i); checkJSPattern),
//     makes the catalog unreadable, so an update is refused and the saved catalog stays, where the web flasher would
//     use it (or, for a flag group, fail). The two still differ on characters no file name holds: JavaScript's "."
//     also refuses \r, \u2028 and \u2029, and its \s also takes \v and Unicode spaces.
//   - A malformed catalog that js/catalog.js would choke on (no device list, a null device or firmware, a version
//     without a file list) is refused, as loadCatalog fails; a few shapes the JavaScript would wander through oddly
//     (a firmware entry that's a number, file patterns that aren't an object, a release file that isn't an object)
//     are refused too.
//   - The model keeps only what the web flasher lists: firmware entries with versions, versions with files.
//     Release notes are plain text with Windows line ends tidied; formatChangeLog's links are not made.
//   - Update fetches one file at a time (the reference fetches the release lists in parallel), and both catalogs or
//     neither: a failure anywhere leaves the saved catalog untouched. It fetches the release lists each catalog
//     names, as loadCatalog does (today github and github-ripple, and github-zephcore).
//   - Files are fetched from absolute addresses on the catalog's own site (the browser resolves firmwareUrl against
//     the page). A MeshCore release file ("?repo=github") that the flasher site fails to give is tried once from
//     GitHub's meshcore-dev/MeshCore releases; the reference has no fallback (SPEC.md 2).
//   - Downloads are checked, which the reference doesn't do (research 2.6): status 200, not a web page, not
//     compressed, the whole Content-Length, at least 50 KB, the right kind of file (an ESP32 image starts with 0xE9,
//     or for a classic ESP32's -merged.bin has it at 0x1000 after 0xFF padding; a .zip opens; a .uf2 starts with a
//     UF2 block), and GitHub's Content-Length and X-Ms-Blob-Content-Md5 when it is the fallback. Requests carry the
//     User-Agent "Mozilla/5.0 (compatible; meshflash/<version>)" (SPEC.md 2).
//
// Resting on UNVERIFIED research: that production runs flasher@45a5257's JavaScript (research 1.2); that GitHub's
// meshcore-dev/MeshCore release assets are the files flasher.meshcore.io serves for "?repo=github" (2.4); that
// GitHub's storage keeps sending X-Ms-Blob-Content-Md5 (not a documented contract, so it is checked only when
// present); whether flasher.meshcore.io sends a Content-Length (checked only when present); and the "erase" file name
// as the board's SoftDevice (5.1; the nRF52 DFU package's own init packet is the real check, in internal/nrfdfu).
package catalog
