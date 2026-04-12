import { prisma } from '~~/prisma/prisma'
import { createDocCategorySchema } from '~/validations/doc'

export default defineEventHandler(async (event) => {
  const userInfo = await getCookieTokenInfo(event)
  if (!userInfo) {
    return kunError(event, '请先登录', 205)
  }
  if (userInfo.role <= 2) {
    return kunError(event, '权限不足，无法执行该操作')
  }

  const input = await kunParsePostBody(event, createDocCategorySchema)
  if (typeof input === 'string') {
    return kunError(event, input)
  }

  const category = await prisma.doc_category.create({
    data: {
      slug: input.slug,
      title: input.title,
      description: input.description,
      icon: input.icon,
      sort_order: input.sortOrder
    }
  })

  const result: DocCategoryItem = {
    id: category.id,
    slug: category.slug,
    title: category.title,
    description: category.description,
    icon: category.icon,
    sortOrder: category.sort_order,
    created: category.created,
    updated: category.updated
  }

  return result
})
