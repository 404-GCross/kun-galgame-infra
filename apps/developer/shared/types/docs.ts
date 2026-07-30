// Typed documentation model for the self-built API reference (/docs/**).
//
// The Tier-A public OpenAPI spec (docs/catalog/public-openapi.yaml) is parsed at
// build time into this render-friendly shape by scripts/sync-specs.mjs, which
// writes the committed generated module app/generated/docs-model.ts. Nothing
// here is hand-authored per endpoint — the spec is the single source, this is
// its projection. (The galgame face was delisted on 2026-07-30 and its spec
// deleted with it.)

export type DocsMethod = 'get' | 'post' | 'put' | 'patch' | 'delete'

export type DocsFaceKey = 'catalog'

// A render-friendly schema tree node. $ref pointers are dereferenced inline
// while building the tree (acyclic in both specs; a cycle guard degrades to a
// named leaf just in case), so the UI recurses over plain data with no lookups.
export interface DocsSchemaNode {
  // Property name within its parent object; absent for a root / array element.
  name?: string
  // Rendered type keyword: object | array | map | string | integer | number |
  // boolean, or a schema name when a cycle was cut.
  type: string
  // OpenAPI `format` (int64 | double | date-time | uri …) when present.
  format?: string
  // Human description carried from the spec, if any.
  doc?: string
  // Whether the field is required in its parent object.
  required?: boolean
  // Whether `null` is an accepted value (OpenAPI 3.1 `type: [x, "null"]`).
  nullable?: boolean
  // Allowed values for an enum field.
  enum?: string[]
  // Object properties (type === 'object').
  children?: DocsSchemaNode[]
  // Element schema (type === 'array', or the value schema of a 'map').
  itemsOf?: DocsSchemaNode
}

// A single query- or path parameter.
export interface DocsParam {
  name: string
  in: 'query' | 'path'
  required: boolean
  type: string
  format?: string
  doc?: string
  enum?: string[]
}

// One documented response (status code + its envelope schema).
export interface DocsResponse {
  status: string
  description: string
  schema?: DocsSchemaNode
}

// A single API operation = one route in /docs/[face]/[operationId].
export interface DocsOperation {
  // operationId — stable identity + the last route segment.
  id: string
  method: DocsMethod
  path: string
  summary: string
  description?: string
  // The OAuth scope a key needs for this face (galgame:read | catalog:read).
  scope: string
  params: DocsParam[]
  requestBody?: DocsSchemaNode
  responses: DocsResponse[]
  // A ready-to-run curl example with a placeholder Bearer key.
  curl: string
}

// Operations grouped by their OpenAPI tag (both faces carry a single tag today).
export interface DocsGroup {
  tag: string
  title: string
  operations: DocsOperation[]
}

// One API face (galgame | catalog): its metadata + grouped operations.
export interface DocsFace {
  key: DocsFaceKey
  // Short label for tabs / breadcrumbs (Galgame | Catalog).
  label: string
  // Full spec title (info.title).
  title: string
  // Public base URL third parties call.
  baseUrl: string
  groups: DocsGroup[]
}

export interface DocsModel {
  faces: DocsFace[]
}
