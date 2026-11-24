# Vendored files

Byte-identical copies of two files of MeshCore's web flasher, flasher.meshcore.dev at commit 45a5257
(`45a5257bf792b11a83f4c51c18d4f312addfb677`, 26 Sep 2026), so the harness runs the reference without a checkout.
`SHA256SUMS` holds their hashes; `make record` checks them first.

| File | From | Licence |
| --- | --- | --- |
| `dfu.js` | `lib/dfu.js` | MIT, Copyright (c) 2025 Rastislav Vysoky (the flasher's licence, below). It adapts adafruit-nrfutil's `dfu_transport_serial.py`, `util.py` and `crc16.py`, BSD-3-Clause, Copyright (c) 2015 Nordic Semiconductor ASA (the text is in `THIRD_PARTY_NOTICES` at the repository root, section 2). |
| `zip.min.js` | `lib/zip.min.js` | zip.js 2.7.57, BSD-3-Clause, Copyright (c) 2023 Gildas Lormeau (below). The flasher's MIT licence doesn't cover it. |
| `package.json` | written here | `{"type":"module"}`, so Node loads the two as ES modules |

To check them against a checkout (`../../fetch-flasher.sh` puts one in `reference/.flasher`):

```bash
cmp dfu.js ../../.flasher/lib/dfu.js && cmp zip.min.js ../../.flasher/lib/zip.min.js
```

## The flasher's licence (dfu.js)

```
MIT License

Copyright (c) 2025 Rastislav Vysoky

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## zip.js's licence (zip.min.js)

```
BSD 3-Clause License

Copyright (c) 2023, Gildas Lormeau

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

1. Redistributions of source code must retain the above copyright notice, this
   list of conditions and the following disclaimer.

2. Redistributions in binary form must reproduce the above copyright notice,
   this list of conditions and the following disclaimer in the documentation
   and/or other materials provided with the distribution.

3. Neither the name of the copyright holder nor the names of its
   contributors may be used to endorse or promote products derived from
   this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS"
AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE
IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```
