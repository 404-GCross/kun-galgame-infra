import { sendingMessage } from './service/message-sending'
import { recallMessage } from './service/message-recall'
import { ERROR_CODES } from './error'
import type { KUNGalgameSocket } from './socket'

const userSockets = new Map<number | undefined, KUNGalgameSocket>()

export const handleSocketRequest = (socket: KUNGalgameSocket) => {
  socket.on('private:join', (receiverUid: number) => {
    const uid = socket.payload?.uid
    if (!uid) {
      socket.emit(ERROR_CODES.MISSING_UID)
      return
    }

    userSockets.set(uid, socket)
    const roomId = generateRoomId(uid, receiverUid)
    socket.join(roomId)
  })

  socket.on('message:sending', async (receiverUid: number, content: string) => {
    const uid = socket.payload?.uid
    const sendingMessageUserSocket = userSockets.get(uid)

    if (!uid) {
      socket.emit(ERROR_CODES.MISSING_UID)
      return false
    }
    if (uid === receiverUid) {
      socket.emit(ERROR_CODES.CANNOT_SEND_MESSAGE_TO_YOURSELF)
      return false
    }
    if (!content.trim().length || content.length > 1007) {
      socket.emit(ERROR_CODES.CONTENT_TOO_LONG)
      return false
    }
    if (!sendingMessageUserSocket) {
      socket.emit(ERROR_CODES.INVALID_SOCKET)
      return false
    }

    const message = await sendingMessage(uid, receiverUid, content)

    const roomId = generateRoomId(uid, receiverUid)
    sendingMessageUserSocket.emit('message:sent', message)
    sendingMessageUserSocket.to(roomId).emit('message:received', message)
  })

  socket.on('message:recall', async (messageId: number) => {
    const uid = socket.payload?.uid
    if (!uid) {
      socket.emit(ERROR_CODES.MISSING_UID)
      return
    }

    const parsedMessageId = Number(messageId)
    if (!Number.isInteger(parsedMessageId)) {
      socket.emit('message:recall:error', '撤回消息失败，请刷新后重试')
      return
    }

    try {
      const recalledMessage = await recallMessage(uid, parsedMessageId)
      socket.emit('message:recalled', recalledMessage)
      socket
        .to(recalledMessage.chatroomName)
        .emit('message:recalled', recalledMessage)
    } catch (error) {
      const errorMessage =
        error instanceof Error ? error.message : '撤回消息失败，请稍后重试'
      socket.emit('message:recall:error', errorMessage)
    }
  })

  socket.on('private:leave', async () => {
    const uid = socket.payload?.uid

    if (uid) {
      userSockets.delete(uid)
    }
  })
}
