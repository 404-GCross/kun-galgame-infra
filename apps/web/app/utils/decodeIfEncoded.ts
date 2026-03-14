export function decodeIfEncoded(str: string): string {
  try {
    const decoded = decodeURIComponent(str)
    return decoded !== str ? decoded : str
  } catch {
    return str
  }
}
