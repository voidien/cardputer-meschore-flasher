"""Read a framebuffer picture of meshflash's screen back as text, cell by cell.

meshflash --screen draws a grid of cells in a PSF bitmap font, centred on the LCD (internal/screen: NewScreen, drawRow),
each cell one colour on another. This finds the grid the same way, and for each cell tries each of its colours as the
background: the pixels that differ make a bitmap, which is looked up among the font's glyphs. The font is the one the
picture was drawn in (on the CardputerZero, a copy of /usr/share/consolefonts/Uni2-Terminus12x6.psf.gz), PSF1 or PSF2,
gzipped or not. Like internal/screen it adds its two stand-ins for a 6x12 font that lacks them (font.go, AddStandIns),
and a character the font lacks is drawn as the font's "?", so it reads back as "?". A cell that matches nothing
reads as "¤" (the launcher's home screen, say, which isn't a text grid).

Standard library only (Python 3.9 or newer), so it runs on the T14 and on the device.

    python3 ocr.py <picture.raw> <font.psf[.gz]> [--width 320 --height 170 --stride 640]
"""

import argparse
import array
import gzip
import struct
import sys

WIDTH, HEIGHT, STRIDE = 320, 170, 640  # the CardputerZero's LCD: RGB565 little-endian, 640 bytes a row
UNKNOWN = "¤"

# Every character beyond ASCII that meshflash's screens use (internal/screen/testpage.go, Glyphs): preferred when a
# glyph stands for several characters, so "·" reads as "·" and not as another character drawn the same.
PREFERRED = "“” ‘’ – — … ▸ ● ○ · ¢ • → ✓ ✎"

# internal/screen/font.go AddStandIns: drawn for a 6x12 font that lacks them ('#' is ink).
STAND_INS = {
    "○": ["......", "......", "......", "..##..", ".#..#.", "#....#",
          "#....#", ".#..#.", "..##..", "......", "......", "......"],
    "✎": ["......", "....#.", "...#.#", "..#.#.", "..##..", ".#.#..",
          ".##...", ".#....", "#.....", "......", "......", "......"],
}


class Font:
    """A PSF font: W x H glyphs, RowBytes bytes a row, the leftmost pixel in the top bit, and its Unicode table."""

    def __init__(self, w, h, glyphs, chars):
        self.w, self.h = w, h
        self.row_bytes = (w + 7) // 8
        self.glyphs = glyphs  # list of bytes, one per glyph
        self.index = {}       # character -> glyph number (the first one, as internal/screen's Font.add)
        for i, cs in enumerate(chars):
            for c in cs:
                self.index.setdefault(c, i)
        self.extra = {}
        if w == 6 and h == 12:
            for c, rows in STAND_INS.items():
                if c not in self.index:
                    self.extra[c] = _rows6(rows)
        # Bitmap -> character, for reading back: ASCII first, then meshflash's own glyphs, then the rest.
        self.char_of = {bytes(self.row_bytes * h): " "}
        order = [chr(c) for c in range(0x20, 0x7F)] + [c for c in PREFERRED if c != " "]
        order += sorted(c for c in self.index if c not in order)
        for c in order:
            if c in self.index:
                self.char_of.setdefault(self.glyphs[self.index[c]], c)
        for c, g in self.extra.items():
            self.char_of.setdefault(g, c)

    def has(self, c):
        return c in self.index or c in self.extra

    def glyph(self, c):
        """What internal/screen draws for c (Font.Glyph): the font's, else a stand-in, else the font's "?"."""
        if c in self.index:
            return self.glyphs[self.index[c]]
        if c in self.extra:
            return self.extra[c]
        if "?" in self.index:
            return self.glyphs[self.index["?"]]
        return bytes(self.row_bytes * self.h)


def _rows6(rows):
    out = bytearray()
    for line in rows:
        b = 0
        for x, ch in enumerate(line[:8]):
            if ch == "#":
                b |= 0x80 >> x
        out.append(b)
    return bytes(out)


def load_font(path):
    """Reads a PSF1 or PSF2 font with its Unicode table, gzipped or not."""
    with open(path, "rb") as f:
        data = f.read(4 << 20)
    if data[:2] == b"\x1f\x8b":
        data = gzip.decompress(data)
    return parse_font(data)


def parse_font(data):
    if len(data) >= 4 and data[0] == 0x36 and data[1] == 0x04:
        return _psf1(data)
    if len(data) >= 32 and struct.unpack_from("<I", data)[0] == 0x864AB572:
        return _psf2(data)
    raise ValueError("not a PSF font")


def _psf1(data):
    mode, h = data[2], data[3]
    n = 512 if mode & 1 else 256
    if h == 0 or len(data) < 4 + n * h:
        raise ValueError("PSF1 font is cut short")
    glyphs = [bytes(data[4 + i * h:4 + (i + 1) * h]) for i in range(n)]
    chars = [[] for _ in range(n)]
    if not mode & 2:
        chars = [[chr(i)] for i in range(n)]
    else:
        p = 4 + n * h
        for i in range(n):
            seq = False
            while p + 1 < len(data):
                (c,) = struct.unpack_from("<H", data, p)
                p += 2
                if c == 0xFFFF:
                    break
                if c == 0xFFFE:
                    seq = True  # sequences (a character with combining marks) aren't single characters
                if not seq:
                    chars[i].append(chr(c))
    return Font(8, h, glyphs, chars)


