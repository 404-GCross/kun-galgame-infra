#!/usr/bin/env node
/**
 * Builds the typed documentation model the self-built API reference (/docs/**)
 * renders. Reads two Tier-A catalog specs from the repo's docs/, parses them,
 * dereferences $ref pointers inline into render-friendly schema trees, derives
 * parameter tables / request bodies / responses, generates a ready-to-run curl
 * example per operation, and writes `app/generated/docs-model.ts`.
 *
 * The generated file is committed (same pattern as app/assets/kun-icons.ts): a
 * derived build artifact, never hand-edited. Re-run after the specs change:
 *   pnpm --filter developer sync:specs
 *
 * The galgame face was dropped at wave 146 (2026-07-30): its /v1/galgame
 * projection was delisted and its spec deleted.
 *
 * Two spec files, three faces. `public-openapi.yaml` carries catalog (API-key,
 * read-only) and playtime (user-token; scope differs per method). Modelling
 * those as one face published five playtime operations as `catalog:read` with
 * an `nm_live_` key in the curl sample — a credential that face rejects.
 * `openapi.yaml` is the user-edit subset only: the third face, `edit`, claims
 * `/api/v1/user/catalog/edit` and names every op under that prefix as include
 * or exclude. The rest of that spec (S2S, claims, covers, submit) is a
 * first-party surface and is not portal-documented.
 */
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { parse as parseYaml } from 'yaml'

const __dirname = dirname(fileURLToPath(import.meta.url))
const REPO_ROOT = join(__dirname, '..', '..', '..')
const OUTPUT = join(__dirname, '..', 'app/generated/docs-model.ts')

const API_HOST = 'https://api.nextmoe.dev'

const PUBLIC_SPEC = join(REPO_ROOT, 'docs/catalog/public-openapi.yaml')
const CATALOG_SPEC = join(REPO_ROOT, 'docs/catalog/openapi.yaml')

// A face is a path prefix plus the credential that prefix accepts. The spec
// itself carries neither: OpenAPI security schemes are not emitted by the
// public gen target, and scope lives in prose. Both are derived here, from the
// prefix and the method, and they are the only place the reference pages learn
// which credential to print.
const FACES = [
  {
    key: 'catalog',
    label: 'Catalog',
    file: PUBLIC_SPEC,
    prefix: '/v1/catalog',
    title: (spec) => spec.info?.title || 'Catalog',
    scope: () => 'catalog:read',
    auth: {
      kind: 'api_key',
      curl: 'Authorization: Bearer nm_live_<YOUR_KEY>',
      display: 'Authorization: Bearer nm_live_…',
      note: '机器 API 密钥,服务端持有'
    }
  },
  {
    key: 'playtime',
    label: 'Playtime',
    file: PUBLIC_SPEC,
    prefix: '/v1/playtime',
    title: () => 'NextMoe Open API — Playtime',
    scope: (method) => (method === 'get' ? 'playtime:read' : 'playtime:write'),
    auth: {
      kind: 'user_token',
      curl: 'Authorization: Bearer <ACCESS_TOKEN>',
      display: 'Authorization: Bearer <用户访问令牌>',
      note: '用户授权后拿到的访问令牌,不是 API 密钥'
    }
  },
  {
    key: 'edit',
    label: 'Edit',
    file: CATALOG_SPEC,
    prefix: '/api/v1/user/catalog/edit',
    include: [
      'getEditSchemaUser',
      'getEditSnapshotUser',
      'createEditProposalUser',
      'listEditProposalsUser',
      'getEditProposalUser',
      'withdrawEditProposalUser'
    ],
    title: () => 'NextMoe Open API — Edit',
    scope: () => 'catalog:edit',
    auth: {
      kind: 'user_token',
      curl: 'Authorization: Bearer <ACCESS_TOKEN>',
      display: 'Authorization: Bearer <用户访问令牌>',
      note: '用户授权后的访问令牌(OAuth 授权码 + PKCE),需 catalog:edit scope'
    },
    notes: [
      '第三方应用的令牌恒为「只提案」姿态：审核、自动合入、撤销他人提案永不可；schema 投影对它 can_review=false。',
      '每用户未决提案帽 20：经第三方应用创建提案时，该用户 open 状态提案已达 20 条则返回 429。',
      '引用类校验发生在批准合并时：提案保存成功不等于能合入，审核者批准时可能得到 422。',
      '准入需要两步：应用经 user_login 自助申请 catalog:edit scope；应用的 client 还须由平台绑定目录租户（catalog_site）后本面才放行——目前为人工开通，请联系平台。'
    ]
  }
]

// Third-party tokens always 403 on these four; documenting them on the portal
// would imply they are callable. The completeness guard below requires every
// operation under /api/v1/user/catalog/edit to appear in include or here.
const EDIT_EXCLUDED_OPERATION_IDS = [
  'amendEditProposalUser',
  'declineEditProposalUser',
  'mergeEditProposalUser',
  'revertEditEntityUser'
]

const EXPECTED_OPERATION_COUNTS = { catalog: 25, playtime: 5, edit: 6 }

const refName = (ref) => ref.split('/').pop()

const splitType = (rawType) => {
  const types = Array.isArray(rawType) ? rawType : rawType ? [rawType] : []
  return {
    primary: types.find((t) => t !== 'null'),
    nullable: types.includes('null')
  }
}

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

const samplePathValue = (param) => {
  if (param.enum?.length) return String(param.enum[0])
  return param.type === 'integer' || param.type === 'number' ? '1' : 'value'
}

