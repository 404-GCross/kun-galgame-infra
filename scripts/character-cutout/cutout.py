"""Remove the white background from catalog character standing art (figure_hash).

Reads <hash>.webp files, writes RGBA <hash>.webp cutouts plus manifest.jsonl
carrying the QC metrics the Go writeback stage gates on.

    python scripts/character-cutout/cutout.py --input in/ --output out/ --gpu 1

Needs the sibling upscale-bench venv (torch + transformers + onnxruntime-gpu):
    /home/kun/Desktop/code/website/upscale-bench/.venv/bin/python
Model selection and the numbers behind it: upscale-bench/HANDOFF_SEGMENTATION.md
"""

import argparse
import json
import sys
from pathlib import Path

import cv2
import numpy as np
import torch
import torch.nn.functional as F
from PIL import Image
from transformers import AutoModelForImageSegmentation

ISNET_URL = "https://github.com/danielgatis/rembg/releases/download/v0.0.0/isnet-anime.onnx"
IMAGENET_MEAN = torch.tensor([0.485, 0.456, 0.406]).view(1, 3, 1, 1)
IMAGENET_STD = torch.tensor([0.229, 0.224, 0.225]).view(1, 3, 1, 1)

ALPHA_FLOOR = 25
COMPONENT_KEEP_RATIO = 0.01
DIVERGENCE_LIMIT = 0.05
FG_MIN, FG_MAX = 0.05, 0.95
BBOX_PAD = 2


def to_batch(img, res, mean, std, device):
    t = torch.from_numpy(np.asarray(img.convert("RGB"), dtype=np.float32) / 255.0)
    t = t.permute(2, 0, 1)[None]
    t = F.interpolate(t, size=(res, res), mode="bilinear", align_corners=False)
    return ((t - mean) / std).to(device)


def to_alpha(prob, size):
    a = F.interpolate(prob.float(), size=(size[1], size[0]), mode="bilinear", align_corners=False)
    return (a.clamp(0, 1)[0, 0].cpu().numpy() * 255).round().astype(np.uint8)


class ToonOut:
    name = "ToonOut"

    def __init__(self, device, cache_dir):
        from huggingface_hub import hf_hub_download

        self.device = device
        self.model = AutoModelForImageSegmentation.from_pretrained(
            "ZhengPeng7/BiRefNet", trust_remote_code=True
        )
        sd = torch.load(
            hf_hub_download("joelseytre/toonout", "birefnet_finetuned_toonout.pth"),
            map_location="cpu",
            weights_only=True,
        )
        # The checkpoint was saved from a DDP-wrapped, torch.compile'd model, so keys
        # carry "module._orig_mod." — strip in that order and load strict. strict=False
        # loads nothing at all and silently leaves you running stock BiRefNet, whose
        # output looks plausible: it just drops chibi characters entirely.
        sd = {k.removeprefix("module.").removeprefix("_orig_mod."): v for k, v in sd.items()}
        self.model.load_state_dict(sd, strict=True)
        self.model = self.model.to(device).half()
        self.model.train(False)

    def __call__(self, img):
        x = to_batch(img, 1024, IMAGENET_MEAN, IMAGENET_STD, self.device).half()
        with torch.no_grad():
            pred = self.model(x)
        while isinstance(pred, (list, tuple)):
            pred = pred[-1]
        return to_alpha(torch.sigmoid(pred), img.size)


class IsnetAnime:
    name = "isnet-anime"

    def __init__(self, path):
        import onnxruntime as ort

        self.sess = ort.InferenceSession(
            str(path), providers=["CUDAExecutionProvider", "CPUExecutionProvider"]
        )
        self.inp = self.sess.get_inputs()[0].name

    def __call__(self, img):
        x = to_batch(img, 1024, IMAGENET_MEAN, torch.ones(1, 3, 1, 1), "cpu").numpy()
        pred = torch.from_numpy(self.sess.run(None, {self.inp: x})[0][:, 0:1])
        pred = (pred - pred.min()) / (pred.max() - pred.min() + 1e-8)
        return to_alpha(pred, img.size)


def ensure_isnet(cache_dir):
    cache_dir.mkdir(parents=True, exist_ok=True)
    onnx = cache_dir / "isnet-anime.onnx"
    if not onnx.exists():
        import urllib.request

        print(f"downloading {ISNET_URL} ...", file=sys.stderr)
        urllib.request.urlretrieve(ISNET_URL, onnx)
    return onnx


