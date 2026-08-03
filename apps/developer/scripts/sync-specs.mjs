#!/usr/bin/env node
/**
 * Builds the typed documentation model the self-built API reference (/docs/**)
 * renders. Reads the Tier-A public OpenAPI spec from the repo's docs/ (the
 * single source, produced by `cmd/gen-openapi` public targets), parses it,
 * dereferences $ref pointers inline into render-friendly schema trees, derives
 * parameter tables / request bodies / responses, generates a ready-to-run curl
 * example per operation, and writes `app/generated/docs-model.ts`.
 *
 * The generated file is committed (same pattern as app/assets/kun-icons.ts): a
 * derived build artifact, never hand-edited. Re-run after the public specs
 * change:  pnpm --filter developer sync:specs
 *
 * The galgame face was dropped at wave 146 (2026-07-30): its /v1/galgame
 * projection was delisted and its spec deleted, so catalog is the only public
 * face left to document.
 */
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { parse as parseYaml } from 'yaml'

const __dirname = dirname(fileURLToPath(import.meta.url))
const REPO_ROOT = join(__dirname, '..', '..', '..')
const OUTPUT = join(__dirname, '..', 'app/generated/docs-model.ts')

// The live open-API host (docs/developer-platform/01-design §2). The public
// specs are server-agnostic; every URL we render (base URL, curl) is this host
// + the spec path (which already carries the /v1 prefix).
const API_HOST = 'https://api.nextmoe.dev'

const FACES = [
  {
    key: 'catalog',
    label: 'Catalog',
    scope: 'catalog:read',
    file: join(REPO_ROOT, 'docs/catalog/public-openapi.yaml')
  }
]

// Total operations across the frozen specs — a coverage guard so a spec edit
// that adds/drops an endpoint without a model rebuild fails loudly.
// catalog 23 (prior 12 + A2-1b taxonomy lists 4 + A2-1c calendar buckets 3 +
// A2-1d works/search 1 + wave-149b stats 1 + wave-149c series detail 1 +
// the series browse lane 1). The galgame face's 26 ops left the count at wave 146
// when that face was delisted (46 → 20).
const EXPECTED_OPERATION_COUNT = 23

const refName = (ref) => ref.split('/').pop()

// OpenAPI 3.1 encodes nullability as `type: [x, "null"]`. Split a raw type into
// its primary keyword + a nullable flag.
const splitType = (rawType) => {
  const types = Array.isArray(rawType) ? rawType : rawType ? [rawType] : []
  return {
    primary: types.find((t) => t !== 'null'),
    nullable: types.includes('null')
  }
}

// Build a render-friendly schema tree, dereferencing $ref inline. `seen` tracks
// the ref chain on the current path so a cycle degrades to a named leaf instead
// of recursing forever (both specs are acyclic; this is just a guard).
const buildNode = (schema, { name, required, schemas, seen }) => {
  if (schema.$ref) {
    const rn = refName(schema.$ref)
    if (seen.has(rn)) {
      return { ...(name !== undefined && { name }), type: rn, ...(required && { required }) }
    }
    const resolved = schemas[rn]
    if (!resolved) throw new Error(`unresolved $ref: ${schema.$ref}`)
    const next = new Set(seen)
    next.add(rn)
    return buildNode(resolved, { name, required, schemas, seen: next })
  }

  const { primary, nullable } = splitType(schema.type)
  const node = {}
  if (name !== undefined) node.name = name
  if (required) node.required = true
  if (nullable) node.nullable = true
  if (schema.description) node.doc = schema.description
  if (schema.format) node.format = schema.format
  if (schema.enum) node.enum = schema.enum

  if (primary === 'object' && schema.properties) {
    node.type = 'object'
    const req = new Set(schema.required || [])
    // Drop the JSON-schema self-link ($schema, readOnly) — pure noise in docs.
    node.children = Object.keys(schema.properties)
      .filter((k) => k !== '$schema')
      .map((k) =>
        buildNode(schema.properties[k], {
          name: k,
          required: req.has(k),
          schemas,
          seen
        })
      )
  } else if (
    primary === 'object' &&
    schema.additionalProperties &&
    typeof schema.additionalProperties === 'object'
  ) {
    node.type = 'map'
    node.itemsOf = buildNode(schema.additionalProperties, { schemas, seen })
  } else if (primary === 'array') {
    node.type = 'array'
    node.itemsOf = schema.items
      ? buildNode(schema.items, { schemas, seen })
      : { type: 'any' }
  } else {
    node.type = primary || 'object'
  }
  return node
}

// A representative JSON value for a request-body example (required fields only).
const sampleValue = (schema, { schemas, seen }) => {
  if (schema.$ref) {
    const rn = refName(schema.$ref)
    if (seen.has(rn)) return null
    const next = new Set(seen)
    next.add(rn)
    return sampleValue(schemas[rn], { schemas, seen: next })
  }
  if (schema.enum) return schema.enum[0]
  const { primary } = splitType(schema.type)
  switch (primary) {
    case 'object': {
      if (schema.properties) {
        const req = new Set(schema.required || [])
        const obj = {}
        for (const k of Object.keys(schema.properties)) {
          if (k === '$schema' || !req.has(k)) continue
          obj[k] = sampleValue(schema.properties[k], { schemas, seen })
        }
        return obj
      }
      if (schema.additionalProperties && typeof schema.additionalProperties === 'object') {
        return { key: sampleValue(schema.additionalProperties, { schemas, seen }) }
      }
      return {}
    }
    case 'array':
      return schema.items ? [sampleValue(schema.items, { schemas, seen })] : []
    case 'integer':
    case 'number':
      return 0
    case 'boolean':
      return true
    default:
      return 'string'
  }
}

