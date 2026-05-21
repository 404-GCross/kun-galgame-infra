import { prisma } from '~~/prisma/prisma'
import { deleteMessageSchema } from '~/validations/chat'

export default defineEventHandler(async (event) => {
  const userInfo = await getCookieTokenInfo(event)
  if (!userInfo) {
    return kunError(event, '用户登录失效', 205)
  }
  const userId = userInfo.id

  const input = kunParseDeleteQuery(event, deleteMessageSchema)
  if (typeof input === 'string') {
    return kunError(event, input)
  }

  await prisma.message.delete({
    where: { id: input.messageId, receiver_id: userId }
  })

  return 'MOEMOE delete message successfully!'
})
