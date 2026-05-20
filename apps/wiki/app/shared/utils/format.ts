export const formatFileSize = (size: number) => {
  const MB = 1024 * 1024

  if (size < 1024) {
    return `${size} B`
  }
  if (size < MB) {
    return `${(size / 1024).toFixed(1)} KB`
  }
  if (size < 1024 * MB) {
    return `${(size / MB).toFixed(1)} MB`
  }
  return `${(size / (1024 * MB)).toFixed(2)} GB`
}

export const formatNumber = (num: number) => {
  if (num >= 1_000_000) {
    return (num / 1_000_000).toFixed(1) + 'M'
  } else if (num >= 10_000) {
    return (num / 10_000).toFixed(1) + 'w'
  } else if (num >= 1_000) {
    return (num / 1_000).toFixed(1) + 'k'
  } else {
    return num.toString()
  }
}

export const formatNumberWithCommas = (number: number): string => {
  if (number >= 10000) {
    return (number / 1000).toFixed(1) + 'k'
  } else {
    return number.toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',')
  }
}

export const camelToSnakeCase = (str: string) => {
  return str.replace(/[A-Z]/g, (letter) => `_${letter.toLowerCase()}`)
}

// formatReleaseDate renders a galgame's release info from the typed
// pair (release_date, release_date_tba):
//
//   date='2024-06-15', tba=false → "2024-06-15"
//   date='2024-06-01', tba=true  → "2024-06-01 (待定)"
//   date=null,         tba=true  → "待定"
//   date=null,         tba=false → "未知"
//
// Replaces the old `released` string display. The two fields are
// independent — a game can be scheduled with an approximate date.
export const formatReleaseDate = (
  date: string | null | undefined,
  tba: boolean | undefined
): string => {
  if (date && tba) return `${date} (待定)`
  if (date) return date
  if (tba) return '待定'
  return '未知'
}