// A sample value to substitute into a path segment ({id} → 1).
const samplePathValue = (param) => {
  if (param.enum?.length) return String(param.enum[0])
  return param.type === 'integer' || param.type === 'number' ? '1' : 'value'
}

const buildCurl = (method, path, params, bodyExample) => {
  let url = API_HOST + path
  for (const p of params) {
    if (p.in === 'path') url = url.replace(`{${p.name}}`, samplePathValue(p))
  }
  const verb = method.toUpperCase()
  const lines = [verb === 'GET' ? `curl "${url}"` : `curl -X ${verb} "${url}"`]
  lines.push('  -H "Authorization: Bearer nm_live_<YOUR_KEY>"')
  if (bodyExample !== undefined) {
    lines.push('  -H "Content-Type: application/json"')
    lines.push(`  -d '${JSON.stringify(bodyExample)}'`)
  }
  return lines.join(' \\\n')
}

const buildParams = (rawParams = []) => {
  const mapped = rawParams.map((p) => {
    const s = p.schema || {}
    const { primary } = splitType(s.type)
    const param = {
      name: p.name,
      in: p.in,
      required: !!p.required,
      type: primary || 'string'
    }
    if (s.format) param.format = s.format
    if (p.description || s.description) param.doc = p.description || s.description
    if (s.enum) param.enum = s.enum
    return param
  })
  // Path parameters first (fundamental + always required), then query.
  return mapped.sort((a, b) => {
    if (a.in === b.in) return 0
    return a.in === 'path' ? -1 : 1
  })
}

const jsonContent = (content) =>
  content?.['application/json'] || content?.['application/problem+json']

const buildOperation = (method, path, op, { schemas, scope }) => {
  const params = buildParams(op.parameters)

  let requestBody
  let bodyExample
  const bodySchema = jsonContent(op.requestBody?.content)?.schema
  if (bodySchema) {
    requestBody = buildNode(bodySchema, { schemas, seen: new Set() })
    bodyExample = sampleValue(bodySchema, { schemas, seen: new Set() })
  }

  const responses = Object.entries(op.responses || {}).map(([status, res]) => {
    const schema = jsonContent(res.content)?.schema
    return {
      status,
      description: res.description || '',
      ...(schema && { schema: buildNode(schema, { schemas, seen: new Set() }) })
    }
  })

  return {
    id: op.operationId,
    method,
    path,
    summary: op.summary || '',
    ...(op.description && { description: op.description }),
    scope,
    params,
    ...(requestBody && { requestBody }),
    responses,
    curl: buildCurl(method, path, params, bodyExample)
  }
}

const METHODS = ['get', 'post', 'put', 'patch', 'delete']

const buildFace = (faceDef) => {
  const spec = parseYaml(readFileSync(faceDef.file, 'utf8'))
  const schemas = spec.components?.schemas || {}

  // Group operations by their OpenAPI tag (both faces carry a single tag today,
  // so each face has one group — the model keeps the layer for future tags).
  const groups = new Map()
  for (const [path, item] of Object.entries(spec.paths || {})) {
    for (const method of METHODS) {
      const op = item[method]
      if (!op) continue
      const tag = op.tags?.[0] || 'default'
      if (!groups.has(tag)) groups.set(tag, [])
      groups.get(tag).push(
        buildOperation(method, path, op, { schemas, scope: faceDef.scope })
      )
    }
  }

  return {
    key: faceDef.key,
    label: faceDef.label,
    title: spec.info?.title || faceDef.label,
    baseUrl: API_HOST,
    groups: [...groups.entries()].map(([tag, operations]) => ({
      tag,
      // e.g. "catalog-public" → "Catalog" (the -public suffix is redundant with
      // the face key; the sidebar collapses a lone group into the face).
      title: tag
        .replace(/-public$/, '')
        .split(/[-_]/)
        .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
        .join(' '),
      operations: operations.sort((a, b) => a.path.localeCompare(b.path))
    }))
  }
}

const model = { faces: FACES.map(buildFace) }

const opCount = model.faces.reduce(
  (n, f) => n + f.groups.reduce((m, g) => m + g.operations.length, 0),
  0
)
if (opCount !== EXPECTED_OPERATION_COUNT) {
  throw new Error(
    `docs-model coverage guard: expected ${EXPECTED_OPERATION_COUNT} operations, built ${opCount}`
  )
}

const out = `/**
 * Auto-generated by scripts/sync-specs.mjs — do not edit by hand.
 * Run \`pnpm --filter developer sync:specs\` after the public specs change.
 *
 * The Tier-A public OpenAPI spec (catalog face) projected into the
 * render-friendly DocsModel the /docs/** reference pages consume.
 */
import type { DocsModel } from '~~/shared/types/docs'

export const docsModel: DocsModel = ${JSON.stringify(model, null, 2)}
`

mkdirSync(dirname(OUTPUT), { recursive: true })
writeFileSync(OUTPUT, out)
console.log(
  `Wrote docs model → app/generated/docs-model.ts (${model.faces.length} faces, ${opCount} operations)`
)
