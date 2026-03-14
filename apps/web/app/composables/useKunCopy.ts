export const useKunCopy = () => {
  const message = useMessage()

  const copyToClipboard = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text)
      message.success('Copied to clipboard')
      return true
    } catch {
      message.error('Failed to copy')
      return false
    }
  }

  return { copyToClipboard }
}
