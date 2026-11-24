package nrfdfu

// The package format is adafruit-nrfutil's (nordicsemi/dfu/package.py, manifest.py, init_packet.py; BSD-3-Clause,
// Copyright (c) 2015 Nordic Semiconductor ASA), read as the OTAFIX bootloader reads it (src/dfu_init.c). CRC16 is
// ported from the web flasher's lib/dfu.js calcCrc16 (flasher.meshcore.dev@45a5257, MIT, Copyright (c) 2025
// Rastislav Vysoky), itself from nrfutil's crc16.py. See THIRD_PARTY_NOTICES.

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
)

// The checks that refuse a package. Each error OpenPackage or CheckSoftDevice returns wraps one of these, with the
// detail a person needs ("its size, 1027 bytes, is not a multiple of 4").
var (
	// ErrNotPackage means the file isn't a DFU package at all: not a zip, or no readable manifest.json.
	ErrNotPackage = errors.New("nrfdfu: not a DFU package")
	// ErrNotApplication means the package holds something besides an application: a SoftDevice, a bootloader or
	// both (the catalog's OTAFIX bootloaders), which meshflash never writes (docs/meshcore-flashing.md 3.9 rule 3).
	ErrNotApplication = errors.New("nrfdfu: not an application-only package")
	// ErrMissingFile means the manifest names a file the zip doesn't have.
	ErrMissingFile = errors.New("nrfdfu: the package is incomplete")
	// ErrBadImage means firmware.bin can't be flashed: empty, too big, or not a whole number of 32-bit words.
	ErrBadImage = errors.New("nrfdfu: the firmware image can't be flashed")
	// ErrBadInitPacket means firmware.dat isn't an nRF52 init packet the bootloader would accept.
	ErrBadInitPacket = errors.New("nrfdfu: the init packet (.dat) is not for an nRF52")
	// ErrCRCMismatch means firmware.bin's CRC-16 isn't the one in the init packet: the file is damaged, or the two
	// don't belong together. The bootloader would only find out at STOP, after erasing the app.
	ErrCRCMismatch = errors.New("nrfdfu: the firmware doesn't match its init packet's CRC")
	// ErrWrongSoftDevice means the firmware was built for a SoftDevice the board doesn't have. The bootloader would
	// refuse its init packet after erasing the app.
	ErrWrongSoftDevice = errors.New("nrfdfu: the firmware needs a different SoftDevice")
	// ErrSoftDeviceUnchecked is Flash refusing a package that CheckSoftDevice hasn't passed for the board. Of all the
	// checks, the SoftDevice is the one whose failure costs most (the app erased, and an OTAFIX board left in
	// Bluetooth DFU mode with no USB port), so it can't be left to the caller to remember.
	ErrSoftDeviceUnchecked = errors.New("nrfdfu: the package wasn't checked against the board's SoftDevice (Package.CheckSoftDevice)")
)

// SoftDevice firmware IDs, as an init packet's sd_req lists them (docs/meshcore-flashing.md 5.1).
const (
	SoftDeviceS140v6 uint16 = 0x00B6 // S140 6.1.1, on most MeshCore nRF52 boards
	SoftDeviceS140v7 uint16 = 0x0123 // S140 7.3.0
	// SoftDeviceAny in sd_req means any SoftDevice will do (dfu_init.c DFU_SOFTDEVICE_ANY).
	SoftDeviceAny uint16 = 0xFFFE
)

// DeviceTypeNRF52 is the device type every Adafruit nRF52 bootloader requires in an init packet (dfu_init.c
// ADAFRUIT_DEVICE_TYPE); MeshCore's packages are built with --dev-type 0x0052.
const DeviceTypeNRF52 uint16 = 0x0052

// Limits on what OpenPackage reads, so a damaged or hostile zip can't make it allocate without bound.
const (
	// MaxImage is the nRF52840's whole flash, 1 MiB: no application can be bigger (the bootloader's own limit,
	// DFU_IMAGE_MAX_SIZE_FULL, is smaller and depends on the SoftDevice; it refuses a bigger image at START, before
	// erasing anything).
	MaxImage = 1 << 20
	// maxInitPacket: the bootloader keeps the init packet in a 64-byte buffer (OTAFIX dfu_single_bank.c:34), and
	// dfu.js adds 2 bytes to it.
	maxInitPacket = 62
	maxManifest   = 64 << 10
)

