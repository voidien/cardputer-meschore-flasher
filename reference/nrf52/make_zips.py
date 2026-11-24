# Builds the synthetic DFU packages internal/nrfdfu's tests and the reference recordings use, into <outdir>.
# Run with python3 -I. Extended from the research prototype's make_tiny_zip.py: every zip is built with fixed
# timestamps and attributes, so the same script gives the same bytes on every run.
#
#   tiny-1028.zip    1028 bytes, fw[i] = (7i + 0xC0) & 0xFF: 3 DATA frames, with 0xC0 and 0xDB bytes to escape
#   tiny-4096.zip    4096 bytes, the same pattern: exactly 8 DATA frames (two page-write sleeps back to back)
#   synth-40000.zip  40000 bytes from a fixed LCG: 79 DATA frames (the sequence number wraps ten times), an erase
#                    wait above dfu.js's 500 ms floor, and entries deflated rather than stored
#   sd-bl.zip        a SoftDevice + bootloader package laid out like the OTAFIX ones: dfu.js opens the port and then
#                    fails; meshflash refuses it before touching the port
#
# The packages are laid out like MeshCore's (adafruit-nrfutil dfu genpkg, dfu_version 0.5): manifest.json,
# firmware.dat (the 14-byte init packet) and firmware.bin. No real firmware is used.
import json, struct, sys, zipfile

out = sys.argv[1]

def crc16(data, crc=0xFFFF):
    # CRC-16/CCITT-FALSE, as nrfutil's crc16.py, dfu.js and the bootloader compute it
    for b in data:
        crc = ((crc >> 8) & 0xFF) | ((crc << 8) & 0xFF00); crc ^= b; crc ^= (crc & 0xFF) >> 4
        crc ^= (crc << 12) & 0xFFFF; crc ^= ((crc & 0xFF) << 5) & 0xFFFF
    return crc

def pattern(n):
    return bytes(((i * 7) + 0xC0) & 0xFF for i in range(n))

def lcg(n, seed=0x4D455348):  # "MESH"
    out, x = bytearray(), seed
    for _ in range(n):
        x = (x * 1103515245 + 12345) & 0xFFFFFFFF
        out.append((x >> 16) & 0xFF)
    return bytes(out)

def entry(z, name, data, compress):
    info = zipfile.ZipInfo(name, date_time=(2026, 11, 24, 0, 0, 0))
    info.compress_type = compress
    info.create_system = 3
    info.external_attr = 0o644 << 16
    z.writestr(info, data)

def app_zip(path, fw, sd_req=(0x00B6,), compress=zipfile.ZIP_STORED):
    c = crc16(fw)
    dat = struct.pack('<HHIH', 0x0052, 0xFFFF, 0xFFFFFFFF, len(sd_req)) + b''.join(struct.pack('<H', s) for s in sd_req) + struct.pack('<H', c)
    man = {"manifest": {"application": {"bin_file": "firmware.bin", "dat_file": "firmware.dat",
           "init_packet_data": {"application_version": 4294967295, "device_revision": 65535, "device_type": 82,
           "firmware_crc16": c, "softdevice_req": list(sd_req)}}, "dfu_version": 0.5}}
    with zipfile.ZipFile(path, 'w') as z:
        entry(z, 'manifest.json', json.dumps(man, indent=4), compress)
        entry(z, 'firmware.dat', dat, compress)
        entry(z, 'firmware.bin', fw, compress)
    print(path, 'bin', len(fw), 'crc16 0x%04x' % c, 'dat', dat.hex())

def sd_bl_zip(path):
    img = pattern(1024)
    dat = struct.pack('<HHIHHH', 0x0052, 52840, 0xFFFFFFFF, 1, 0xFFFE, crc16(img))
    man = {"manifest": {"dfu_version": 0.5, "softdevice_bootloader": {"bin_file": "sd_bl.bin", "dat_file": "sd_bl.dat",
           "sd_size": 512, "bl_size": 512}}}
    with zipfile.ZipFile(path, 'w') as z:
        entry(z, 'manifest.json', json.dumps(man, indent=4), zipfile.ZIP_STORED)
        entry(z, 'sd_bl.dat', dat, zipfile.ZIP_STORED)
        entry(z, 'sd_bl.bin', img, zipfile.ZIP_STORED)
    print(path, 'softdevice_bootloader', len(img))

app_zip(out + '/tiny-1028.zip', pattern(1028))
app_zip(out + '/tiny-4096.zip', pattern(4096))
app_zip(out + '/synth-40000.zip', lcg(40000), compress=zipfile.ZIP_DEFLATED)
sd_bl_zip(out + '/sd-bl.zip')
