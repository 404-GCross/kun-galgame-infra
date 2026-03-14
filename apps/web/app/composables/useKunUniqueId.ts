let counter = 0

export const useKunUniqueId = (prefix = 'kun') => {
  const id = useState(`${prefix}-id-${++counter}`, () => `${prefix}-${counter}`)
  return id.value
}
