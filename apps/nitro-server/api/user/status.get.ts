import { prisma } from '~~/prisma/prisma'

export default defineEventHandler(async (event) => {
  const userInfo = await getCookieTokenInfo(event)
  if (!userInfo) {
    return kunError(event, '用户登录失效', 205)
  }
  const userId = userInfo.id

  const user = await prisma.user.findUnique({
    where: { id: userId }
  })
  if (!user) {
    return kunError(event, '未找到该用户')
  }

  const message = await prisma.message.count({
    where: { receiver_id: userId, status: 'unread' }
  })

  const messageAdmin = await prisma.system_message.count({
    where: { status: 'unread' }
  })

  const chatMessage = await prisma.chat_message.count({
    where: {
      sender_id: {
        not: userId
      },
      chat_room: {
        participant: {
          some: {
            user_id: userId
          }
        }
      },
      read_by: {
        none: {
          user_id: userId
        }
      }
    }
  })

  const responseData: HomeUserStatus = {
    moemoepoints: user.moemoepoint,
    isCheckIn: user.daily_check_in === 1,
    hasNewMessage: !!(message || messageAdmin || chatMessage)
  }

  return responseData
})
