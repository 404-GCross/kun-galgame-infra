export type MessageType = 'success' | 'error' | 'warning' | 'info'

export interface Message {
  id: string
  type: MessageType
  content: string
  duration?: number
}

const messages = ref<Message[]>([])

export const useMessage = () => {
  const addMessage = (type: MessageType, content: string, duration = 3000) => {
    const id = Math.random().toString(36).substring(2, 9)
    const message: Message = { id, type, content, duration }

    messages.value.push(message)

    if (duration > 0) {
      setTimeout(() => {
        removeMessage(id)
      }, duration)
    }

    return id
  }

  const removeMessage = (id: string) => {
    const index = messages.value.findIndex(m => m.id === id)
    if (index > -1) {
      messages.value.splice(index, 1)
    }
  }

  const success = (content: string, duration?: number) => addMessage('success', content, duration)
  const error = (content: string, duration?: number) => addMessage('error', content, duration)
  const warning = (content: string, duration?: number) => addMessage('warning', content, duration)
  const info = (content: string, duration?: number) => addMessage('info', content, duration)

  return {
    messages,
    success,
    error,
    warning,
    info,
    remove: removeMessage,
  }
}
