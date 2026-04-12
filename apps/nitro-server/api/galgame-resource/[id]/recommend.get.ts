import { prisma } from '~~/prisma/prisma'
import { getGalgameResourceDetailSchema } from '~/validations/galgame-resource'

const recommendationInclude = (userId?: number) => ({
  like: {
    where: {
      user_id: userId
    }
  },
  _count: {
    select: { like: true }
  },
  user: {
    select: {
      id: true,
      name: true,
      avatar: true
    }
  },
  galgame: {
    select: {
      id: true,
      name_en_us: true,
      name_ja_jp: true,
      name_zh_cn: true,
      name_zh_tw: true
    }
  }
})

export const getRecommendedResources = async (
  galgameId: number,
  excludeIds: number[],
  size: number,
  userId?: number
) => {
  const baseWhere = {
    status: 0
  }

  const sameGalgame = await prisma.galgame_resource.findMany({
    where: {
      ...baseWhere,
      galgame_id: galgameId,
      ...(excludeIds.length ? { id: { notIn: excludeIds } } : {})
    },
    take: size,
    orderBy: { download: 'desc' },
    include: recommendationInclude(userId)
  })

  if (sameGalgame.length >= size) {
    return sameGalgame
  }

  const exclude = [...excludeIds, ...sameGalgame.map((resource) => resource.id)]
  const fallbackNeeded = size - sameGalgame.length
  const fallbackPool = await prisma.galgame_resource.findMany({
    where: {
      ...baseWhere,
      download: { gt: 500 },
      ...(exclude.length ? { id: { notIn: exclude } } : {})
    },
    take: fallbackNeeded * 3 || fallbackNeeded,
    orderBy: { updated: 'desc' },
    include: recommendationInclude(userId)
  })
  const fallback = shuffleArray(fallbackPool).slice(0, fallbackNeeded)

  return [...sameGalgame, ...fallback]
}

export default defineEventHandler(async (event) => {
  const input = await kunParsePutBody(event, getGalgameResourceDetailSchema)
  if (typeof input === 'string') {
    return kunError(event, input)
  }
})
