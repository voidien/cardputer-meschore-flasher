"""Draws meshflash's home-screen icon for the CardputerZero's launcher, 100x100, flat and outlined like the launcher's
own icons (share/images/*_100.png):

- meshflash_100.png: a green LoRa board with a gold antenna and a row of header pins, and a lightning bolt (flashing)
  across its dark chip, on a teal tile. This is concept A of the three drawn for the owner, the one they chose
  (SPEC.md 11, "Icon").

Unlike the kit's icons it sits on a tile (SPEC.md 9.2): only the tile's rounded corners are clear.

Run: python3 packaging/make_icon.py (needs Pillow). It writes it next to itself.
"""
from pathlib import Path

from PIL import Image, ImageDraw

SCALE = 8  # drawn at 800x800, then scaled down, for smooth edges
TEAL, TEAL_DARK = (22, 96, 110, 255), (10, 48, 58, 255)
PCB, PCB_DARK = (46, 140, 80, 255), (20, 70, 38, 255)
CHIP, CHIP_DARK = (40, 44, 52, 255), (12, 14, 18, 255)
GOLD, GOLD_DARK = (240, 190, 60, 255), (140, 96, 16, 255)
BOLT, BOLT_DARK = (255, 214, 64, 255), (150, 100, 10, 255)
LINE = 3  # outline width, in the 100-unit grid


def s(*v):
    return [round(x * SCALE) for x in v]


def bolt(d, cx, cy, k):
    """A lightning bolt centred on (cx, cy), k times the size it's drawn at here."""
    pts = [(-4, -22), (10, -22), (3, -5), (12, -5), (-6, 22), (-1, 3), (-10, 3)]
    d.polygon([tuple(s(cx + x * k, cy + y * k)) for x, y in pts], fill=BOLT, outline=BOLT_DARK,
              width=round(2.2 * SCALE))


def draw():
    im = Image.new("RGBA", (100 * SCALE, 100 * SCALE), (0, 0, 0, 0))
    d = ImageDraw.Draw(im)

    # The tile, with the corners clear like the launcher's other icons.
    d.rounded_rectangle(s(8, 8, 92, 92), radius=14 * SCALE, fill=TEAL, outline=TEAL_DARK, width=LINE * SCALE)

    # The antenna: a gold whip, drawn before the board so the board's top edge covers its foot.
    d.rounded_rectangle(s(62, 14, 68, 40), radius=3 * SCALE, fill=GOLD, outline=GOLD_DARK, width=round(2 * SCALE))

    # The board, with its header pins along the bottom edge.
    d.rounded_rectangle(s(20, 36, 80, 82), radius=5 * SCALE, fill=PCB, outline=PCB_DARK, width=LINE * SCALE)
    for x in (26, 32, 38, 44, 50, 56, 62, 68, 74):
        d.rectangle(s(x - 1.6, 76, x + 1.6, 80), fill=GOLD)

    # The chip, and the bolt across it: the board being flashed.
    d.rounded_rectangle(s(28, 43, 62, 73), radius=2 * SCALE, fill=CHIP, outline=CHIP_DARK, width=round(2 * SCALE))
    bolt(d, 45, 58, 0.95)

    return im.resize((100, 100), Image.Resampling.LANCZOS)


if __name__ == "__main__":
    out = Path(__file__).with_name("meshflash_100.png")
    draw().save(out, optimize=True)
    print("wrote", out)
