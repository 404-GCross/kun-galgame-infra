
export interface ImageURLOptions {
  cdnBase: string
  ext?: string
}

/**
 * Build the public URL for the main image of a hash.
 */
export const imageMainUrl = (
  hash: string,
  opts: ImageURLOptions
): string => {
  const ext = opts.ext ?? 'webp'
  const base = opts.cdnBase.replace(/\/+$/, '')
  return `${base}/${hash.slice(0, 2)}/${hash.slice(2, 4)}/${hash}.${ext}`
}

/**
 * Build the public URL for a specific variant.
 */
export const imageVariantUrl = (
  hash: string,
  variant: string,
  opts: ImageURLOptions
): string => {
  const ext = opts.ext ?? 'webp'
  const base = opts.cdnBase.replace(/\/+$/, '')
  return `${base}/${hash.slice(0, 2)}/${hash.slice(2, 4)}/${hash}_${variant}.${ext}`
}

/**
 * Build a URL resolver bound to a fixed CDN base. Useful when the calling
 * site wants a simpler signature at call sites.
 *
 * const { main, variant } = bindImageUrls(cfg.imageCdnBase)
 * <img :src="main(user.avatar_image_hash)">
 */
export const bindImageUrls = (cdnBase: string) => ({
  main: (hash: string, ext?: string) => imageMainUrl(hash, { cdnBase, ext }),
  variant: (hash: string, v: string, ext?: string) =>
    imageVariantUrl(hash, v, { cdnBase, ext })
})

/**
 * Graceful resolver that falls back to a legacy URL or a placeholder if
 * the hash is not set. Intended for migration period compat.
 */
export const resolveImageUrl = (
  hash: string | null | undefined,
  legacyUrl: string | null | undefined,
  placeholder: string,
  opts: ImageURLOptions,
  variant?: string
): string => {
  if (hash) {
    return variant
      ? imageVariantUrl(hash, variant, opts)
      : imageMainUrl(hash, opts)
  }
  if (legacyUrl) return legacyUrl
  return placeholder
}
