"""Turn a framebuffer picture (RGB565 little-endian, as /dev/fb0 or a --framebuffer file holds it) into a PNG.

Standard library only (Python 3.9 or newer, no Pillow): the PNG is written by hand, scaled up by whole pixels so the
6x12 text can be read. contact_sheet puts several pictures on one PNG, each under its name, for attaching one image.

    python3 fb2png.py <picture.raw> <out.png> [--scale 2] [--width 320 --height 170 --stride 640]
"""

import argparse
import array
import struct
import sys
import zlib

WIDTH, HEIGHT, STRIDE = 320, 170, 640


def _rgb(v):
    r, g, b = v >> 11, (v >> 5) & 63, v & 31
    return bytes(((r << 3) | (r >> 2), (g << 2) | (g >> 4), (b << 3) | (b >> 2)))


def to_rgb_rows(raw, width=WIDTH, height=HEIGHT, stride=STRIDE):
    """The picture as rows of 8-bit RGB bytes."""
    need = stride * height
    px = array.array("H")
    px.frombytes(bytes(raw[:need]).ljust(need, b"\0"))
    if sys.byteorder == "big":
        px.byteswap()
    sp = stride // 2
    cache = {}
    rows = []
    for y in range(height):
        out = []
        for v in px[y * sp:y * sp + width]:
            c = cache.get(v)
            if c is None:
                c = cache[v] = _rgb(v)
            out.append(c)
        rows.append(out)
    return rows


def _scale_rows(rows, scale):
    out = []
    for row in rows:
        line = b"".join(c * scale for c in row) if scale > 1 else b"".join(row)
        out.extend([line] * scale)
    return out


def _chunk(kind, data):
    return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data) & 0xFFFFFFFF)


def encode_png(lines, width, height):
    """A PNG of 8-bit RGB lines (bytes of width*3 each)."""
    body = b"".join(b"\0" + line for line in lines)
    return (b"\x89PNG\r\n\x1a\n" + _chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0)) +
            _chunk(b"IDAT", zlib.compress(body, 9)) + _chunk(b"IEND", b""))


def png_bytes(raw, scale=2, width=WIDTH, height=HEIGHT, stride=STRIDE):
    lines = _scale_rows(to_rgb_rows(raw, width, height, stride), scale)
    return encode_png(lines, width * scale, height * scale)


def write_png(raw, path, scale=2, width=WIDTH, height=HEIGHT, stride=STRIDE):
    with open(path, "wb") as f:
        f.write(png_bytes(raw, scale, width, height, stride))


def contact_sheet(frames, path, font=None, columns=3, scale=1, width=WIDTH, height=HEIGHT, stride=STRIDE):
    """One PNG of several pictures, `columns` a row, each with its name above it in `font` (an ocr.Font, or no
    names when None). frames is a list of (name, raw)."""
    gap, label = 6, (font.h + 4 if font else 0)
    cw, ch = width * scale, height * scale
    rows_n = (len(frames) + columns - 1) // columns
    total_w = columns * cw + (columns + 1) * gap
    total_h = rows_n * (ch + label) + (rows_n + 1) * gap
    back, ink = b"\x30\x30\x30", b"\xe6\xe6\xe6"
    canvas = [bytearray(back * total_w) for _ in range(total_h)]
    for n, (name, raw) in enumerate(frames):
        x0 = gap + (n % columns) * (cw + gap)
        y0 = gap + (n // columns) * (ch + label + gap)
        if font:
            for i, c in enumerate(name[:cw // font.w]):
                g = font.glyph(c)
                for gy in range(font.h):
                    bits = int.from_bytes(g[gy * font.row_bytes:(gy + 1) * font.row_bytes], "big")
                    for gx in range(font.w):
                        if bits & (1 << (font.row_bytes * 8 - 1 - gx)):
                            x = x0 + i * font.w + gx
                            canvas[y0 + 1 + gy][x * 3:x * 3 + 3] = ink
        for y, line in enumerate(_scale_rows(to_rgb_rows(raw, width, height, stride), scale)):
            canvas[y0 + label + y][x0 * 3:x0 * 3 + len(line)] = line
    with open(path, "wb") as f:
        f.write(encode_png([bytes(r) for r in canvas], total_w, total_h))


def main():
    ap = argparse.ArgumentParser(description="RGB565 little-endian framebuffer picture to PNG.")
    ap.add_argument("raw")
    ap.add_argument("png")
    ap.add_argument("--scale", type=int, default=2)
    ap.add_argument("--width", type=int, default=WIDTH)
    ap.add_argument("--height", type=int, default=HEIGHT)
    ap.add_argument("--stride", type=int, default=STRIDE)
    a = ap.parse_args()
    with open(a.raw, "rb") as f:
        raw = f.read()
    write_png(raw, a.png, a.scale, a.width, a.height, a.stride)


if __name__ == "__main__":
    main()
