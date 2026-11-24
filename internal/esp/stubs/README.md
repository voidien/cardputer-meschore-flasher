# The flasher stub meshflash uploads to an ESP32

These three files are the flasher stubs that the MeshCore web flasher uploads into an ESP32's RAM before it writes
the flash, copied unchanged from the npm package `esptool-js@0.5.4` (`package/lib/targets/stub_flasher/`; the web
flasher's `lib/esp32.js` is that package's `bundle.js` and embeds the same bytes):

| File | Chip | text | data | entry |
| --- | --- | --- | --- | --- |
| `stub_flasher_32.json` | ESP32 | 3532 bytes @ 0x400be000 | 156 bytes @ 0x3ffdeba8 | 0x400be5ec |
| `stub_flasher_32s3.json` | ESP32-S3 | 5292 bytes @ 0x40378000 | 252 bytes @ 0x3fcb2bfc | 0x40378a80 |
| `stub_flasher_32c3.json` | ESP32-C3 | 3868 bytes @ 0x40380000 | 160 bytes @ 0x3fc96bb4 | 0x403806d0 |

They are Espressif's **legacy flasher stub v1.3.0** (esptool-legacy-flasher-stub, tag v1.3.0), as shipped in esptool
v4.8.0 and v4.8.1 (`esptool/targets/stub_flasher/1/`). That was checked by the SHA-256 of the decoded `text` and
`data` and by the entry point (`docs/meshcore-flashing.md` 1.5; the research's `tools/bundled_stubs.json`). The
SHA-256 of the decoded segments (`stub_test.go` checks them):

| Chip | text | data |
| --- | --- | --- |
| ESP32 | c3d3ddfa74e6b41710863f07a2d0496bb6f758a2011bf1d9cdd5b280479db6b9 | d28a40ec32f40afd656ddbc1a89b0cc042203548dfa255617b55067d80350f2f |
| ESP32-S3 | 14ab7c0beb0520624f7c14f4547f9c42657e651f50c19106cb510477751c8e29 | 0ed9e45f3c15a144281066dc7c50db6ae4e936bad0fea257d7453a6ec16752f5 |
| ESP32-C3 | 9b8c66b2975c18c28464869050541b847e683fe0b5345b3163176ee55c7372f7 | d8a11ba0605663eda7b3f00331ff62d904762faf91507fdfbe25ca539b1ba214 |

## Licence

The stub is **GPL-2.0-or-later**: Copyright 2016 Cesanta Software Limited, contributions 2016-2022 Espressif Systems
(Shanghai) CO LTD. Its complete source, with the GPLv2 text, is in
`third_party/esptool-legacy-flasher-stub-v1.3.0/` at the root of this repository (`LICENSE`, and
`flasher_stub/stub_flasher.c` for the copyright header).

The stub runs on the ESP32, not in meshflash: meshflash sends these bytes to the chip as data, as the web flasher
does. meshflash's own code keeps its MIT licence (see `THIRD_PARTY_NOTICES`, item 4).

Only the three chips in MeshCore's catalog are here. The other chips esptool-js 0.5.4 carries a stub for (ESP32-S2,
C2, C5, C6, C61, H2, P4, ESP8266) are detected and refused by name.
