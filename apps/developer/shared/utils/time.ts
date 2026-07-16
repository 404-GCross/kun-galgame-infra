import { format } from 'date-fns'

// Format an RFC3339 timestamp for the key/usage tables. Mirrors apps/web's
// shared/utils/time.ts formatDate so the two frontends read identically.
export const formatDate = (
  time: Date | string | number,
  config?: { isShowYear?: boolean; isPrecise?: boolean }
): string => {
  let formatString = 'MM-dd'

  if (config?.isShowYear) {
    formatString = 'yyyy-MM-dd'
  }

  if (config?.isPrecise) {
    formatString = `${formatString} - HH:mm`
  }

  return format(new Date(time), formatString)
}
