"""Contact sheet for eyeballing cutouts: source on top, cutout on a dark plate below.

    python scripts/character-cutout/sheet.py --src in/ --cut out/ --out sheet.png
    python scripts/character-cutout/sheet.py --src in/ --cut out/ --out flagged.png --flagged
"""

import argparse
import json
from pathlib import Path

from PIL import Image

DARK = (24, 24, 28)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--src", type=Path, required=True)
    ap.add_argument("--cut", type=Path, required=True)
    ap.add_argument("--out", type=Path, required=True)
    ap.add_argument("--cell", type=int, default=200)
    ap.add_argument("--cols", type=int, default=10)
    ap.add_argument("--limit", type=int, default=30)
    ap.add_argument("--flagged", action="store_true")
    args = ap.parse_args()

    rows = [json.loads(l) for l in (args.cut / "manifest.jsonl").open()]
    if args.flagged:
        rows = [r for r in rows if r.get("flagged") or r.get("error")]
    rows = [r for r in rows if not r.get("error")][: args.limit]
    if not rows:
        print("nothing to draw")
        return

    c = args.cell
    cols = min(args.cols, len(rows))
    grid_rows = (len(rows) + cols - 1) // cols
    sheet = Image.new("RGB", (c * cols, c * 2 * grid_rows), DARK)

    for i, r in enumerate(rows):
        gx, gy = (i % cols) * c, (i // cols) * c * 2
        src = Image.open(args.src / f"{r['hash']}.webp").convert("RGB")
        src.thumbnail((c, c))
        sheet.paste(src, (gx + (c - src.width) // 2, gy + (c - src.height) // 2))

        cut = Image.open(args.cut / r["file"]).convert("RGBA")
        cut.thumbnail((c, c))
        plate = Image.new("RGBA", cut.size, DARK + (255,))
        plate.alpha_composite(cut)
        sheet.paste(plate.convert("RGB"), (gx + (c - cut.width) // 2, gy + c + (c - cut.height) // 2))

    sheet.save(args.out)
    print(f"{args.out}  {sheet.size[0]}x{sheet.size[1]}  {len(rows)} pairs")


if __name__ == "__main__":
    main()
