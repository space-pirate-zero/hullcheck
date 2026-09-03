#!/usr/bin/env python3
"""gen_brand.py — the hullcheck brand package, generated rather than drawn.

Every mark here is geometry, so the SVG and the PNGs come from ONE definition and
cannot drift apart. That matters more than usual for an icon: a wordmark whose
vector and raster forms disagree is the same class of bug this tool exists to find.

The letterforms are the CLI banner's own 5x5 slab font, rendered as rectangles.
No font file, no dependency, and the terminal and the logo are literally the same
alphabet.

    uv run --with pillow python3 brand/gen_brand.py
"""
from __future__ import annotations
import json, math
from pathlib import Path
from PIL import Image, ImageDraw

HERE = Path(__file__).resolve().parent

# Industrial-goth cyberpunk noir: near-black void, hot neon, cold signal,
# newsprint grit. Five values, no others (studio palette rule).
VOID, PINK, CYAN, PAPER, MUTED = "#030303", "#FF1493", "#00F0FF", "#E8E8E8", "#8A90A0"

def rgb(h): return tuple(int(h[i:i+2], 16) for i in (1, 3, 5))

# ---------------------------------------------------------------- icon geometry
# A bulkhead hatch seen head-on: an octagonal plate, four rivets, and a seam
# across the middle that is interrupted. The interruption is the whole idea -
# a hull breach does not announce itself, so you go and look.

def octagon(cx, cy, r, rot=22.5):
    return [(cx + r * math.cos(math.radians(rot + k * 45)),
             cy + r * math.sin(math.radians(rot + k * 45))) for k in range(8)]

def icon_geometry(size):
    s = size / 512.0
    cx = cy = 256 * s
    return dict(
        s=s, cx=cx, cy=cy,
        outer=octagon(cx, cy, 200 * s),
        inner=octagon(cx, cy, 150 * s),
        ring_w=max(1.0, 26 * s),
        rivet_r=max(0.8, 13 * s),
        rivets=[(cx + 172 * s * math.cos(math.radians(a)),
                 cy + 172 * s * math.sin(math.radians(a))) for a in (45, 135, 225, 315)],
        seam_y=cy,
        seam_h=max(1.0, 22 * s),
        seam_x0=cx - 150 * s, seam_x1=cx + 150 * s,
        # The breach: a gap in the seam, right of centre, filled with pink.
        gap_x0=cx + 22 * s, gap_x1=cx + 74 * s,
    )

def draw_icon_png(size: int, bg: bool = True) -> Image.Image:
    g = icon_geometry(size)
    ss = 4  # supersample: the octagon's diagonals alias badly at 16px otherwise
    big = size * ss
    gb = icon_geometry(big)
    im = Image.new("RGBA", (big, big), rgb(VOID) + ((255,) if bg else (0,)))
    d = ImageDraw.Draw(im)
    d.polygon(gb["outer"], fill=rgb(CYAN))
    d.polygon(gb["inner"], fill=rgb(VOID))
    for (x, y) in gb["rivets"]:
        d.ellipse([x - gb["rivet_r"], y - gb["rivet_r"], x + gb["rivet_r"], y + gb["rivet_r"]],
                  fill=rgb(MUTED))
    h = gb["seam_h"] / 2
    d.rectangle([gb["seam_x0"], gb["seam_y"] - h, gb["seam_x1"], gb["seam_y"] + h], fill=rgb(PAPER))
    d.rectangle([gb["gap_x0"], gb["seam_y"] - h, gb["gap_x1"], gb["seam_y"] + h], fill=rgb(VOID))
    d.rectangle([gb["gap_x0"], gb["seam_y"] - h * 0.5, gb["gap_x1"], gb["seam_y"] + h * 0.5],
                fill=rgb(PINK))
    del g
    return im.resize((size, size), Image.LANCZOS)

def icon_svg(bg=True) -> str:
    g = icon_geometry(512)
    pts = lambda p: " ".join(f"{x:.1f},{y:.1f}" for x, y in p)
    riv = "".join(f'<circle cx="{x:.1f}" cy="{y:.1f}" r="{g["rivet_r"]:.1f}" fill="{MUTED}"/>'
                  for x, y in g["rivets"])
    h = g["seam_h"] / 2
    ground = f'<rect width="512" height="512" fill="{VOID}"/>' if bg else ""
    return f'''<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512" width="512" height="512" role="img" aria-label="hullcheck">
<title>hullcheck</title>
{ground}
<polygon points="{pts(g["outer"])}" fill="{CYAN}"/>
<polygon points="{pts(g["inner"])}" fill="{VOID}"/>
{riv}
<rect x="{g["seam_x0"]:.1f}" y="{g["seam_y"]-h:.1f}" width="{g["seam_x1"]-g["seam_x0"]:.1f}" height="{g["seam_h"]:.1f}" fill="{PAPER}"/>
<rect x="{g["gap_x0"]:.1f}" y="{g["seam_y"]-h:.1f}" width="{g["gap_x1"]-g["gap_x0"]:.1f}" height="{g["seam_h"]:.1f}" fill="{VOID}"/>
<rect x="{g["gap_x0"]:.1f}" y="{g["seam_y"]-h*0.5:.1f}" width="{g["gap_x1"]-g["gap_x0"]:.1f}" height="{h:.1f}" fill="{PINK}"/>
</svg>
'''