const buildCurl = (method, path, params, bodyExample, authHeader) => {
  let url = API_HOST + path
  for (const p of params) {
    if (p.in === 'path') url = url.replace(`{${p.name}}`, samplePathValue(p))
  }
  const verb = method.toUpperCase()
  const lines = [verb === 'GET' ? `curl "${url}"` : `curl -X ${verb} "${url}"`]
  lines.push(`  -H "${authHeader}"`)
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
  return mapped.sort((a, b) => {
    if (a.in === b.in) return 0
    return a.in === 'path' ? -1 : 1
  })
}

const jsonContent = (content) =>
  content?.['application/json'] || content?.['application/problem+json']

const buildOperation = (method, path, op, { schemas, scope, authHeader }) => {
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
    curl: buildCurl(method, path, params, bodyExample, authHeader)
  }
}

const METHODS = ['get', 'post', 'put', 'patch', 'delete']

const buildFace = (faceDef, specs) => {
  const spec = specs.get(faceDef.file)
  const schemas = spec.components?.schemas || {}

  const groups = new Map()
  for (const [path, item] of Object.entries(spec.paths || {})) {
    if (!path.startsWith(faceDef.prefix)) continue
    for (const method of METHODS) {
      const op = item[method]
      if (!op) continue
      if (faceDef.include && !faceDef.include.includes(op.operationId)) continue
      const tag = op.tags?.[0] || 'default'
      if (!groups.has(tag)) groups.set(tag, [])
      groups.get(tag).push(
        buildOperation(method, path, op, {
          schemas,
          scope: faceDef.scope(method),
          authHeader: faceDef.auth.curl
        })
      )
    }
  }

  if (faceDef.include) {
    const builtIds = new Set(
      [...groups.values()].flatMap((ops) => ops.map((o) => o.id))
    )
    for (const id of faceDef.include) {
      if (!builtIds.has(id)) {
        throw new Error(
          `docs-model coverage guard: included operation ${id} was not found under ${faceDef.prefix}`
        )
      }
    }
  }

  return {
    key: faceDef.key,
    label: faceDef.label,
    title: faceDef.title(spec),
    baseUrl: API_HOST,
    prefix: faceDef.prefix,
    auth: faceDef.auth,
    ...(faceDef.notes?.length && { notes: faceDef.notes }),
    groups: [...groups.entries()].map(([tag, operations]) => ({
      tag,
      title: tag
        .replace(/-public$/, '')
        .split(/[-_]/)
        .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
        .join(' '),
      operations: operations.sort((a, b) => a.path.localeCompare(b.path))
    }))
  }
}

const specs = new Map()
for (const faceDef of FACES) {
  if (!specs.has(faceDef.file)) {
    specs.set(faceDef.file, parseYaml(readFileSync(faceDef.file, 'utf8')))
  }
}

const model = { faces: FACES.map((f) => buildFace(f, specs)) }

const faceOpCount = (f) =>
  f.groups.reduce((m, g) => m + g.operations.length, 0)

for (const face of model.faces) {
  const want = EXPECTED_OPERATION_COUNTS[face.key]
  const got = faceOpCount(face)
  if (want !== got) {
    throw new Error(
      `docs-model coverage guard: face ${face.key} expected ${want} operations, built ${got}`
    )
  }
}

// A path the prefixes do not claim would vanish from the reference with every
// other guard still green, which is how five playtime operations shipped
// documented as catalog ones. Full-coverage applies to public-openapi.yaml
// only — openapi.yaml is a first-party spec whose unclaimed prefixes must
// not trip this guard.
const publicSpec = specs.get(PUBLIC_SPEC)
const claimed = new Set(
  model.faces
    .filter((f) => FACES.find((d) => d.key === f.key)?.file === PUBLIC_SPEC)
    .flatMap((f) => f.groups.flatMap((g) => g.operations.map((o) => o.path)))
)
for (const path of Object.keys(publicSpec.paths || {})) {
  if (!claimed.has(path)) {
    throw new Error(
      `docs-model coverage guard: spec path ${path} belongs to no face — add one to FACES`
    )
  }
}

const editDef = FACES.find((f) => f.key === 'edit')
const catalogSpec = specs.get(CATALOG_SPEC)
const namedEditOps = new Set([
  ...(editDef.include || []),
  ...EDIT_EXCLUDED_OPERATION_IDS
])
for (const [path, item] of Object.entries(catalogSpec.paths || {})) {
  if (!path.startsWith(editDef.prefix)) continue
  for (const method of METHODS) {
    const op = item[method]
    if (!op) continue
    if (!namedEditOps.has(op.operationId)) {
      throw new Error(
        `docs-model completeness guard: ${op.operationId} under ${editDef.prefix} is neither included nor excluded`
      )
    }
  }
}

const opCount = model.faces.reduce((n, f) => n + faceOpCount(f), 0)

const out = `/**
 * Auto-generated by scripts/sync-specs.mjs — do not edit by hand.
 * Run \`pnpm --filter developer sync:specs\` after the public specs change.
 *
 * Two Tier-A catalog specs projected into the render-friendly DocsModel
 * the /docs/** reference pages consume, one entry per public face.
 */
import type { DocsModel } from '~~/shared/types/docs'

export const docsModel: DocsModel = ${JSON.stringify(model, null, 2)}
`

mkdirSync(dirname(OUTPUT), { recursive: true })
writeFileSync(OUTPUT, out)
const breakdown = model.faces
  .map((f) => `${f.key} ${faceOpCount(f)}`)
  .join(', ')
console.log(
  `Wrote docs model → app/generated/docs-model.ts (${model.faces.length} faces, ${opCount} operations: ${breakdown})`
)
