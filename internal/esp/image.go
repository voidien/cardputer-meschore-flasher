package esp

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// AppAddress is where MeshCore's app-only ESP32 images go (the catalog's "flash-update" files); merged images
// ("flash-wipe") go at 0x0 (js/flash.js ESP32_APP_ADDRESS and esp32Address).
const AppAddress = 0x10000

// imageMagic starts every ESP image header (esp_image_header_t).
const imageMagic = 0xE9

var errNoHeader = errors.New("no image header")

// ImageChip says which chip an ESP firmware image is built for, from the chip ID in its image header, so a file can
// be matched to the board before anything touches it (docs/meshcore-flashing.md 2.6 item 3 and 4.9). address is
// where the image will be written: an image for 0x10000 (an app) starts with its header, and one with a partition
// table at 0x8000 (a merged image) is refused there; an image for 0x0 must have an image header at the chip's
// bootloader offset, 0x1000 on the ESP32 and 0x0 on the ESP32-S3 and ESP32-C3, and when it reaches 0x10000 its app
// there must be for the same chip.
//
// At 0x0 the header can't tell a merged image from an app on the ESP32-S3 and ESP32-C3: both start with an image
// header for the chip, and ZephCore's "simple boot" wipe images are an app at 0x0 by design (its README), so no
// content check is made there. Flash ties 0x0 to the full wipe instead (Options).
func ImageChip(data []byte, address uint32) (Chip, error) {
	if address != 0 {
		d, err := headerChip(data, 0)
		if errors.Is(err, errNoHeader) {
			return "", fmt.Errorf("%w: no ESP image header at its start", ErrImage)
		}
		if err != nil {
			return "", err
		}
		if hasPartitionTable(data) {
			// An S3 or C3 merged image starts with a header too (its bootloader's); its partition table gives it
			// away. Written at 0x10000 it would put a bootloader where the app belongs.
			return "", fmt.Errorf("%w: this is a merged image (it has a partition table at 0x8000); it goes at 0x0", ErrImage)
		}
		return d.chip, nil
	}
	for _, off := range []uint32{0x0, 0x1000, 0x2000} {
		d, err := headerChip(data, off)
		if errors.Is(err, errNoHeader) {
			continue
		}
		if err != nil {
			return "", err
		}
		if d.bootloader != off {
			return "", fmt.Errorf("%w: an %s image written at 0x0 needs its bootloader at 0x%x, but this one has it at 0x%x",
				ErrImage, d.name, d.bootloader, off)
		}
		if app, err := headerChip(data, AppAddress); err == nil && app != d {
			return "", fmt.Errorf("%w: its bootloader is for the %s but its app is for the %s", ErrImage, d.name, app.name)
		}
		return d.chip, nil
	}
	return "", fmt.Errorf("%w: no ESP image header at 0x0, 0x1000 or 0x2000", ErrImage)
}

// headerChip reads the image header at off: magic E9, a plausible segment count, and the chip ID at offset 12 of
// the extended header.
func headerChip(data []byte, off uint32) (*chipDef, error) {
	if uint64(len(data)) < uint64(off)+24 || data[off] != imageMagic {
		return nil, errNoHeader
	}
	h := data[off : off+24]
	if h[1] == 0 || h[1] > 16 {
		return nil, fmt.Errorf("%w: the header at 0x%x claims %d segments", ErrImage, off, h[1])
	}
	id := int(binary.LittleEndian.Uint16(h[12:14]))
	d := chipByImageID(id)
	if d == nil {
		return nil, fmt.Errorf("%w: the header at 0x%x names an unknown chip (ID %d)", ErrImage, off, id)
	}
	return d, nil
}

// hasPartitionTable says whether a merged image's partition table sits at 0x8000: an entry's magic AA 50, a type
// of app (0) or data (1), and a partition offset on a 4 KB boundary past the table (ESP-IDF's
// esp_partition_info_t; docs/meshcore-flashing.md 4.9).
func hasPartitionTable(data []byte) bool {
	const at = 0x8000
	if len(data) < at+32 || data[at] != 0xAA || data[at+1] != 0x50 || data[at+2] > 1 {
		return false
	}
	off := binary.LittleEndian.Uint32(data[at+4:])
	return off >= at+0x1000 && off%0x1000 == 0
}

// padImage (esptool-js 0.5.4 src/util.ts padTo, Apache-2.0, Espressif; modified: Go) pads the image with 0xFF to a multiple of 4 bytes, as esptool-js's writeFlash does (padTo(image, 4)):
// the bytes written are exactly the file plus that padding (docs/meshcore-flashing.md 4.2).
func padImage(data []byte) []byte {
	out := append([]byte(nil), data...)
	for len(out)%4 != 0 {
		out = append(out, 0xFF)
	}
	return out
}

// Compressor turns the padded image into the zlib stream sent in FLASH_DEFL_DATA blocks. The stub inflates any
// valid zlib stream, so the choice changes only which bytes go over the wire, not what lands in the flash.
type Compressor func(data []byte) ([]byte, error)

// ZlibCompress is the default Compressor: Go's compress/zlib at its best compression. The web flasher uses pako at
// level 9, whose stream (equal to zlib 1.3's) is a few percent smaller and not byte-equal (4.6).
func ZlibCompress(data []byte) ([]byte, error) {
	var b bytes.Buffer
	w, err := zlib.NewWriterLevel(&b, zlib.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// writeBlockSize is FLASH_WRITE_SIZE: esptool-js 0.5.4 sends 0x4000-byte FLASH_DEFL_DATA blocks (the stub's
// MAX_WRITE_BLOCK).
const writeBlockSize = 0x4000

// inflateMarks checks that the compressed stream z inflates to exactly want, and returns, for each writeBlockSize
// block of z, how many bytes of output the stream has given once that block is in. esptool-js counts the same with
// an incremental Inflate (chunkSize 1) to size each block's timeout; the counts also say how much of the image the
// board has acknowledged. Each count inflates the stream up to that block's end, which is quadratic in the number
// of blocks but takes well under a second for a MeshCore image, and is exact.
func inflateMarks(z, want []byte) ([]int, error) {
	r, err := zlib.NewReader(bytes.NewReader(z))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCompress, err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCompress, err)
	}
	if !bytes.Equal(got, want) {
		return nil, fmt.Errorf("%w: the stream inflates to %d bytes that differ from the %d-byte image", ErrCompress, len(got), len(want))
	}
	n := (len(z) + writeBlockSize - 1) / writeBlockSize
	marks := make([]int, n)
	for k := 0; k < n-1; k++ {
		marks[k] = inflatedPrefix(r, z[:(k+1)*writeBlockSize])
	}
	if n > 0 {
		marks[n-1] = len(want)
	}
	return marks, nil
}

// inflatedPrefix is how many bytes the start of a zlib stream inflates to: Go's inflater hands over everything it
// decoded before it ran out of input, as pako does when fed the stream block by block.
func inflatedPrefix(r io.ReadCloser, prefix []byte) int {
	if err := r.(zlib.Resetter).Reset(bytes.NewReader(prefix), nil); err != nil {
		return 0
	}
	n, _ := io.Copy(io.Discard, r)
	return int(n)
}
