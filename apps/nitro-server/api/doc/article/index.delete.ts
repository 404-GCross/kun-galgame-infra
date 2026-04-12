import { prisma } from '~~/prisma/prisma'
import { deleteDocArticleSchema } from '~/validations/doc'

export default defineEventHandler(async (event) => {
  const userInfo = await getCookieTokenInfo(event)
  if (!userInfo) {
    return kunError(event, '请先登录', 205)
  }
  if (userInfo.role <= 2) {
    return kunError(event, '权限不足，无法执行该操作')
  }

  const input = kunParseDeleteQuery(event, deleteDocArticleSchema)
  if (typeof input === 'string') {
    return kunError(event, input)
  }

  await prisma.doc_article.delete({
    where: { id: input.articleId }
  })

  return '删除文档成功！'
})
