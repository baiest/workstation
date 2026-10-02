import { describe, expect, it } from 'vitest'
import { renderPlan } from './markdown'

describe('renderPlan', () => {
  it('renders headings, lists, code and tables', () => {
    const html = renderPlan('# Title\n\n- one\n- two\n\n`code`\n\n| a | b |\n|---|---|\n| 1 | 2 |\n')
    expect(html).toContain('<h1')
    expect(html).toContain('<li>one</li>')
    expect(html).toContain('<code>code</code>')
    expect(html).toContain('<table>')
  })

  it('escapes raw HTML instead of rendering it', () => {
    const html = renderPlan('before <script>alert(1)</script> after\n\n<img src=x onerror=alert(1)>')
    expect(html).not.toContain('<script')
    expect(html).not.toContain('<img')
    expect(html).toContain('&lt;script&gt;')
  })

  it('drops unsafe link schemes but keeps the text', () => {
    const html = renderPlan('[click](javascript:alert(1)) and [ok](https://example.com/x)')
    expect(html).not.toContain('javascript:')
    expect(html).toContain('click')
    expect(html).toContain('href="https://example.com/x"')
    expect(html).toContain('rel="noopener noreferrer"')
    expect(html).toContain('target="_blank"')
  })

  it('never loads remote images', () => {
    const html = renderPlan('![diagram](http://evil.example/track.png)')
    expect(html).not.toContain('<img')
    expect(html).toContain('diagram')
  })

  it('escapes html inside code blocks', () => {
    expect(renderPlan('```\n<b>x</b>\n```')).toContain('&lt;b&gt;')
  })
})