def drop_stray_components(alpha):
    """Delete islands far smaller than the main subject: the copyright line Getchu
    stamps in the corner, which ToonOut keeps as foreground."""
    mask = (alpha > ALPHA_FLOOR).astype(np.uint8)
    n, labels, stats, _ = cv2.connectedComponentsWithStats(mask, 8)
    if n <= 2:
        return alpha, n - 1, n - 1
    areas = stats[1:, cv2.CC_STAT_AREA]
    # A ratio, not an absolute floor: chibi characters on a multi-character plate are
    # legitimately tiny, and dropping them is the exact failure that ruled out the
    # general matting models.
    keep = 1 + np.flatnonzero(areas >= COMPONENT_KEEP_RATIO * areas.max())
    out = alpha.copy()
    out[~np.isin(labels, keep)] = 0
    return out, n - 1, len(keep)


def crop_to_subject(rgba, alpha):
    ys, xs = np.nonzero(alpha > ALPHA_FLOOR)
    if len(ys) == 0:
        return rgba, None
    h, w = alpha.shape
    y0, y1 = max(0, ys.min() - BBOX_PAD), min(h, ys.max() + 1 + BBOX_PAD)
    x0, x1 = max(0, xs.min() - BBOX_PAD), min(w, xs.max() + 1 + BBOX_PAD)
    return rgba[y0:y1, x0:x1], [int(x0), int(y0), int(x1), int(y1)]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--input", type=Path, required=True)
    ap.add_argument("--output", type=Path, required=True)
    ap.add_argument("--gpu", type=int, default=1)
    ap.add_argument("--cache", type=Path, default=Path("models"))
    ap.add_argument("--quality", type=int, default=80)
    ap.add_argument("--no-crop", action="store_true")
    ap.add_argument("--limit", type=int, default=0)
    args = ap.parse_args()

    device = f"cuda:{args.gpu}" if torch.cuda.is_available() else "cpu"
    args.output.mkdir(parents=True, exist_ok=True)
    manifest = (args.output / "manifest.jsonl").open("w")

    srcs = sorted(p for p in args.input.iterdir() if p.suffix.lower() in {".webp", ".png", ".jpg"})
    if args.limit:
        srcs = srcs[: args.limit]
    print(f"{len(srcs)} images -> {args.output} on {device}", file=sys.stderr)

    toon = ToonOut(device, args.cache)
    isnet = IsnetAnime(ensure_isnet(args.cache))

    flagged = 0
    for i, p in enumerate(srcs):
        h = p.stem
        try:
            img = Image.open(p).convert("RGB")
        except Exception as e:
            manifest.write(json.dumps({"hash": h, "error": f"decode: {e}"}) + "\n")
            flagged += 1
            continue

        a_toon = toon(img)
        a_isnet = isnet(img)
        fg_toon = float((a_toon > 127).mean())
        fg_isnet = float((a_isnet > 127).mean())
        divergence = abs(fg_toon - fg_isnet)

        cleaned, n_comp, n_kept = drop_stray_components(a_toon)
        rgba = np.dstack([np.asarray(img, dtype=np.uint8), cleaned])
        if args.no_crop:
            cropped, bbox = rgba, None
        else:
            cropped, bbox = crop_to_subject(rgba, cleaned)

        reasons = []
        if divergence > DIVERGENCE_LIMIT:
            reasons.append("divergence")
        if not (FG_MIN <= fg_toon <= FG_MAX):
            reasons.append("foreground_share")
        if bbox is None:
            reasons.append("empty")
        if reasons:
            flagged += 1

        out_path = args.output / f"{h}.webp"
        Image.fromarray(cropped, "RGBA").save(out_path, "WEBP", quality=args.quality, method=4)

        manifest.write(
            json.dumps(
                {
                    "hash": h,
                    "file": out_path.name,
                    "src_size": list(img.size),
                    "out_size": [cropped.shape[1], cropped.shape[0]],
                    "bytes": out_path.stat().st_size,
                    "fg_toon": round(fg_toon, 4),
                    "fg_isnet": round(fg_isnet, 4),
                    "divergence": round(divergence, 4),
                    "components": n_comp,
                    "components_kept": n_kept,
                    "bbox": bbox,
                    "flagged": reasons,
                }
            )
            + "\n"
        )
        if (i + 1) % 200 == 0:
            print(f"  {i + 1}/{len(srcs)}  flagged {flagged}", file=sys.stderr)

    manifest.close()
    print(f"done: {len(srcs)} images, {flagged} flagged", file=sys.stderr)


if __name__ == "__main__":
    main()
