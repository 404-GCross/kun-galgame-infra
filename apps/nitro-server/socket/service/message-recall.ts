import { prisma } from '~~/prisma/prisma'

const getRecallPreview = (username: string) => `${username}撤回了一条消息`

export const recallMessage = async (
  uid: number,
  messageId: number
): Promise<ChatMessage> => {
  const targetMessage = await prisma.chat_message.findUnique({
    where: { id: messageId },
    include: {
      sender: {
        select: {
          id: true,
          name: true,
          avatar: true
        }
      },
      read_by: {
        include: {
          user: {
            select: {
              id: true,
              name: true,
              avatar: true
            }
          }
        }
      }
    }
  })

  if (!targetMessage) {
    throw new Error('消息不存在或已被删除')
  }

  if (targetMessage.sender_id !== uid) {
    throw new Error('您只能撤回自己发送的消息')
  }

  if (targetMessage.is_recall) {
    throw new Error('该消息已被撤回')
  }

  const recalledMessage = await prisma.chat_message.update({
    where: { id: messageId },
    data: {
      is_recall: true,
      recall_time: new Date()
    },
    include: {
      sender: {
        select: {
          id: true,
          name: true,
          avatar: true
        }
      },
      read_by: {
        include: {
          user: {
            select: {
              id: true,
              name: true,
              avatar: true
            }
          }
        }
      }
    }
  })

  const latestMessage = await prisma.chat_message.findFirst({
    where: { chatroom_name: targetMessage.chatroom_name },
    orderBy: { id: 'desc' }
  })

  if (latestMessage?.id === targetMessage.id) {
    await prisma.chat_room.update({
      where: { id: targetMessage.chat_room_id },
      data: {
        last_message_content: getRecallPreview(targetMessage.sender.name)
      }
    })
  }

  return {
    id: recalledMessage.id,
    chatroomName: recalledMessage.chatroom_name,
    sender: recalledMessage.sender,
    readBy: recalledMessage.read_by.map((reader) => reader.user),
    receiverUid: recalledMessage.receiver_id,
    content: recalledMessage.content,
    isRecall: recalledMessage.is_recall,
    created: recalledMessage.created,
    recallTime: recalledMessage.recall_time,
    editTime: recalledMessage.edit_time
  }
}
