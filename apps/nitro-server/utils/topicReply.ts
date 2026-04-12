import type { PrismaClient } from '~~/prisma/generated/prisma/client'
import type { DefaultArgs } from '@prisma/client/runtime/client'

export type PrismaTransactionClient = Omit<
  PrismaClient<never, undefined, DefaultArgs>,
  '$connect' | '$disconnect' | '$on' | '$transaction' | '$extends'
>

export const collectTopicReplyCascadeIds = async (
  rootReplyIds: number[],
  prisma: PrismaTransactionClient
) => {
  const uniqueRoots = Array.from(new Set(rootReplyIds)).filter((id) =>
    Number.isInteger(id)
  )
  if (uniqueRoots.length === 0) {
    return new Set<number>()
  }

  const idsToDelete = new Set<number>(uniqueRoots)
  let queue = uniqueRoots.slice()

  while (queue.length > 0) {
    const directChildren = await prisma.topic_reply.findMany({
      where: {
        target: {
          some: {
            target_reply_id: {
              in: queue
            }
          }
        }
      },
      select: { id: true }
    })

    queue = []

    for (const { id } of directChildren) {
      if (!idsToDelete.has(id)) {
        idsToDelete.add(id)
        queue.push(id)
      }
    }
  }

  return idsToDelete
}

export const deleteTopicRepliesRecursive = async (
  rootReplyIds: number[],
  prisma: PrismaTransactionClient
) => {
  const idsToDelete = await collectTopicReplyCascadeIds(rootReplyIds, prisma)
  if (idsToDelete.size === 0) {
    return idsToDelete
  }

  await prisma.topic_reply.deleteMany({
    where: {
      id: { in: Array.from(idsToDelete) }
    }
  })

  return idsToDelete
}
