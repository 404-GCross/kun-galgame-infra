import { prisma } from '~~/prisma/prisma'
import { updateDocArticleSchema } from '~/validations/doc'
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

  const input = await kunParsePutBody(event, updateDocArticleSchema)
  if (typeof input === 'string') {
    return kunError(event, input)
  }

  const article = await prisma.doc_article.findUnique({
    where: { id: input.articleId },
    select: { id: true }
  })
  if (!article) {
    return kunError(event, '文档不存在')
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

  const updatedArticle = await prisma.$transaction(async (tx) => {
    const docPath = `/doc/${input.slug}`
    await tx.doc_article.update({
      where: { id: input.articleId },
      data: {
        title: input.title,
        slug: input.slug,
        path: docPath,
        description: input.description,
        banner: input.banner,
        status: input.status,
        is_pin: input.isPin,
        edited_time: new Date(),
        content_markdown: input.contentMarkdown,
        category_id: input.categoryId
      }
    })

    await tx.doc_article_tag_relation.deleteMany({
      where: { doc_article_id: input.articleId }
    })

    if (uniqueTagIds.length) {
      await tx.doc_article_tag_relation.createMany({
        data: uniqueTagIds.map((tagId) => ({
          doc_article_id: input.articleId,
          doc_tag_id: tagId
        })),
        skipDuplicates: true
      })
    }

    return tx.doc_article.findUnique({
      where: { id: input.articleId },
      select: docArticleDetailSelect
    })
  })

  if (!updatedArticle) {
    return kunError(event, '更新文档失败，请稍后重试')
  }

  const result: DocArticleDetail = await mapDocArticleDetail(updatedArticle)
  return result
})
