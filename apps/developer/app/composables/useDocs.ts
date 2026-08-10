import { docsModel } from '~/generated/docs-model'
import type { DocsFace, DocsOperation } from '~~/shared/types/docs'

export const useDocs = () => {
  const faces = docsModel.faces

  const findFace = (key: string): DocsFace | undefined =>
    faces.find((f) => f.key === key)

  const findOperation = (
    faceKey: string,
    operationId: string
  ):
    | { face: DocsFace; operation: DocsOperation }
    | undefined => {
    const face = findFace(faceKey)
    if (!face) return undefined
    for (const group of face.groups) {
      const operation = group.operations.find((o) => o.id === operationId)
      if (operation) return { face, operation }
    }
    return undefined
  }

  const faceOperationCount = (face: DocsFace): number =>
    face.groups.reduce((n, g) => n + g.operations.length, 0)

  return { faces, findFace, findOperation, faceOperationCount }
}
