import { hash, compare } from 'bcrypt'
import { prisma } from '~~/prisma/prisma'
import { userUpdatePasswordSchema } from '~/validations/user'

export default defineEventHandler(async (event) => {
  const input = await kunParsePutBody(event, userUpdatePasswordSchema)
  if (typeof input === 'string') {
    return kunError(event, input)
  }

  const { oldPassword, newPassword } = input

  const userInfo = await getCookieTokenInfo(event)
  if (!userInfo) {
    return kunError(event, '用户登录失效', 205)
  }
  const userId = userInfo.id

  const user = await prisma.user.findUnique({
    where: { id: userId }
  })
  if (!user) {
    return kunError(event, '未找到用户')
  }

  const isCorrectPassword = await compare(oldPassword, user.password)
  if (!isCorrectPassword) {
    return kunError(event, '旧密码错误')
  }

  const hashedPassword = await hash(newPassword, 7)
  await prisma.user.update({
    where: { id: userId },
    data: { password: hashedPassword }
  })

  return 'Moe Moe'
})
