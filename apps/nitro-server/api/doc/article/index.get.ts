import { prisma } from '~~/prisma/prisma'
import { getDocArticleListSchema } from '~/validations/doc'
import {
  docArticleListSelect,
  mapDocArticleListItem
} from '~~/server/utils/doc/article'
import type { Prisma } from '~~/prisma/generated/prisma/client'

const orderFieldKeyMap: Record<
  string,
  keyof Prisma.doc_articleOrderByWithRelationInput
> = {
  published_time: 'published_time',
  created: 'created',
  view: 'view',
  updated: 'updated'
}

export default defineEventHandler(async (event) => {
  const input = kunParseGetQuery(event, getDocArticleListSchema)
  if (typeof input === 'string') {
    return kunError(event, input)
  }

  const {
    page,
    limit,
    categoryId,
    status,
    isPin,
    tagId,
    keyword,
    orderBy,
    sortOrder
  } = input
  const skip = (page - 1) * limit

  const where: Prisma.doc_articleWhereInput = {}

  if (keyword) {
    where.OR = [
      { title: { contains: keyword, mode: 'insensitive' } },
      { description: { contains: keyword, mode: 'insensitive' } }
    ]
  }

  if (typeof categoryId === 'number') {
    where.category_id = categoryId
  }

  if (typeof status === 'number') {
    where.status = status
  }

  if (typeof isPin === 'boolean') {
    where.is_pin = isPin
  }

  if (typeof tagId === 'number') {
    where.tags = {
      some: {
        doc_tag_id: tagId
      }
    }
  }

  const orderByField = orderFieldKeyMap[orderBy] ?? 'published_time'

  const [rows, totalCount] = await prisma.$transaction([
    prisma.doc_article.findMany({
      where,
      skip,
      take: limit,
      orderBy: [{ [orderByField]: sortOrder }, { id: 'desc' }],
      select: docArticleListSelect
    }),
    prisma.doc_article.count({ where })
  ])

  const response: DocArticleListResponse = {
    articles: rows.map(mapDocArticleListItem),
    totalCount,
    page,
    limit
  }

  return response
})
