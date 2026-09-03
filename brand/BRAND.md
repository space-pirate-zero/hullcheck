# hullcheck — brand

Industrial-goth: near-black void, cold signal, hot neon, newsprint grit. Derived
from the Spaceship Alpha 9 studio palette, which is where the register comes from.

Everything here is **generated**, not drawn. `gen_brand.py` emits the SVG and every
PNG from one geometry definition, so the vector and raster forms cannot drift
apart — a wordmark whose formats disagree is the same class of bug this tool
exists to find. That script is also the provenance: every asset is reproducible
from it exactly.

```sh
uv run --with pillow python3 brand/gen_brand.py
```

## The mark

A **bulkhead hatch** seen head-on: an octagonal plate, four rivets, and a seam
across the middle that is interrupted.

The interruption is the whole idea. A hull breach does not announce itself — the
seal looks fine and the reading drifts — so you go and look. The pink gap is the
breach; everything else is the hull holding.

| Asset | Use |
|---|---|
| `icon.svg`, `icon-{16..1024}.png` | app icon, avatar, favicon |
| `icon-transparent.svg` | over a ground that is not void |
| `wordmark.svg`, `wordmark.png` | README header, docs, slides |
| `social-card.png` (1280×640) | GitHub social preview, link unfurls |

## Colour

Exactly five values. No others.

| Token | Hex | Role |
|---|---|---|
| void | `#030303` | ground. Near-black, **never** pure black |
| cyan | `#00F0FF` | the hull — structure, signal, the plate itself |
| pink | `#FF1493` | the breach — one accent, used **once** per composition |
| paper | `#E8E8E8` | foreground; a dirty off-white, newsprint not white |
| muted | `#8A90A0` | rivets, metadata, de-emphasis |

**Rules that are not negotiable:**

1. Never pink on cyan, or cyan on pink. Separate them with void or paper.
2. The pink appears **once**. It is the breach, not decoration. An early wordmark
   spent it on nine letters at once and read as noise.
3. Void is `#030303`. Pure black is a different, flatter thing.

## Type

The wordmark uses the **command-line banner's own 5×5 slab alphabet**, rendered as
rectangles. Not a lookalike — the same glyph table, so the terminal and the logo
are literally one typeface. It needs no font file, which is why the brand has no
dependency and no licensing question.

A 3-wide variant was tried first and abandoned: a K has no room for its diagonal
at that width and the mark read as `HULLCHECI:`.

For running text, anything legible and monospace-adjacent. There is no bundled
text face and no plan for one.

## Don't

- Don't recolour the mark. Five values, fixed roles.
- Don't add a second accent. If something needs emphasis and the pink is spent,
  the composition is doing too much.
- Don't set the wordmark in a different typeface. Regenerate it instead.
- Don't stretch the icon. The octagon is regular; a squashed one reads as a bug.
