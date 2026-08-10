
export type DocsMethod = 'get' | 'post' | 'put' | 'patch' | 'delete'

export type DocsFaceKey = 'catalog'

export interface DocsSchemaNode {
  name?: string
  type: string
  format?: string
  doc?: string
  required?: boolean
  nullable?: boolean
  enum?: string[]
  children?: DocsSchemaNode[]
  itemsOf?: DocsSchemaNode
}

export interface DocsParam {
  name: string
  in: 'query' | 'path'
  required: boolean
  type: string
  format?: string
  doc?: string
  enum?: string[]
}

export interface DocsResponse {
  status: string
  description: string
  schema?: DocsSchemaNode
}

export interface DocsOperation {
  id: string
  method: DocsMethod
  path: string
  summary: string
  description?: string
  scope: string
  params: DocsParam[]
  requestBody?: DocsSchemaNode
  responses: DocsResponse[]
  curl: string
}

export interface DocsGroup {
  tag: string
  title: string
  operations: DocsOperation[]
}

export interface DocsFace {
  key: DocsFaceKey
  label: string
  title: string
  baseUrl: string
  groups: DocsGroup[]
}

export interface DocsModel {
  faces: DocsFace[]
}
