import { prisma } from '~~/prisma/prisma'
import { updateGalgameRatingLikeSchema } from '~/validations/galgame-rating'

export default defineEventHandler(async (event) => {
  const input = await kunParsePutBody(event, updateGalgameRatingLikeSchema)
  if (typeof input === 'string') {
    return kunError(event, input)
  }

  const userInfo = await getCookieTokenInfo(event)
  if (!userInfo) {
    return kunError(event, '用户登录失效', 205)
  }
  const userId = userInfo.id

  const rating = await prisma.galgame_rating.findUnique({
    where: { id: input.galgameRatingId },
    include: {
      like: { where: { user_id: userId } }
    }
  })
  if (!rating) {
    return kunError(event, '评分不存在')
  }
  if (rating.user_id === userId) {
    return kunError(event, '不能给自己的评分点赞')
  }

  const isLiked = rating.like.length > 0

  return prisma.$transaction(async (prisma) => {
    if (isLiked) {
      await prisma.galgame_rating_like.delete({
        where: {
          galgame_rating_id_user_id: {
            user_id: userId,
            galgame_rating_id: input.galgameRatingId
          }
        }
      })
    } else {
      await prisma.galgame_rating_like.create({
        data: {
          galgame_rating_id: input.galgameRatingId,
          user_id: userId
        }
      })
    }

    await prisma.user.update({
      where: { id: rating.user_id },
      data: { moemoepoint: { increment: isLiked ? -1 : 1 } }
    })

    await createDedupMessage(
      prisma,
      userId,
      rating.user_id,
      'liked',
      rating.short_summary.slice(0, 233),
      undefined,
      rating.galgame_id
    )

    return 'MOEMOE like galgame rating operation successfully!'
  })
})