# ------------------------------------------------------------------- wordmark
# The CLI banner's alphabet, as vector blocks.
# These are the CLI banner's glyphs, unchanged. A 3-wide variant was tried first
# and the K was illegible - a K needs the room for its diagonal.
GLYPHS = {
 'H': ["#   #", "#   #", "#####", "#   #", "#   #"],
 'U': ["#   #", "#   #", "#   #", "#   #", "#####"],
 'L': ["#    ", "#    ", "#    ", "#    ", "#####"],
 'C': ["#####", "#    ", "#    ", "#    ", "#####"],
 'E': ["#####", "#    ", "#####", "#    ", "#####"],
 'K': ["#   #", "#  # ", "###  ", "#  # ", "#   #"],
}
WORD = "HULLCHECK"

def word_cells(word=WORD):
    """Yield (col, row) of every lit cell, and the total width in cells."""
    x = 0
    for i, ch in enumerate(word):
        g = GLYPHS[ch]
        for r, line in enumerate(g):
            for c, v in enumerate(line):
                if v == "#":
                    yield (x + c, r)
        x += len(g[0]) + 1
    return

def word_width(word=WORD):
    return sum(len(GLYPHS[c][0]) for c in word) + (len(word) - 1)

def wordmark_svg(unit=18, pad=24, ink=PAPER, accent=PINK, bg=VOID) -> str:
    w, h = word_width(), 5
    W, H = w * unit + pad * 2, h * unit + pad * 2 + unit
    rects = [f'<rect x="{pad + c*unit}" y="{pad + r*unit}" width="{unit}" height="{unit}" fill="{ink}"/>'
             for (c, r) in word_cells()]
    # One breach, matching the icon: a short accent rule beneath the mark.
    rects.append(f'<rect x="{pad}" y="{pad + 5*unit + unit//2}" width="{unit*6}" height="{max(2,unit//3)}" fill="{accent}"/>')
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {W} {H}" width="{W}" height="{H}" '
            f'role="img" aria-label="hullcheck"><title>hullcheck</title>'
            f'<rect width="{W}" height="{H}" fill="{bg}"/>' + "".join(rects) + "</svg>\n")

def draw_word_png(unit=18, pad=24, ink=PAPER, accent=PINK, bg=VOID) -> Image.Image:
    w, h = word_width(), 5
    im = Image.new("RGB", (w * unit + pad * 2, h * unit + pad * 2 + unit), rgb(bg))
    d = ImageDraw.Draw(im)
    for (c, r) in word_cells():
        d.rectangle([pad + c*unit, pad + r*unit, pad + (c+1)*unit - 1, pad + (r+1)*unit - 1], fill=rgb(ink))
    y = pad + 5*unit + unit//2
    d.rectangle([pad, y, pad + unit*6, y + max(2, unit//3)], fill=rgb(accent))
    return im

# ---------------------------------------------------------------- social card
def social_card(W=1280, H=640) -> Image.Image:
    im = Image.new("RGB", (W, H), rgb(VOID))
    d = ImageDraw.Draw(im)
    # A faint rivet grid: industrial texture, never louder than the mark.
    for x in range(60, W, 60):
        for y in range(60, H, 60):
            d.ellipse([x-2, y-2, x+2, y+2], fill=(20, 22, 28))
    icon = draw_icon_png(260, bg=False)
    im.paste(icon, (86, (H - 260)//2), icon)
    word = draw_word_png(unit=12, pad=0)
    im.paste(word, (404, (H - word.height)//2))
    d.rectangle([0, H-10, W, H], fill=rgb(CYAN))
    return im

def main():
    out = HERE
    (out / "icon.svg").write_text(icon_svg())
    (out / "icon-transparent.svg").write_text(icon_svg(bg=False))
    (out / "wordmark.svg").write_text(wordmark_svg())
    for n in (16, 32, 64, 128, 256, 512, 1024):
        draw_icon_png(n).save(out / f"icon-{n}.png")
    draw_icon_png(32).save(out / "favicon.png")
    draw_word_png().save(out / "wordmark.png")
    social_card().save(out / "social-card.png")
    (out / "tokens.json").write_text(json.dumps({
        "name": "hullcheck",
        "derived_from": "Spaceship Alpha 9 / Space Pirate Zero",
        "mood": "industrial-goth cyberpunk noir - near-black void, hot neon, cold signal, newsprint grit",
        "colors": {
            "void":  {"hex": VOID,  "role": "ground; near-black, never pure black"},
            "pink":  {"hex": PINK,  "role": "the breach; one accent, used once"},
            "cyan":  {"hex": CYAN,  "role": "the hull; structure and signal"},
            "paper": {"hex": PAPER, "role": "foreground; dirty off-white, newsprint"},
            "muted": {"hex": MUTED, "role": "rivets, metadata, de-emphasis"},
        },
        "rules": [
            "Exactly these five values. No other hues.",
            "Never pink on cyan or cyan on pink - separate them with void or paper.",
            "The pink appears once per composition. It is the breach, not decoration.",
        ],
    }, indent=2) + "\n")
    for p in sorted(out.glob("*")):
        if p.suffix in (".png", ".svg"):
            print(f"  {p.name:26s} {p.stat().st_size//1024:>4} KB")

if __name__ == "__main__":
    main()
