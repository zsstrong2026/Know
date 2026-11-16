import assert from 'node:assert/strict'
import { test } from 'node:test'

import JSZip from 'jszip'
import { DOMParser } from '@xmldom/xmldom'

import {
  buildDocxBytes,
  buildDocxDocumentXml,
  buildDocumentExport,
  buildHtmlDocument,
  buildMarkdownDocument,
  collectDocxImages,
  parseDocBlocks,
  type DocxImageMap,
} from './export-doc'

const input = { title: '接口文档', markdown: '## 概述\n\n第一段。' }

/** 造一张 NxM 的 PNG 头：只要 IHDR 里的宽高正确，Word 不校验像素数据。 */
function pngBytes(width: number, height: number): Uint8Array<ArrayBuffer> {
  const bytes = new Uint8Array(new ArrayBuffer(33))
  bytes.set([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a], 0)
  const view = new DataView(bytes.buffer)
  view.setUint32(8, 13) // IHDR 数据长度
  bytes.set([0x49, 0x48, 0x44, 0x52], 12) // "IHDR"
  view.setUint32(16, width)
  view.setUint32(20, height)
  return bytes
}

/** 解析 XML 并返回错误列表：空数组表示结构合法。 */
function xmlErrors(xml: string): string[] {
  const errors: string[] = []
  new DOMParser({ onError: (_level, message) => errors.push(message) }).parseFromString(xml, 'text/xml')
  return errors
}

test('buildMarkdownDocument：补标题且不重复同名首行', () => {
  assert.equal(buildMarkdownDocument(input), '# 接口文档\n\n## 概述\n\n第一段。\n')
  assert.equal(
    buildMarkdownDocument({ title: '接口文档', markdown: '# 接口文档\n\n正文' }),
    '# 接口文档\n\n正文\n',
  )
  assert.equal(buildMarkdownDocument({ title: '空文档', markdown: '   ' }), '# 空文档\n')
})

test('buildHtmlDocument：单文件、带样式，原始 HTML 与危险链接被中和', () => {
  const html = buildHtmlDocument({
    title: '标题<script>',
    markdown: '<img src=x onerror=alert(1)>\n\n[点我](javascript:alert(1))\n\n[官网](https://example.com)',
  })
  assert.ok(html.startsWith('<!DOCTYPE html>'))
  assert.ok(html.includes('<style>'))
  // 标题里的尖括号被转义，不会提前闭合 <title>
  assert.ok(html.includes('<title>标题&lt;script&gt;</title>'))
  // 原始 HTML 作为文本输出
  assert.ok(html.includes('&lt;img src=x onerror=alert(1)&gt;'))
  // javascript: 协议被降级成纯文本，安全链接照常保留
  assert.ok(!html.includes('javascript:'))
  assert.ok(html.includes('href="https://example.com"'))
})

test('parseDocBlocks：识别标题、列表、引用、代码、表格与分割线', () => {
  const blocks = parseDocBlocks(
    [
      '# 一级',
      '',
      '- 甲',
      '- **乙**',
      '',
      '> 引用一',
      '> 引用二',
      '',
      '```ts',
      'const a = 1',
      '```',
      '',
      '| 事项 | 状态 |',
      '| --- | --- |',
      '| 联调 | 已完成 |',
      '',
      '---',
    ].join('\n'),
  )
  assert.deepEqual(blocks.map((block) => block.kind), [
    'heading',
    'list',
    'quote',
    'code',
    'table',
    'divider',
  ])
  const list = blocks[1]
  assert.ok(list.kind === 'list' && !list.ordered)
  // 行内强调标记在 Word 里没有意义，解析阶段就剥掉
  assert.deepEqual(list.kind === 'list' ? list.items : [], ['甲', '乙'])
  const table = blocks[4]
  assert.ok(table.kind === 'table')
  if (table.kind === 'table') {
    assert.deepEqual(table.header, ['事项', '状态'])
    assert.deepEqual(table.rows, [['联调', '已完成']])
  }
})