// InitPacket is firmware.dat decoded: the legacy (dfu_version 0.5) init packet adafruit-nrfutil builds
// (nordicsemi/dfu/init_packet.py) and the bootloader checks (OTAFIX src/dfu_init.c), all little-endian.
type InitPacket struct {
	// DeviceType must be DeviceTypeNRF52.
	DeviceType uint16
	// DeviceRev is checked only for SoftDevice and bootloader updates; MeshCore's is 0xFFFF.
	DeviceRev uint16
	// AppVersion isn't checked by the bootloader; MeshCore's is 0xFFFFFFFF.
	AppVersion uint32
	// SDReq lists the SoftDevice firmware IDs the application runs on; the board's must be among them (or
	// SoftDeviceAny), or the bootloader refuses the init packet after the erase.
	SDReq []uint16
	// CRC16 is the CRC-16/CCITT-FALSE of firmware.bin, which the bootloader checks at STOP.
	CRC16 uint16
}

// Package is an application DFU package (the catalog's nRF52 .zip), as OpenPackage checked it. Flash takes it once
// CheckSoftDevice has matched it to the board.
type Package struct {
	// Bin is the application image (firmware.bin), sent in DATA frames.
	Bin []byte
	// Dat is the init packet (firmware.dat) exactly as the zip holds it: INIT sends these bytes.
	Dat []byte
	// Init is Dat decoded.
	Init InitPacket

	// sdChecked and sdFWID are the last CheckSoftDevice that passed: Flash refuses the package without one, and
	// checks the board's SoftDevice against Dat (the bytes it sends) again.
	sdChecked bool
	sdFWID    uint16
}

// manifest is manifest.json as adafruit-nrfutil writes it: {"manifest": {"application": {...}, "dfu_version": 0.5}}.
type manifest struct {
	Manifest map[string]json.RawMessage `json:"manifest"`
}

type manifestImage struct {
	BinFile string `json:"bin_file"`
	DatFile string `json:"dat_file"`
}

// OpenPackage reads and checks a DFU package before anything touches the board (dfu.js opens the port first and
// checks nothing; nrfutil parses first, docs/meshcore-flashing.md 5.1). It refuses:
//   - anything but a zip with a manifest.json (ErrNotPackage);
//   - a manifest without "application", or with "softdevice", "bootloader", "softdevice_bootloader" or any other
//     part (ErrNotApplication: section 3.9 rule 3);
//   - a missing .bin or .dat (ErrMissingFile);
//   - an image that's empty, over MaxImage, or not a multiple of 4 bytes (ErrBadImage: the bootloader takes only
//     whole words, OTAFIX dfu_single_bank.c:297-299);
//   - an init packet that's under 14 bytes, has the wrong length for its sd_req list, an empty sd_req, or a
//     device type other than 0x0052 (ErrBadInitPacket);
//   - an image whose CRC-16 isn't the init packet's (ErrCRCMismatch).
//
// The board's SoftDevice is checked separately, with CheckSoftDevice, which Flash requires.
func OpenPackage(zipBytes []byte) (*Package, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, fmt.Errorf("%w: not a zip file (%v)", ErrNotPackage, err)
	}
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		if _, dup := files[f.Name]; dup {
			// Which copy would be flashed is a guess (zip.js keeps the last one); a real package never has two.
			return nil, fmt.Errorf("%w: it has two files named %q", ErrNotPackage, f.Name)
		}
		files[f.Name] = f
	}
	mf, ok := files["manifest.json"]
	if !ok {
		return nil, fmt.Errorf("%w: no manifest.json in the zip", ErrNotPackage)
	}
	raw, err := readEntry(mf, maxManifest)
	if err != nil {
		return nil, fmt.Errorf("%w: manifest.json: %v", ErrNotPackage, err)
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil || m.Manifest == nil {
		return nil, fmt.Errorf("%w: manifest.json is not a DFU manifest", ErrNotPackage)
	}
	keys := make([]string, 0, len(m.Manifest))
	for key := range m.Manifest {
		keys = append(keys, key)
	}
	sort.Strings(keys) // the same message for the same file
	for _, key := range keys {
		switch key {
		case "application", "dfu_version":
		case "softdevice":
			return nil, fmt.Errorf("%w: it holds a SoftDevice, which meshflash never writes", ErrNotApplication)
		case "bootloader":
			return nil, fmt.Errorf("%w: it holds a bootloader, which meshflash never writes", ErrNotApplication)
		case "softdevice_bootloader":
			return nil, fmt.Errorf("%w: it holds a SoftDevice and bootloader, which meshflash never writes", ErrNotApplication)
		default:
			return nil, fmt.Errorf("%w: it holds %q, which meshflash doesn't know", ErrNotApplication, key)
		}
	}
	appRaw, ok := m.Manifest["application"]
	if !ok {
		return nil, fmt.Errorf("%w: the manifest has no application", ErrNotApplication)
	}
	var app manifestImage
	if err := json.Unmarshal(appRaw, &app); err != nil {
		return nil, fmt.Errorf("%w: the manifest's application is not readable", ErrNotPackage)
	}
	// dfu.js loads only entries whose names end in .bin and .dat (dfu.js:399), so the manifest must name those.
	if !strings.HasSuffix(app.BinFile, ".bin") || !strings.HasSuffix(app.DatFile, ".dat") {
		return nil, fmt.Errorf("%w: the manifest names %q and %q, not a .bin and a .dat", ErrNotPackage, app.BinFile, app.DatFile)
	}
	binFile, ok := files[app.BinFile]
	if !ok {
		return nil, fmt.Errorf("%w: %s is missing", ErrMissingFile, app.BinFile)
	}
	datFile, ok := files[app.DatFile]
	if !ok {
		return nil, fmt.Errorf("%w: %s is missing", ErrMissingFile, app.DatFile)
	}
	bin, err := readEntry(binFile, MaxImage)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrBadImage, app.BinFile, err)
	}
	dat, err := readEntry(datFile, maxInitPacket)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrBadInitPacket, app.DatFile, err)
	}
	p := &Package{Bin: bin, Dat: dat}
	ip, err := p.check()
	if err != nil {
		return nil, err
	}
	p.Init = ip
	return p, nil
}

