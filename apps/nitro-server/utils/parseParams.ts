import type { H3Event } from 'h3'

export const kunParseRouteId = (event: H3Event) => {
  const id = Number(event.context.params?.id)
  if (typeof id === 'number') {
    return id
  } else {
    return null
  }
}
