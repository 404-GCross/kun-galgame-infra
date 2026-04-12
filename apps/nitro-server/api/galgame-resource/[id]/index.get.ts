import { prisma } from '~~/prisma/prisma'
import { getGalgameResourceDetailSchema } from '~/validations/galgame-resource'
import { toResourceCard } from '~~/server/utils/card/toResourceCard'
import { getRecommendedResources } from './recommend.get'

export default defineEventHandler(async (event) => {
  const input = kunParseGetQuery(event, getGalgameResourceDetailSchema)
  if (typeof input === 'string') {
    return kunError(event, input)
  }

  const userInfo = await getCookieTokenInfo(event)
  const userId = userInfo?.uid

  const data = await prisma.galgame_resource.findUnique({
    where: { id: input.resourceId },
    include: {
      user: {
        select: {
          id: true,
          name: true,
          avatar: true
        }
      },
      like: {
        where: {
          user_id: userId
        }
      },
      _count: {
        select: { like: true }
      },
      link: {
        select: { url: true }
      },
      galgame: {
        select: {
          id: true,
          name_en_us: true,
          name_ja_jp: true,
          name_zh_cn: true,
          name_zh_tw: true,
          banner: true,
          content_limit: true,
          resource_update_time: true,
          view: true,
          original_language: true,
          age_limit: true,
          resource: {
            select: {
              type: true,
              platform: true,
              language: true
            }
          }
        }
      }
    }
  })
  if (!data) {
    return 'not found'
  }

  await prisma.galgame_resource.update({
    where: { id: input.resourceId },
    data: { view: { increment: 1 } }
  })

  const linkDomain = [
    ...new Set(data.link.map((l) => getDomain(l.url, { getRootDomain: true })))
  ]

  const resource: GalgameResource = {
    id: data.id,
    view: data.view,
    galgameId: data.galgame_id,
    user: data.user,
    type: data.type,
    language: data.language,
    platform: data.platform,
    size: data.size,
    status: data.status,
    download: data.download,
    linkDomain: linkDomain.length ? linkDomain[0]! : '',
    note: data.note,
    likeCount: data._count.like,
    isLiked: data.like.length > 0,
    edited: data.edited,
    created: data.created
  }

  const galgame: GalgameResourceSummary = {
    id: data.galgame.id,
    name: {
      'en-us': data.galgame.name_en_us,
      'ja-jp': data.galgame.name_ja_jp,
      'zh-cn': data.galgame.name_zh_cn,
      'zh-tw': data.galgame.name_zh_tw
    },
    banner: data.galgame.banner,
    contentLimit: data.galgame.content_limit,
    resourceUpdateTime: data.galgame.resource_update_time,
    view: data.galgame.view,
    originalLanguage: data.galgame.original_language,
    ageLimit: data.galgame.age_limit as KunAgeLimit,
    platform: uniqueArray(data.galgame.resource.map((item) => item.platform)),
    language: uniqueArray(data.galgame.resource.map((item) => item.language)),
    type: uniqueArray(data.galgame.resource.map((item) => item.type))
  }

  const recommendationsRaw = await getRecommendedResources(
    data.galgame_id,
    [data.id],
    6,
    userId
  )
  const recommendations = recommendationsRaw.map(toResourceCard)

  const payload: GalgameResourcePageData = {
    galgame,
    resource,
    recommendations
  }

  return payload
})
