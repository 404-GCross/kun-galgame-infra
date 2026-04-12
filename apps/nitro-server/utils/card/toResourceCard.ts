export const toResourceCard = (resource: {
  id: number
  view: number
  galgame_id: number
  user: KunUser
  type: string
  language: string
  platform: string
  size: string
  status: number
  download: number
  created: Date
  edited: Date | null
  like: { id: number }[]
  _count: { like: number }
  galgame: {
    name_en_us: string
    name_ja_jp: string
    name_zh_cn: string
    name_zh_tw: string
  }
}): GalgameResourceCard => ({
  id: resource.id,
  view: resource.view,
  galgameId: resource.galgame_id,
  user: resource.user,
  type: resource.type,
  language: resource.language,
  platform: resource.platform,
  size: resource.size,
  status: resource.status,
  download: resource.download,
  likeCount: resource._count.like,
  isLiked: resource.like.length > 0,
  linkDomain: '',
  edited: resource.edited,
  created: resource.created,
  galgameName: {
    'en-us': resource.galgame.name_en_us,
    'ja-jp': resource.galgame.name_ja_jp,
    'zh-cn': resource.galgame.name_zh_cn,
    'zh-tw': resource.galgame.name_zh_tw
  }
})
