// Resolve a human name from ANY catalog/galgame entity record. The faces name
// themselves differently: labels carry a flat display_name; names/characters
// nest it (`name` as a string, a { name, latin } stub, or a locale map);
// galgame aggregates use a `names` locale map. Honest chain, null when truly
// nameless (callers fall back to #id).
export const entityName = (data: Record<string, unknown>): string | null => {
  const dn = data.display_name
  if (typeof dn === 'string' && dn) return dn
  const fromMap = (o: Record<string, unknown>): string | null => {
    for (const k of ['zh_cn', 'zh-cn', 'zh', 'ja', 'en']) {
      const v = o[k]
      if (typeof v === 'string' && v) return v
    }
    const first = Object.values(o).find((v) => typeof v === 'string' && v)
    return typeof first === 'string' ? first : null
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
