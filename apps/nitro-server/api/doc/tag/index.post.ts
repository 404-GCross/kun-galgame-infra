import { prisma } from '~~/prisma/prisma'
import { createDocTagSchema } from '~/validations/doc'

export default defineEventHandler(async (event) => {
  const userInfo = await getCookieTokenInfo(event)
  if (!userInfo) {
    return kunError(event, '请先登录', 205)
  }
  if (userInfo.role <= 2) {
    return kunError(event, '权限不足，无法执行该操作')
  }

  const input = await kunParsePostBody(event, createDocTagSchema)
  if (typeof input === 'string') {
    return kunError(event, input)
  }

  const tag = await prisma.doc_tag.create({
    data: {
      slug: input.slug,
      title: input.title,
      description: input.description
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
