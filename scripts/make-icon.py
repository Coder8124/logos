#!/usr/bin/env python3
"""Generate the app icon.

The mark is the product's own signature view: an ego graph. One bright focus
node, a few neighbours of varying size (size means degree, exactly as the graph
view draws it), and edges — some radiating from the focus, some between
neighbours so it reads as a graph rather than a starburst.

Drawn at 4x and downsampled, because Pillow has no anti-aliasing of its own.

    ./scripts/make-icon.py            # writes app/build/appicon.png
    ./scripts/make-icon.py --preview  # also writes a small-size legibility strip
"""

import sys
from pathlib import Path
from PIL import Image, ImageDraw, ImageFilter

SIZE = 1024
SS = 4                      # supersample factor
N = SIZE * SS
ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / "app" / "build" / "appicon.png"

# Palette lifted from the app's dark theme so the icon and the UI agree.
BG_TOP = (26, 30, 48)       # a touch lighter than --bg-top, so the tile reads on black
BG_BOT = (8, 9, 16)
ACCENT = (107, 120, 232)    # --accent
ACCENT_2 = (139, 108, 240)  # --accent-2
FOCUS_FILL = (243, 244, 250)

# macOS icon grid: the tile occupies ~824 of 1024, leaving room for the shadow.
MARGIN = 0.098


def superellipse_mask(n, margin, power=5.0):
    """The macOS squircle — |x|^p + |y|^p = 1, not a rounded rectangle."""
    mask = Image.new("L", (n, n), 0)
    d = ImageDraw.Draw(mask)
    inset = margin * n
    a = (n - 2 * inset) / 2
    cx = cy = n / 2
    pts = []
    steps = 2048
    for i in range(steps):
        t = 2 * 3.141592653589793 * i / steps
        import math
        ct, st = math.cos(t), math.sin(t)
        x = abs(ct) ** (2.0 / power) * a * (1 if ct >= 0 else -1)
        y = abs(st) ** (2.0 / power) * a * (1 if st >= 0 else -1)
        pts.append((cx + x, cy + y))
    d.polygon(pts, fill=255)
    return mask


def vertical_gradient(n, top, bot):
    grad = Image.new("RGB", (1, n))
    px = grad.load()
    for y in range(n):
        t = y / (n - 1)
        px[0, y] = tuple(round(top[i] + (bot[i] - top[i]) * t) for i in range(3))
    return grad.resize((n, n), Image.BILINEAR)


def lerp(c1, c2, t):
    return tuple(round(c1[i] + (c2[i] - c1[i]) * t) for i in range(3))


def disc(draw, cx, cy, r, fill):
    draw.ellipse([cx - r, cy - r, cx + r, cy + r], fill=fill)


def build():
    tile = vertical_gradient(N, BG_TOP, BG_BOT)

    # A soft accent bloom behind the focus, so the tile is not flat.
    glow = Image.new("RGB", (N, N), (0, 0, 0))
    gd = ImageDraw.Draw(glow)
    disc(gd, 0.46 * N, 0.54 * N, 0.30 * N, lerp(ACCENT, (0, 0, 0), 0.62))
    glow = glow.filter(ImageFilter.GaussianBlur(0.13 * N))
    tile = Image.blend(tile, Image.blend(tile, glow, 0.55), 0.75)

    art = tile.copy()
    d = ImageDraw.Draw(art, "RGBA")

    # focus + neighbours; radius stands in for degree
    focus = (0.475, 0.535, 0.090)
    ring = [
        (0.470, 0.250, 0.054),
        (0.735, 0.385, 0.063),
        (0.700, 0.735, 0.048),
        (0.268, 0.722, 0.058),
        (0.262, 0.362, 0.044),
    ]
    edges = [(focus, n) for n in ring] + [(ring[0], ring[1]), (ring[3], ring[4])]

    for a, b in edges:
        outer = a is not focus and b is not focus
        w = (0.0115 if not outer else 0.0085) * N
        col = ACCENT + (150 if outer else 225,)
        d.line([a[0] * N, a[1] * N, b[0] * N, b[1] * N], fill=col, width=round(w))

    # neighbours: a cool-to-violet spread so they aren't a flat single colour
    for i, (x, y, r) in enumerate(ring):
        c = lerp(ACCENT, ACCENT_2, i / (len(ring) - 1))
        disc(d, x * N, y * N, r * N, c + (255,))

    # focus last. A thin dark gap keeps the edges from visually running through
    # the node without reading as a drawn ring.
    fx, fy, fr = focus
    disc(d, fx * N, fy * N, (fr + 0.009) * N, BG_BOT + (170,))
    disc(d, fx * N, fy * N, fr * N, FOCUS_FILL + (255,))

    # Light from the upper left. Heavily blurred so it never shows an edge —
    # the earlier hard-edged version read as a rendering artefact.
    sheen = Image.new("L", (N, N), 0)
    ImageDraw.Draw(sheen).ellipse(
        [-0.55 * N, -0.75 * N, 0.95 * N, 0.55 * N], fill=44)
    sheen = sheen.filter(ImageFilter.GaussianBlur(0.16 * N))
    art = Image.composite(Image.new("RGB", (N, N), (255, 255, 255)), art, sheen)

    out = Image.new("RGBA", (N, N), (0, 0, 0, 0))
    out.paste(art, (0, 0), superellipse_mask(N, MARGIN))
    return out.resize((SIZE, SIZE), Image.LANCZOS)


def preview(icon):
    sizes = [512, 256, 128, 64, 32, 16]
    pad, gap = 24, 20
    w = sum(sizes) + gap * (len(sizes) - 1) + pad * 2
    h = max(sizes) + pad * 2
    strip = Image.new("RGB", (w, h), (18, 18, 22))
    x = pad
    for s in sizes:
        strip.paste(icon.resize((s, s), Image.LANCZOS), (x, pad + (max(sizes) - s) // 2),
                    icon.resize((s, s), Image.LANCZOS))
        x += s + gap
    p = ROOT / "app" / "build" / "appicon-preview.png"
    strip.save(p)
    return p


if __name__ == "__main__":
    icon = build()
    OUT.parent.mkdir(parents=True, exist_ok=True)
    icon.save(OUT)
    print(f"wrote {OUT.relative_to(ROOT)} ({icon.size[0]}x{icon.size[1]})")
    if "--preview" in sys.argv:
        print(f"wrote {preview(icon).relative_to(ROOT)}")
