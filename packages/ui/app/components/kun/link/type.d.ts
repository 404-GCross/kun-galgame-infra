import type { KunUIColor, KunUISize } from '../ui/type'

export interface KunLinkProps {
  // NuxtLink auto-falls-through to `<a>` for external URLs, so a
  // separate `tag` prop is unnecessary. Removed in v0.1.0; consumers
  // passing `tag` will see TS error and can drop the prop.
  href?: string
  to?: string | Record<string, string>
  color?: KunUIColor
  underline?: 'none' | 'hover' | 'always'
  size?: KunUISize
  className?: string
  rel?: string
  target?: '_blank' | '_self' | '_parent' | '_top'
  isShowAnchorIcon?: boolean
}