// errTooBig is readEntry's refusal of an entry over its limit.
var errTooBig = errors.New("too big")

// readEntry reads one zip entry, refusing more than limit bytes whatever its header claims.
func readEntry(f *zip.File, limit int) ([]byte, error) {
	if f.UncompressedSize64 > uint64(limit) {
		return nil, fmt.Errorf("%w (%d bytes, at most %d)", errTooBig, f.UncompressedSize64, limit)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(b) > limit {
		return nil, fmt.Errorf("%w (more than %d bytes)", errTooBig, limit)
	}
	return b, nil
}

// check is every test OpenPackage makes on the image and init packet, and returns the init packet decoded; Flash
// runs it again so a Package built by hand can't skip it.
func (p *Package) check() (InitPacket, error) {
	switch {
	case len(p.Bin) == 0:
		return InitPacket{}, fmt.Errorf("%w: it is empty", ErrBadImage)
	case len(p.Bin) > MaxImage:
		return InitPacket{}, fmt.Errorf("%w: its size, %d bytes, is more than the chip's flash", ErrBadImage, len(p.Bin))
	case len(p.Bin)%4 != 0:
		return InitPacket{}, fmt.Errorf("%w: its size, %d bytes, is not a multiple of 4", ErrBadImage, len(p.Bin))
	}
	ip, err := ParseInitPacket(p.Dat)
	if err != nil {
		return InitPacket{}, err
	}
	if ip.DeviceType != DeviceTypeNRF52 {
		return InitPacket{}, fmt.Errorf("%w: its device type is 0x%04X, not 0x0052", ErrBadInitPacket, ip.DeviceType)
	}
	if crc := CRC16(p.Bin); crc != ip.CRC16 {
		return InitPacket{}, fmt.Errorf("%w: the image's CRC-16 is 0x%04X, the init packet says 0x%04X", ErrCRCMismatch, crc, ip.CRC16)
	}
	return ip, nil
}

// ParseInitPacket decodes a legacy init packet: device type, device revision, application version, the sd_req
// count and list, then the image's CRC-16 (nrfutil init_packet.py; OTAFIX dfu_init.c reads the CRC as the first two
// bytes after the sd_req list). It refuses a packet under 14 bytes, an empty sd_req (the bootloader would refuse it
// whatever SoftDevice the board has), and any length other than 12 + 2 × the sd_req count: a CRC that isn't the
// last two bytes means a format meshflash doesn't know (a hash, say), so it isn't guessed at.
func ParseInitPacket(dat []byte) (InitPacket, error) {
	if len(dat) < 14 {
		return InitPacket{}, fmt.Errorf("%w: it is %d bytes, at least 14 expected", ErrBadInitPacket, len(dat))
	}
	if len(dat) > maxInitPacket {
		return InitPacket{}, fmt.Errorf("%w: it is %d bytes, more than the bootloader takes", ErrBadInitPacket, len(dat))
	}
	le := binary.LittleEndian
	n := int(le.Uint16(dat[8:]))
	if n == 0 {
		return InitPacket{}, fmt.Errorf("%w: it lists no SoftDevice", ErrBadInitPacket)
	}
	if want := 12 + 2*n; len(dat) != want {
		return InitPacket{}, fmt.Errorf("%w: it is %d bytes, but %d SoftDevice IDs and a CRC make %d", ErrBadInitPacket, len(dat), n, want)
	}
	ip := InitPacket{
		DeviceType: le.Uint16(dat[0:]),
		DeviceRev:  le.Uint16(dat[2:]),
		AppVersion: le.Uint32(dat[4:]),
		SDReq:      make([]uint16, n),
		CRC16:      le.Uint16(dat[10+2*n:]),
	}
	for i := range ip.SDReq {
		ip.SDReq[i] = le.Uint16(dat[10+2*i:])
	}
	return ip, nil
}

// CheckSoftDevice checks the package runs on the board's SoftDevice, given as its firmware ID (from the catalog:
// SoftDeviceS140v6 or SoftDeviceS140v7). The bootloader makes the same check only after START has erased the app,
// and on an OTAFIX bootloader a refusal leaves the board in Bluetooth DFU mode with no USB port
// (docs/meshcore-flashing.md 5.6). fwid 0 means the board's SoftDevice isn't known: the package is allowed, and note
// says so for the owner to see.
//
// It reads the sd_req list from Dat, the bytes INIT sends, not from Init, so a Package whose two disagree can't
// pass. Flash refuses a package this hasn't passed (ErrSoftDeviceUnchecked); a refusal undoes an earlier pass.
func (p *Package) CheckSoftDevice(fwid uint16) (note string, err error) {
	p.sdChecked = false
	ip, err := ParseInitPacket(p.Dat)
	if err != nil {
		return "", err
	}
	if !sdAccepts(ip.SDReq, fwid) {
		return "", fmt.Errorf("%w: it needs %s; this board has %s", ErrWrongSoftDevice, sdList(ip.SDReq), sdName(fwid))
	}
	p.sdChecked, p.sdFWID = true, fwid
	if fwid == 0 {
		return fmt.Sprintf("This board's SoftDevice isn't known, so meshflash can't check the firmware is built for it (it needs %s).",
			sdList(ip.SDReq)), nil
	}
	return "", nil
}

// sdAccepts is the bootloader's sd_req test (dfu_init.c dfu_init_prevalidate): the board's SoftDevice, or
// SoftDeviceAny, is listed. fwid 0 (not known) is let through, as CheckSoftDevice documents.
func sdAccepts(sdReq []uint16, fwid uint16) bool {
	if fwid == 0 {
		return true
	}
	for _, s := range sdReq {
		if s == fwid || s == SoftDeviceAny {
			return true
		}
	}
	return false
}

// sdName names a SoftDevice firmware ID for people.
func sdName(id uint16) string {
	switch id {
	case SoftDeviceS140v6:
		return "S140 6.1.1 (0x00B6)"
	case SoftDeviceS140v7:
		return "S140 7.3.0 (0x0123)"
	case SoftDeviceAny:
		return "any SoftDevice"
	}
	return fmt.Sprintf("SoftDevice 0x%04X", id)
}

func sdList(ids []uint16) string {
	names := make([]string, len(ids))
	for i, id := range ids {
		names[i] = sdName(id)
	}
	return strings.Join(names, " or ")
}

// CRC16 is CRC-16/CCITT-FALSE (polynomial 0x1021, initial value 0xFFFF), in the byte-wise form dfu.js
// (calcCrc16, dfu.js:68-80), nrfutil (crc16.py) and the bootloader (crc16.c) share. CRC16([]byte("123456789")) is
// 0x29B1.
func CRC16(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc = crc>>8 | crc<<8
		crc ^= uint16(b)
		crc ^= (crc & 0xFF) >> 4
		crc ^= crc << 12
		crc ^= (crc & 0xFF) << 5
	}
	return crc
}
