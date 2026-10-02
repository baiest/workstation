import { Marked } from 'marked'

const escapeHtml = (s: string) =>
  s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')

// Plans are written by Claude and may embed text pasted from the web, so the
// output must be safe to inject: raw HTML is escaped, only http(s)/mailto links
// are kept, and images are never loaded.
const md = new Marked({
  gfm: true,
  renderer: {
    html: ({ text }) => escapeHtml(text),
    image: ({ text }) => escapeHtml(text),
    link({ href, tokens }) {
      const text = this.parser.parseInline(tokens)
      if (!/^(https?:|mailto:)/i.test(href)) return text
      return `<a href="${escapeHtml(href)}" target="_blank" rel="noopener noreferrer">${text}</a>`
    },
  },
})

export function renderPlan(markdown: string): string {
  return md.parse(markdown, { async: false })
}
