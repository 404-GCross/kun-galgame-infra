import { getRequestURL, sendRedirect } from 'h3'

export default defineEventHandler((event) => {
  const url = getRequestURL(event)
  const path = url.pathname

  // deprecated /doc/xxx/xxx-xxx to /doc/xxx-xxx
  const match = path.match(/^\/doc\/(.+\/)?([^/]+)$/)
  if (match) {
    const last = match[2]
    if (path !== `/doc/${last}`) {
      return sendRedirect(event, `/doc/${last}`, 301)
    }
  }
})
