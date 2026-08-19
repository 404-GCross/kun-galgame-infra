export const entityName = (data: Record<string, unknown>): string | null => {
  const dn = data.display_name
  if (typeof dn === 'string' && dn) return dn
  // A slot may be a bare string or, since wave 210's names block, {value, machine}.
  const slot = (v: unknown): string | null => {
    if (typeof v === 'string' && v) return v
    if (v && typeof v === 'object') {
      const inner = (v as Record<string, unknown>).value
      if (typeof inner === 'string' && inner) return inner
    }
    return null
  }
  const fromMap = (o: Record<string, unknown>): string | null => {
    for (const k of ['zh_cn', 'zh-cn', 'zh', 'ja', 'en']) {
      const v = slot(o[k])
      if (v) return v
    }
    for (const v of Object.values(o)) {
      const s = slot(v)
      if (s) return s
    }
    return null
  }
  const n = data.name
  if (typeof n === 'string' && n) return n
  if (n && typeof n === 'object') {
    const o = n as Record<string, unknown>
    if (typeof o.name === 'string' && o.name) return o.name
    if (typeof o.latin === 'string' && o.latin) return o.latin
    const m = fromMap(o)
    if (m) return m
  }
  const ns = data.names
  if (ns && typeof ns === 'object') {
    const m = fromMap(ns as Record<string, unknown>)
    if (m) return m
  }
  return null
}
