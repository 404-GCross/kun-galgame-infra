import { prisma } from '~~/prisma/prisma'
import { getGalgameResourceDetailSchema } from '~/validations/galgame'

export default defineEventHandler(async (event) => {
  const input = kunParseGetQuery(event, getGalgameResourceDetailSchema)
  if (typeof input === 'string') {
    return kunError(event, input)
  }

  const userInfo = await getCookieTokenInfo(event)
  const userId = userInfo?.uid

  const data = await prisma.galgame_resource.findUnique({
    where: { id: input.galgameResourceId },
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
      }
    }
  })
  if (!data) {
    kunError(event, '未找到该 Galgame 资源')
    return
  }

  await prisma.galgame_resource.update({
    where: { id: input.galgameResourceId },
    data: { download: { increment: 1 } }
  })

  const resource: GalgameResourceDetailLink = {
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
    linkDomain: '',
    note: data.note,
    likeCount: data._count.like,
    isLiked: data.like.length > 0,
    edited: data.edited,
    created: data.created,
    link: data.link.map((l) => l.url),
    code: data.code,
    password: data.password
  }

  return resource
})
