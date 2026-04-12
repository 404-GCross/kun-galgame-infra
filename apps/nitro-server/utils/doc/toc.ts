export interface GenerateMarkdownTocOptions {
  maxDepth?: number
  ignoreH1?: boolean
}

const headingRegex = /^(#{1,6})\s(.{1,200}?)\n\n/gm

const createSlug = (text: string) =>
  text
    .trim()
    .toLowerCase()
    .replace(/[^\w\s-]/g, '')
    .replace(/\s+/g, '-')
    .replace(/-+/g, '-')
    .slice(0, 100)

const groupHeadings = (links: DocTocLink[], depth = 2) => {
  if (!links.length) {
    return links
  }

  const stack: DocTocLink[] = []
  const result: DocTocLink[] = []

  links.forEach((link) => {
    while (stack.length && link.depth <= stack[stack.length - 1]!.depth) {
      stack.pop()
    }

    if (!stack.length) {
      result.push(link)
    } else {
      if (!stack[stack.length - 1]!.children) {
        stack[stack.length - 1]!.children = []
      }
      stack[stack.length - 1]!.children!.push(link)
    }

    stack.push(link)
  })

  return result.filter((item) => item.depth <= depth)
}

export const generateMarkdownToc = (
  markdown: string,
  options: GenerateMarkdownTocOptions = {}
): DocTocLink[] => {
  const { maxDepth = 3, ignoreH1 = true } = options

  const links: DocTocLink[] = []
  let match: RegExpExecArray | null
  let index = 0

  while ((match = headingRegex.exec(markdown)) !== null) {
    const [fullMatch, hashes, title] = match
    const depth = hashes.length

    if (depth < 1 || depth > 6) {
      continue
    }

    if (ignoreH1 && depth === 1) {
      continue
    }

    if (depth > maxDepth) {
      continue
    }

    const level = depth === 1 && ignoreH1 ? 2 : depth
    const slug = createSlug(title)

    links.push({
      id: slug || `heading-${index}`,
      depth: level,
      text: title.trim()
    })

    index++
  }

  return groupHeadings(links, maxDepth)
}
