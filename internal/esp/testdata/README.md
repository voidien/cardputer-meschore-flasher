# internal/esp/testdata

Recorded by `reference/esp32` (`make record`; its README says how): the web flasher's own JavaScript (flasher.meshcore.dev
at 45a5257, esptool-js 0.5.4) flashing a fake ESP32 in Node.

- `*.bin`: small synthetic ESP images (an image header with the chip's ID, then pseudo-random bytes from a fixed
  seed), made by `reference/esp32/mkimages.mjs`. Not firmware.
- `<case>.jsonl.gz` and `<case>-md5.jsonl.gz`: one JSON object per event (`open`, `signals`, `sleep`, `tx`, `rx`,
  `close`, …), the second with esptool-js's MD5 verify switched on.

The recordings include the bytes of Espressif's legacy flasher stub v1.3.0 as the reference uploads it (MEM_DATA).
The stub is GPL-2.0-or-later (Copyright 2016 Cesanta Software Limited, contributions 2016-2022 Espressif Systems);
its complete source and licence are in `third_party/esptool-legacy-flasher-stub-v1.3.0/` at the root of this
repository.
