import { prisma } from '~~/prisma/prisma'

export default defineEventHandler(async () => {
  const data = await prisma.galgame.findMany({
    orderBy: {
      created: 'desc'
    },
    take: 10,
    include: {
      user: {
        select: {
          id: true,
          name: true,
          avatar: true
        }
      }
    }
  })

  const galgames: RSSGalgame[] = data.map((galgame) => ({
    id: galgame.id,
    name: getPreferredLanguageText({
      'en-us': galgame.name_en_us,
      'ja-jp': galgame.name_ja_jp,
      'zh-cn': galgame.name_zh_cn,
      'zh-tw': galgame.name_zh_tw
    }),
    banner: galgame.banner,
    user: galgame.user,
    description: getPreferredLanguageText({
      'en-us': galgame.intro_en_us,
      'ja-jp': galgame.intro_ja_jp,
      'zh-cn': galgame.intro_zh_cn,
      'zh-tw': galgame.intro_zh_tw
    }).slice(0, 233),
    created: galgame.created
  }))

  return galgames
})