test('buildDocxDocumentXml：标题、表格与转义符合 OOXML', () => {
  const xml = buildDocxDocumentXml('报告 <A&B>', '- 甲\n\n| 列一 |\n| --- |\n| x & y |')
  assert.ok(xml.startsWith('<?xml version="1.0"'))
  assert.ok(xml.includes('<w:body>'))
  assert.ok(xml.includes('<w:sectPr>'))
  // 标题与单元格里的 & < 都被转义
  assert.ok(xml.includes('报告 &lt;A&amp;B&gt;'))
  assert.ok(xml.includes('x &amp; y'))
  assert.ok(xml.includes('<w:tbl>'))
  assert.ok(xml.includes('• 甲'))
})

test('buildDocxDocumentXml：产物是合法 XML（Word 能否打开的前提）', () => {
  const xml = buildDocxDocumentXml(
    '标题 & 副标题',
    '## 小节\n\n正文含 <尖括号> 与 "引号"\n\n| 空值 | 有值 |\n| --- | --- |\n| | x |',
  )
  const doc = new DOMParser().parseFromString(xml, 'text/xml')
  assert.deepEqual(xmlErrors(xml), [])
  assert.equal(doc.documentElement?.tagName, 'w:document')
  // 空单元格也要补一个段落，否则 Word 会报文件损坏
  assert.ok(xml.includes('<w:tc><w:tcPr><w:tcW w:w="0" w:type="auto"/></w:tcPr><w:p/>'))
})

test('buildDocumentExport：三种格式的文件名、MIME 与包结构', async () => {
  const md = await buildDocumentExport('markdown', input)
  assert.equal(md.fileName, '接口文档.md')
  assert.equal(await md.blob.text(), '# 接口文档\n\n## 概述\n\n第一段。\n')

  const html = await buildDocumentExport('html', input)
  assert.equal(html.fileName, '接口文档.html')
  assert.equal(html.blob.type, 'text/html;charset=utf-8')

  const docx = await buildDocumentExport('docx', input)
  assert.equal(docx.fileName, '接口文档.docx')
  assert.equal(
    docx.blob.type,
    'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
  )
  const zip = await JSZip.loadAsync(await buildDocxBytes(input))
  const files = Object.keys(zip.files).filter((path) => !zip.files[path].dir).sort()
  assert.deepEqual(files, ['[Content_Types].xml', '_rels/.rels', 'word/document.xml'])
})

test('buildDocumentExport：标题非法字符被安全化，空标题退回默认名', async () => {
  const unsafe = await buildDocumentExport('markdown', { title: 'a/b:c*d?', markdown: 'x' })
  assert.equal(unsafe.fileName, 'a-b-c-d-.md')
  const empty = await buildDocumentExport('html', { title: '', markdown: 'x' })
  assert.equal(empty.fileName, 'workbench.html')
})

test('parseDocBlocks / 文本格式：asset:// 图片降级为说明文字', () => {
  const markdown = '正文\n\n![架构图](asset://a1)\n\n![无说明](asset://a2)\n\n结尾'
  const images = parseDocBlocks(markdown).filter((block) => block.kind === 'image')
  assert.deepEqual(images, [
    { kind: 'image', alt: '架构图', assetId: 'a1' },
    { kind: 'image', alt: '无说明', assetId: 'a2' },
  ])
  // Markdown / HTML 没有资产通道：保留说明，去掉点不开的 asset:// 链接
  const md = buildMarkdownDocument({ title: '图', markdown })
  assert.ok(!md.includes('asset://'))
  assert.ok(md.includes('架构图'))
  assert.ok(md.includes('无说明'))
})

