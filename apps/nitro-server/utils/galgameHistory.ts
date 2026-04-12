import type { DefaultArgs } from '@prisma/client/runtime/client'
import type { PrismaClient } from '~~/prisma/generated/prisma/client'

export const createGalgameHistory = async (
  prisma: Omit<
    PrismaClient<never, undefined, DefaultArgs>,
    '$connect' | '$disconnect' | '$on' | '$transaction' | '$extends'
  >,
  history: GalgameHistoryCreateRequestData
) => {
  await prisma.galgame_history.create({
    data: history
  })
}
