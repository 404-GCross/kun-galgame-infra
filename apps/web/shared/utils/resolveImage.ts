// Image URL resolver helpers.
//
// Background: see docs/image_service/04-migration-plan.md.
//
// Some image fields are migrated to image_service (hash-addressed), some
// stay on legacy buckets (kungal/moyu old WebP). The resolve helpers
// implement a uniform fallback chain:
//
//    image_hash → image_service URL
//    legacy URL → use as-is
//    neither    → caller-provided placeholder
//
// They depend on a CDN base URL (image_service public CDN). Read it from
// runtime config rather than hardcoding.

interface ImageURLOptions {
  /** image_service CDN base, e.g. https://image.kungal.nextmoe.dev */
  cdnBase: string
  /** ext (default 'webp', V1 always outputs webp) */
  ext?: string
  /** variant suffix (e.g. '256', '100', 'mini'); when omitted, returns main URL */
  variant?: string
}

/** Build a public image_service URL from a content hash. */
export const imageHashUrl = (
  hash: string,
  opts: ImageURLOptions
): string => {
  const ext = opts.ext ?? 'webp'
  const base = opts.cdnBase.replace(/\/+$/, '')
  if (!hash || hash.length < 4) return ''
  const prefix = `${base}/${hash.slice(0, 2)}/${hash.slice(2, 4)}/${hash}`
  return opts.variant ? `${prefix}_${opts.variant}.${ext}` : `${prefix}.${ext}`
}

/**
 * Resolve a user's avatar URL. Prefers `avatar_image_hash` (new), falls
 * back to legacy `avatar`, finally to the placeholder.
 *
 * @param user object with `avatar` and optional `avatar_image_hash`
 * @param opts.cdnBase  image_service CDN base
 * @param opts.variant  e.g. '256' (default avatar tile) or '100' (small)
 * @param placeholder   shown when neither hash nor legacy URL is set
 */
export const resolveAvatarUrl = (
  user: { avatar: string; avatar_image_hash?: string | null } | null | undefined,
  opts: ImageURLOptions,
  placeholder = ''
): string => {
  if (!user) return placeholder
  if (user.avatar_image_hash) {
    return imageHashUrl(user.avatar_image_hash, opts)
  }
  if (user.avatar) return user.avatar
  return placeholder
}

/**
 * Resolve a galgame's banner URL. Prefers `banner_image_hash` (new),
 * falls back to legacy `banner`. For list views pass `variant: 'mini'`.
 */
export const resolveBannerUrl = (
  galgame: { banner: string; banner_image_hash?: string | null } | null | undefined,
  opts: ImageURLOptions,
  placeholder = ''
): string => {
  if (!galgame) return placeholder
  if (galgame.banner_image_hash) {
    return imageHashUrl(galgame.banner_image_hash, opts)
  }
  if (galgame.banner) return galgame.banner
  return placeholder
}
