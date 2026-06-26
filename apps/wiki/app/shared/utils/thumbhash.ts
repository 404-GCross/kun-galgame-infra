// ThumbHash helpers — turn the compact base64 ThumbHash that image_service
// stores per image into a blur-up placeholder, and build the wrapper style
// that reserves the correct aspect ratio (no layout shift) and paints the
// blur underneath the real image while it loads.
//
// ThumbHash is a ~25-byte perceptual hash (base64'd to ~34 chars) that decodes
// to a tiny blurred preview entirely client-side — no extra request. Pairing
// it with the exact width/height (also from image_service) gives the
// recognised best-practice: reserved box + meaningful placeholder.

import { thumbHashToDataURL } from 'thumbhash'

/**
 * Decode a base64 ThumbHash into a tiny blurred PNG data URL usable as a CSS
 * background. Returns '' on missing/invalid input so callers fall back to a
 * plain skeleton. Pure JS (SSR-safe — no canvas/DOM).
 */
export const thumbhashToDataUrl = (b64?: string | null): string => {
  if (!b64) return ''
  try {
    const bin = atob(b64)
    const bytes = new Uint8Array(bin.length)
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i)
    return thumbHashToDataURL(bytes)
  } catch {
    return ''
  }
}

/** Intrinsic image metadata as returned by the backend (all optional). */
export interface ImageMetaLike {
  width?: number
  height?: number
  thumbhash?: string
}

/**
 * Build the wrapper `<div>` style for an image box: reserve the exact aspect
 * ratio (falling back to `fallbackRatio` when dimensions are unknown) and,
 * when a thumbhash is present, paint its blur-up as a cover background so it
 * shows through until the real image fades in on top.
 */
export const imageBoxStyle = (
  meta: ImageMetaLike | null | undefined,
  fallbackRatio = '16 / 9'
): Record<string, string> => {
  const style: Record<string, string> = {
    aspectRatio:
      meta?.width && meta?.height ? `${meta.width} / ${meta.height}` : fallbackRatio
  }
  const url = thumbhashToDataUrl(meta?.thumbhash)
  if (url) {
    style.backgroundImage = `url("${url}")`
    style.backgroundSize = 'cover'
    style.backgroundPosition = 'center'
  }
  return style
}

/**
 * Memo-friendly map builder: image_hash → box style, for a list of covers /
 * screenshots. Computed once per list so the per-item thumbhash decode doesn't
 * re-run on every render.
 */
export const imageBoxStyles = (
  items: (ImageMetaLike & { image_hash: string })[] | null | undefined,
  fallbackRatio = '16 / 9'
): Record<string, Record<string, string>> => {
  const out: Record<string, Record<string, string>> = {}
  for (const it of items ?? []) {
    out[it.image_hash] = imageBoxStyle(it, fallbackRatio)
  }
  return out
}
