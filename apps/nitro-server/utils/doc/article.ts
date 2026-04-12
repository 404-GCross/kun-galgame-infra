import { markdownToHtml } from '../remark/markdownToHtml'
import { generateMarkdownToc } from './toc'
import type { Prisma } from '~~/prisma/generated/prisma/client'

export const docArticleListSelect = {
  id: true,
  title: true,
  slug: true,
  path: true,
  description: true,
  banner: true,
  status: true,
  is_pin: true,
  view: true,
  published_time: true,
  edited_time: true,
  created: true,
  updated: true,
  category: {
    select: {
      id: true,
      slug: true,
      title: true
    }
  },
  author: {
    select: {
      id: true,
      name: true,
      avatar: true
    }
  },
  tags: {
    select: {
      doc_tag: {
        select: {
          id: true,
          slug: true,
          title: true,
          description: true,
          created: true,
          updated: true
        }
      }
    }
  }
} satisfies Prisma.doc_articleSelect

export type DocArticleListPayload = Prisma.doc_articleGetPayload<{
  select: typeof docArticleListSelect
}>

export const docArticleDetailSelect = {
  ...docArticleListSelect,
  content_markdown: true
} satisfies Prisma.doc_articleSelect

export type DocArticleDetailPayload = Prisma.doc_articleGetPayload<{
  select: typeof docArticleDetailSelect
}>

export const mapDocArticleListItem = (
  article: DocArticleListPayload | DocArticleDetailPayload
): DocArticleSummary => ({
  id: article.id,
  title: article.title,
  slug: article.slug,
  path: article.path,
  description: article.description,
  banner: article.banner,
  status: article.status,
  isPin: article.is_pin,
  view: article.view,
  publishedTime: article.published_time,
  editedTime: article.edited_time,
  created: article.created,
  updated: article.updated,
  category: article.category,
  author: article.author,
  tags: article.tags.map((tag) => tag.doc_tag)
})

export const mapDocArticleDetail = async (
  article: DocArticleDetailPayload
): Promise<DocArticleDetail> => ({
  ...mapDocArticleListItem(article),
  contentMarkdown: article.content_markdown,
  contentHtml: await markdownToHtml(article.content_markdown),
  toc: generateMarkdownToc(article.content_markdown)
})
