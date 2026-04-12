import { prisma } from '~~/prisma/prisma'
import { updateDocTagSchema } from '~/validations/doc'

export default defineEventHandler(async (event) => {
  const userInfo = await getCookieTokenInfo(event)
  if (!userInfo) {
    return kunError(event, '请先登录', 205)
  }
  if (userInfo.role <= 2) {
    return kunError(event, '权限不足，无法执行该操作')
  }

  const input = await kunParsePutBody(event, updateDocTagSchema)
  if (typeof input === 'string') {
    return kunError(event, input)
  }

  const { tagId, ...data } = input

  const tag = await prisma.doc_tag.update({
    where: { id: tagId },
    data: {
      slug: data.slug,
      title: data.title,
      description: data.description
    }
  })

  const result: DocTagItem = {
    id: tag.id,
    slug: tag.slug,
    title: tag.title,
    description: tag.description,
    created: tag.created,
    updated: tag.updated
  }

  return result
})
