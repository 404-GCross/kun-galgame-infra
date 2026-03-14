const stickers = [
  '/stickers/sticker1.webp',
  '/stickers/sticker2.webp',
  '/stickers/sticker3.webp',
]

export function getRandomSticker(): string {
  const randomIndex = Math.floor(Math.random() * stickers.length)
  return stickers[randomIndex] || '/stickers/default.webp'
}
