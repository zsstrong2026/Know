import assert from 'node:assert/strict'
import { test } from 'node:test'

import { markdownToPrintHtml } from './insight-print.ts'

test('连续列表项合并为一个列表，编号列表用 ol', () => {
  const html = markdownToPrintHtml('- 甲\n- 乙\n1. 丙\n2. 丁')
  assert.equal(html, '<ul><li>甲</li><li>乙</li></ul>\n<ol><li>丙</li><li>丁</li></ol>')
})

test('标题、引用与代码块按语义渲染', () => {
  const html = markdownToPrintHtml('## 结论\n> 引用\n```go\nfmt.Println(1)\n```')
  assert.match(html, /<h2>结论<\/h2>/)
  assert.match(html, /<blockquote>引用<\/blockquote>/)
  assert.match(html, /<pre><code>fmt\.Println\(1\)<\/code><\/pre>/)
})

test('正文中的 HTML 被转义，避免打印页执行脚本', () => {
  const html = markdownToPrintHtml('<script>alert(1)</script>')
  assert.equal(html, '<p>&lt;script&gt;alert(1)&lt;/script&gt;</p>')
})
