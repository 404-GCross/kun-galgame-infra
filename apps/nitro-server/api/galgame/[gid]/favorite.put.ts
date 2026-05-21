import { prisma } from '~~/prisma/prisma'
import { updateGalgameFavoriteSchema } from '~/validations/galgame'

export default defineEventHandler(async (event) => {
  const input = await kunParsePutBody(event, updateGalgameFavoriteSchema)
  if (typeof input === 'string') {
    return kunError(event, input)
  }
  const userInfo = await getCookieTokenInfo(event)
  if (!userInfo) {
    return kunError(event, '用户登录失效', 205)
  }

  const userId = userInfo.id
  const galgameId = input.galgameId

  const galgame = await prisma.galgame.findUnique({
    where: { id: galgameId, status: { not: 1 } },
    include: {
      favorite: {
        where: {
          user_id: userId
        }
      }
    }
  })
  if (!galgame) {
    return kunError(event, '未找到这个 Galgame')
  }

  const isFavoriteGalgame = galgame.favorite.length > 0

  return prisma.$transaction(async (prisma) => {
    if (isFavoriteGalgame) {
      await prisma.galgame_favorite.delete({
        where: {
          galgame_id_user_id: {
            user_id: userId,
            galgame_id: galgameId
          }
        }
      })
    } else {
      await prisma.galgame_favorite.create({
        data: {
          user_id: userId,
          galgame_id: galgameId
        }
      })
    }

    if (userId !== galgame.user_id) {
      await prisma.user.update({
        where: { id: userId },
        data: {
          moemoepoint: {
            increment: isFavoriteGalgame ? -1 : 1
          }
        }
      })

      if (!isFavoriteGalgame) {
        await createDedupMessage(
          prisma,
          userId,
          galgame.user_id,
          'favorite',
          galgame.name_zh_cn ||
            galgame.name_zh_tw ||
            galgame.name_ja_jp ||
            galgame.name_en_us,
          undefined,
          galgameId
        )
      }
    }

    return 'MOEMOE favorite galgame operation successfully!'
  })
})
