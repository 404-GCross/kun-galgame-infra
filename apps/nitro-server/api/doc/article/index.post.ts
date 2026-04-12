import { prisma } from '~~/prisma/prisma'
import { createDocArticleSchema } from '~/validations/doc'
import {
  docArticleDetailSelect,
  mapDocArticleDetail
} from '~~/server/utils/doc/article'

export default defineEventHandler(async (event) => {
  const userInfo = await getCookieTokenInfo(event)
  if (!userInfo) {
    return kunError(event, '请先登录', 205)
  }
  if (userInfo.role <= 2) {
    return kunError(event, '权限不足，无法执行该操作')
  }

  const input = await kunParsePostBody(event, createDocArticleSchema)
  if (typeof input === 'string') {
    return kunError(event, input)
  }

  const category = await prisma.doc_category.findUnique({
    where: { id: input.categoryId },
    select: { id: true }
  })
  if (!category) {
    return kunError(event, '文档分类不存在')
  }

  const uniqueTagIds = Array.from(new Set(input.tagIds))
  if (uniqueTagIds.length) {
    const tags = await prisma.doc_tag.findMany({
      where: { id: { in: uniqueTagIds } },
      select: { id: true }
    })
    if (tags.length !== uniqueTagIds.length) {
      return kunError(event, '存在无效的标签 ID')
    }
  }

  const article = await prisma.$transaction(async (tx) => {
    const docPath = `/doc/${input.slug}`
    const createdArticle = await tx.doc_article.create({
      data: {
        title: input.title,
        slug: input.slug,
        path: docPath,
        description: input.description,
        banner: input.banner,
        status: input.status,
        is_pin: input.isPin,
        content_markdown: input.contentMarkdown,
        category_id: input.categoryId,
        author_id: userInfo.uid
      }
    })

    if (uniqueTagIds.length) {
      await tx.doc_article_tag_relation.createMany({
        data: uniqueTagIds.map((tagId) => ({
          doc_article_id: createdArticle.id,
          doc_tag_id: tagId
        })),
        skipDuplicates: true
      })
    }

    return tx.doc_article.findUnique({
      where: { id: createdArticle.id },
      select: docArticleDetailSelect
    })
  })

  if (!article) {
    return kunError(event, '创建文档失败，请稍后重试')
  }

  const result: DocArticleDetail = await mapDocArticleDetail(article)
  return result
})
