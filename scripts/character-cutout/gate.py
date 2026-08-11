"""Select the bust images (image_hash) whose background is a white plate.

    python scripts/character-cutout/gate.py --input in/ --out pass.txt --reject-out reject.txt

The bust corpus is bimodal: roughly half are studio plates on white, half are
scene crops off a CG. ToonOut has no correct answer for a scene crop — it
returns ~94% foreground and eats a random slice of the composition — so the
decision of what to cut is made here, on a measurement of the SOURCE, never on
the model's own confidence. The model always emits an alpha; it never says
"this one has no background".
"""

import argparse
import json
from multiprocessing import Pool
from pathlib import Path

import numpy as np
from PIL import Image

WHITE_MIN = 240
CORNER = 8
BORDER = 4
MIN_WHITE_SHARE = 0.25
# Not "all four corners are white": a bust crop puts the shoulders against the
# bottom edge, so a clean studio plate routinely shows only 3 white corners and
# a bottom ring that is 40% subject. Corner counting rejected 21,524 plates that
# a 40-image review found to be flawless. The ring mean is what separates them:
# >=0.70 was 70/70 clean on review, <0.50 is magazine pages and scene photos.
MIN_BORDER_WHITE = 0.70


def measure(path):
    try:
        a = np.asarray(Image.open(path).convert("RGB"), dtype=np.uint8)
    except Exception as e:
        return {"hash": path.stem, "error": f"decode: {e}"}
    white = (a >= WHITE_MIN).all(axis=2)
    h, w = white.shape
    c = CORNER
    corners = [white[:c, :c], white[:c, -c:], white[-c:, :c], white[-c:, -c:]]
    n_corner = sum(1 for k in corners if k.mean() > 0.9)
    b = BORDER
    ring = np.concatenate(
        [white[:b, :].ravel(), white[-b:, :].ravel(), white[:, :b].ravel(), white[:, -b:].ravel()]
    )
    border = float(ring.mean())
    share = float(white.mean())
    return {
        "hash": path.stem,
        "size": [w, h],
        "corners": n_corner,
        "border": round(border, 4),
        "white": round(share, 4),
        "pass": border >= MIN_BORDER_WHITE and share >= MIN_WHITE_SHARE,
    }


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--input", type=Path, required=True)
    ap.add_argument("--out", type=Path, required=True)
    ap.add_argument("--reject-out", type=Path)
    ap.add_argument("--manifest", type=Path)
    ap.add_argument("--jobs", type=int, default=16)
    args = ap.parse_args()

    srcs = sorted(p for p in args.input.iterdir() if p.suffix.lower() in {".webp", ".png", ".jpg"})
    with Pool(args.jobs) as pool:
        rows = pool.map(measure, srcs, chunksize=64)

    passed = [r for r in rows if r.get("pass")]
    errors = [r for r in rows if "error" in r]
    args.out.write_text("".join(r["hash"] + "\n" for r in passed))
    if args.reject_out:
        args.reject_out.write_text(
            "".join(r["hash"] + "\n" for r in rows if not r.get("pass") and "error" not in r)
        )
    if args.manifest:
        with args.manifest.open("w") as f:
            for r in rows:
                f.write(json.dumps(r) + "\n")

    print(f"total    {len(rows)}")
    print(f"pass     {len(passed)}  ({len(passed) / len(rows):.1%})")
    print(f"reject   {len(rows) - len(passed) - len(errors)}")
    print(f"errors   {len(errors)}")


if __name__ == "__main__":
    main()
