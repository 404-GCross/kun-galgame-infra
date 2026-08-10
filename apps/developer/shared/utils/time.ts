import { format } from 'date-fns'

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
