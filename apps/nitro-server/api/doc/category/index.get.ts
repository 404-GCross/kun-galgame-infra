import { prisma } from '~~/prisma/prisma'
import { getDocCategoryListSchema } from '~/validations/doc'
import type { Prisma } from '~~/prisma/generated/prisma/client'

export default defineEventHandler(async (event) => {
  const input = kunParseGetQuery(event, getDocCategoryListSchema)
  if (typeof input === 'string') {
    return kunError(event, input)
  }

  const { page, limit, keyword } = input
  const skip = (page - 1) * limit

  const where: Prisma.doc_categoryWhereInput = keyword
    ? {
        OR: [
          { title: { contains: keyword, mode: 'insensitive' } },
          { slug: { contains: keyword, mode: 'insensitive' } }
        ]
      }
    : {}

  const [rows, totalCount] = await prisma.$transaction([
    prisma.doc_category.findMany({
      where,
      orderBy: [{ sort_order: 'asc' }, { id: 'asc' }],
      skip,
      take: limit
    }),
    prisma.doc_category.count({ where })
  ])

  const categories: DocCategoryItem[] = rows.map((category) => ({
    id: category.id,
    slug: category.slug,
    title: category.title,
    description: category.description,
    icon: category.icon,
    sortOrder: category.sort_order,
    created: category.created,
    updated: category.updated
  }))

  const response: DocCategoryListResponse = {
    categories,
    totalCount,
    page,
    limit
  }

  return response
})