test('collectDocxImages：按块序编号、读真实像素尺寸，取不到或非位图则跳过', async () => {
  const blobs: Record<string, Blob | null> = {
    a1: new Blob([pngBytes(320, 240)], { type: 'image/png' }),
    a2: null,
    // 没有 type 的旧资产靠魔数识别
    a3: new Blob([pngBytes(800, 600)]),
    a4: new Blob(['<svg/>'], { type: 'image/svg+xml' }),
  }
  const { images, files } = await collectDocxImages(
    '![封面](asset://a1)\n\n![缺图](asset://a2)\n\n![截图](asset://a3)\n\n![图标](asset://a4)\n\n![封面复用](asset://a1)',
    async (assetId) => blobs[assetId] ?? null,
  )

  // 同一资产被引用两次只存一份媒体文件
  assert.deepEqual([...images.keys()], ['a1', 'a3'])
  assert.deepEqual(files.map((file) => file.path), ['word/media/image1.png', 'word/media/image2.png'])

  const first = images.get('a1')!
  assert.equal(first.relId, 'rIdImg1')
  // 320x240 像素 → 96 DPI 下的 EMU
  assert.equal(first.widthEmu, 320 * 9525)
  assert.equal(first.heightEmu, 240 * 9525)
})

test('collectDocxImages：超宽图片等比缩到正文宽度内', async () => {
  const { images } = await collectDocxImages('![宽图](asset://wide)', async () =>
    new Blob([pngBytes(4000, 1000)], { type: 'image/png' }),
  )
  const part = images.get('wide')!
  // 上限 5 731 510 EMU（≈6.27 英寸），高度按原比例
  assert.equal(part.widthEmu, 5_731_510)
  assert.ok(Math.abs(part.heightEmu - 5_731_510 / 4) <= 1)
})

test('buildDocxBytes：内嵌图片写出媒体文件、关系与 drawing，Word 结构完整', async () => {
  const bytes = await buildDocxBytes({
    title: '带图报告',
    markdown: '## 架构\n\n![架构图](asset://a1)\n\n![缺图](asset://missing)\n\n结尾段落',
    resolveImage: async (assetId) =>
      assetId === 'a1' ? new Blob([pngBytes(64, 48)], { type: 'image/png' }) : null,
  })
  const zip = await JSZip.loadAsync(bytes)
  const files = Object.keys(zip.files).filter((path) => !zip.files[path].dir).sort()
  assert.deepEqual(files, [
    '[Content_Types].xml',
    '_rels/.rels',
    'word/_rels/document.xml.rels',
    'word/document.xml',
    'word/media/image1.png',
  ])

  const rels = await zip.file('word/_rels/document.xml.rels')!.async('string')
  assert.ok(rels.includes('Id="rIdImg1"'))
  assert.ok(rels.includes('Target="media/image1.png"'))

  const contentTypes = await zip.file('[Content_Types].xml')!.async('string')
  // 图片扩展名必须在清单里登记，否则 Word 拒绝打开
  assert.ok(contentTypes.includes('Extension="png" ContentType="image/png"'))

  const document = await zip.file('word/document.xml')!.async('string')
  assert.ok(document.includes('<w:drawing'))
  assert.ok(document.includes('r:embed="rIdImg1"'))
  assert.ok(document.includes('cx="609600" cy="457200"')) // 64x48 像素
  // 取不到的图片降级为一行说明，不留空洞
  assert.ok(document.includes('[图片] 缺图'))
  assert.ok(document.includes('结尾段落'))

  // 带 drawing 的 document.xml 也必须是合法 XML
  assert.deepEqual(xmlErrors(document), [])
  assert.deepEqual(xmlErrors(rels), [])
  assert.deepEqual(xmlErrors(contentTypes), [])
})

test('buildDocxDocumentXml：无图片表时图片块退化成说明文字', () => {
  const images: DocxImageMap = new Map()
  const xml = buildDocxDocumentXml('t', '![示意图](asset://x)', images)
  assert.ok(!xml.includes('<w:drawing'))
  assert.ok(xml.includes('[图片] 示意图'))
})

test('buildDocumentExport：docx 传了 resolver 就会内嵌图片', async () => {
  const result = await buildDocumentExport('docx', {
    title: '图',
    markdown: '![图一](asset://a1)',
    resolveImage: async () => new Blob([pngBytes(10, 10)], { type: 'image/png' }),
  })
  const zip = await JSZip.loadAsync(await result.blob.arrayBuffer())
  assert.ok(zip.file('word/media/image1.png'))
})
