import { prisma } from '~~/prisma/prisma'
import { getDocTagListSchema } from '~/validations/doc'
import type { Prisma } from '~~/prisma/generated/prisma/client'

export default defineEventHandler(async (event) => {
  const input = kunParseGetQuery(event, getDocTagListSchema)
  if (typeof input === 'string') {
    return kunError(event, input)
  }

  const { page, limit, keyword } = input
  const skip = (page - 1) * limit

  const where: Prisma.doc_tagWhereInput = keyword
    ? {
        OR: [
          { title: { contains: keyword, mode: 'insensitive' } },
          { slug: { contains: keyword, mode: 'insensitive' } }
        ]
      }
    : {}

  const [rows, totalCount] = await prisma.$transaction([
    prisma.doc_tag.findMany({
      where,
      orderBy: [{ title: 'asc' }],
      skip,
      take: limit
    }),
    prisma.doc_tag.count({ where })
  ])

  const tags: DocTagItem[] = rows.map((tag) => ({
    id: tag.id,
    slug: tag.slug,
    title: tag.title,
    description: tag.description,
    created: tag.created,
    updated: tag.updated
  }))

  const response: DocTagListResponse = {
    tags,
    totalCount,
    page,
    limit
  }

  return response
})