def _psf2(data):
    _, _, header, flags, n, size, h, w = struct.unpack_from("<8I", data)
    row_bytes = (w + 7) // 8
    if not (0 < w <= 64 and 0 < h <= 64 and 0 < n <= 65536) or size != row_bytes * h or \
            len(data) < header + n * size:
        raise ValueError("PSF2 font has a bad header")
    glyphs = [bytes(data[header + i * size:header + (i + 1) * size]) for i in range(n)]
    if not flags & 1:
        return Font(w, h, glyphs, [[chr(i)] for i in range(n)])
    chars = [[] for _ in range(n)]
    p = header + n * size
    for i in range(n):
        end = data.find(b"\xff", p)
        if end < 0:
            end = len(data)
        entry = data[p:end].split(b"\xfe")[0]  # single characters come before any sequence
        p = end + 1
        chars[i] = list(entry.decode("utf-8", "replace").replace("�", ""))
        if p >= len(data):
            break
    return Font(w, h, glyphs, chars)


def pixels(raw, width=WIDTH, height=HEIGHT, stride=STRIDE):
    """The picture as 16-bit pixel values, row after row, stride//2 a row."""
    need = stride * height
    raw = bytes(raw[:need]).ljust(need, b"\0")
    px = array.array("H")
    px.frombytes(raw)
    if sys.byteorder == "big":
        px.byteswap()
    return px


def layout(font, width=WIDTH, height=HEIGHT):
    """The grid internal/screen's NewScreen lays out: columns, rows, and the left and top margins in pixels."""
    cols, rows = width // font.w, height // font.h
    return cols, rows, (width - cols * font.w) // 2, (height - rows * font.h) // 2


def cell_bitmaps(raw, font, width=WIDTH, height=HEIGHT, stride=STRIDE):
    """Each cell's candidate bitmaps, row by row: for each colour in the cell, most used first, the bitmap of the
    pixels that aren't that colour."""
    px = pixels(raw, width, height, stride)
    sp = stride // 2
    cols, rows, left, top = layout(font, width, height)
    fw, fh, rb = font.w, font.h, font.row_bytes
    shift = rb * 8 - 1
    grid = []
    for r in range(rows):
        band = [px[(top + r * fh + y) * sp:(top + r * fh + y) * sp + width] for y in range(fh)]
        line = []
        for c in range(cols):
            x0 = left + c * fw
            cell = [band[y][x0:x0 + fw] for y in range(fh)]
            counts = {}
            for row in cell:
                for v in row:
                    counts[v] = counts.get(v, 0) + 1
            cands = []
            for bg in sorted(counts, key=lambda k: -counts[k]):
                out = bytearray()
                for row in cell:
                    bits = 0
                    for x in range(fw):
                        if row[x] != bg:
                            bits |= 1 << (shift - x)
                    out += bits.to_bytes(rb, "big")
                cands.append(bytes(out))
            line.append(cands)
        grid.append(line)
    return grid


def read_cell(font, cands):
    for b in cands:
        ch = font.char_of.get(b)
        if ch is not None:
            return ch
    return UNKNOWN


def read_lines(raw, font, width=WIDTH, height=HEIGHT, stride=STRIDE):
    """The picture as text: one line a row of cells, trailing spaces dropped."""
    return ["".join(read_cell(font, c) for c in line).rstrip()
            for line in cell_bitmaps(raw, font, width, height, stride)]


def read_text(raw, font, width=WIDTH, height=HEIGHT, stride=STRIDE):
    return "\n".join(read_lines(raw, font, width, height, stride))


def is_black(raw):
    """Every byte zero: internal/screen's blank screen (the default background is black, 0x0000)."""
    return not bytes(raw).strip(b"\0")


def glyph_check(raw, font, expected, width=WIDTH, height=HEIGHT, stride=STRIDE):
    """Finds the row of cells that best shows the text expected from its first column (the --screen-test page's
    line of meshflash's glyphs) and says, for each character, what was drawn: (character, verdict, read back), the
    verdict "ok" (the font's own glyph, or internal/screen's stand-in), "?" (drawn as the font's "?", so the font
    lacks it), or "other". Returns (row number, results)."""
    grid = cell_bitmaps(raw, font, width, height, stride)
    want = [font.glyph(c) for c in expected]
    best, best_row = -1, 0
    for r, line in enumerate(grid):
        score = sum(1 for i, g in enumerate(want) if i < len(line) and g in line[i])
        if score > best:
            best, best_row = score, r
    q = font.glyph("?") if "?" in font.index else None
    results = []
    line = grid[best_row]
    for i, c in enumerate(expected):
        if c == " ":
            continue
        cands = line[i] if i < len(line) else []
        if font.has(c) and font.glyph(c) in cands:
            results.append((c, "ok", c))
        elif q is not None and q in cands:
            results.append((c, "?", "?"))
        else:
            results.append((c, "other", read_cell(font, cands)))
    return best_row, results


def main():
    ap = argparse.ArgumentParser(description="Read a framebuffer picture of meshflash's screen back as text.")
    ap.add_argument("raw", help="the picture: RGB565 little-endian, as /dev/fb0 or a --framebuffer file holds it")
    ap.add_argument("font", help="the PSF font it was drawn in (gzipped or not)")
    ap.add_argument("--width", type=int, default=WIDTH)
    ap.add_argument("--height", type=int, default=HEIGHT)
    ap.add_argument("--stride", type=int, default=STRIDE)
    a = ap.parse_args()
    font = load_font(a.font)
    with open(a.raw, "rb") as f:
        raw = f.read()
    if is_black(raw):
        print("(all black: the screen is blank, dark or asleep)")
        return
    print(read_text(raw, font, a.width, a.height, a.stride))


if __name__ == "__main__":
    main()
