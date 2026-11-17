/**
 * 打印版报告：把 Markdown 渲染成干净 HTML，用隐藏 iframe 调起系统打印。
 *
 * 不复用页面 DOM：避免把按钮、侧栏与交互控件带进纸面，也避免依赖 TDesign 样式。
 */
import { markdownToBlockSpecs, type MarkdownBlockSpec } from './markdown-blocks'

const PRINT_STYLE = `
@page { size: A4; margin: 18mm 16mm; }
* { box-sizing: border-box; }
body { margin: 0; color: #1f2329; font: 13px/1.75 -apple-system, "PingFang SC", "Microsoft YaHei", sans-serif; }
article { max-width: 100%; }
h1 { font-size: 22px; margin: 0 0 16px; }
h2 { font-size: 17px; margin: 22px 0 8px; }
h3 { font-size: 15px; margin: 18px 0 6px; }
h4 { font-size: 13.5px; margin: 14px 0 4px; }
h1, h2, h3 { break-after: avoid; }
p { margin: 6px 0; }
ul, ol { margin: 6px 0; padding-left: 22px; }
li { margin: 2px 0; }
blockquote { margin: 8px 0; padding: 6px 12px; border-left: 3px solid #d0d3d9; color: #4b5563; background: #fafafa; }
hr { border: 0; border-top: 1px solid #e5e6eb; margin: 16px 0; }
pre { background: #f7f8fa; padding: 10px 12px; border-radius: 4px; overflow: auto; white-space: pre-wrap; word-break: break-word; }
code { font-family: "SFMono-Regular", Consolas, monospace; font-size: 12px; }
`

/** 把 Markdown 渲染成打印用 HTML；连续列表项合并成一个 ul/ol。 */
export function markdownToPrintHtml(markdown: string): string {
  const specs = markdownToBlockSpecs(markdown)
  const html: string[] = []
  let i = 0
  while (i < specs.length) {
    const spec = specs[i]
    if (spec.flavour === 'affine:list') {
      const tag = spec.props?.type === 'numbered' ? 'ol' : 'ul'
      const items: string[] = []
      while (i < specs.length && specs[i].flavour === 'affine:list' && sameListKind(specs[i], tag)) {
        items.push(`<li>${escapeHtml(specs[i].text ?? '')}</li>`)
        i += 1
      }
      html.push(`<${tag}>${items.join('')}</${tag}>`)
      continue
    }
    html.push(renderBlock(spec))
    i += 1
  }
  return html.join('\n')
}

function sameListKind(spec: MarkdownBlockSpec, tag: string): boolean {
  return tag === 'ol' ? spec.props?.type === 'numbered' : spec.props?.type !== 'numbered'
}

function renderBlock(spec: MarkdownBlockSpec): string {
  const text = escapeHtml(spec.text ?? '')
  if (spec.flavour === 'affine:divider') return '<hr/>'
  if (spec.flavour === 'affine:code') return `<pre><code>${text}</code></pre>`
  const type = String(spec.props?.type ?? 'text')
  if (/^h[1-4]$/.test(type)) return `<${type}>${text}</${type}>`
  if (type === 'quote') return `<blockquote>${text}</blockquote>`
  return `<p>${text}</p>`
}

function escapeHtml(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

/**
 * 打开系统打印对话框（用户可选「另存为 PDF」）。
 *
 * 用隐藏 iframe 而不是新窗口：不会被弹窗拦截，也不会带上当前页面的导航。
 */
export function printMarkdown(title: string, markdown: string): void {
  const iframe = document.createElement('iframe')
  iframe.setAttribute('aria-hidden', 'true')
  iframe.style.cssText = 'position:fixed;right:0;bottom:0;width:0;height:0;border:0;'
  document.body.appendChild(iframe)

  const doc = iframe.contentDocument
  const win = iframe.contentWindow
  if (!doc || !win) {
    iframe.remove()
    return
  }

  doc.open()
  doc.write(
    `<!doctype html><html><head><meta charset="utf-8"><title>${escapeHtml(title)}</title>` +
      `<style>${PRINT_STYLE}</style></head><body><article>${markdownToPrintHtml(markdown)}</article></body></html>`,
  )
  doc.close()

  let removed = false
  const cleanup = () => {
    if (removed) return
    removed = true
    iframe.remove()
  }
  win.addEventListener('afterprint', cleanup)
  // 等 iframe 完成首帧排版再打印；afterprint 不触发时用定时器兜底。
  setTimeout(() => {
    win.focus()
    win.print()
    setTimeout(cleanup, 1500)
  }, 80)
}
