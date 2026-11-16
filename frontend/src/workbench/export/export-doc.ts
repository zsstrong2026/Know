/**
 * 文档导出（Markdown / HTML / Word）。
 *
 * 与 `package.ts`（画布导出包）的分工：这里只导出「可读正文」，内容来自
 * `projection.ts` 投影出的 Markdown，不带 Y.Doc 快照与资产；三种格式全部在
 * 浏览器本地生成，不增加服务端接口。
 *
 * Word 采用手写最小 OOXML + jszip 打包，而不是引入 docx 依赖 ——
 * 与 `internal/utils/pmdocs` 的思路一致，导出保真度以「能打开、结构正确」为准。
 *
 * 纯逻辑与 DOM 无关，可直接跑 `tsx --test`。
 */
import JSZip from 'jszip'
import { marked } from 'marked'

import {
  extensionOfMime,
  mimeOfExtension,
  normalizeImageMime,
  readImageSize,
  sniffImageMime,
  toEmuSize,
  type EmuSize,
} from './image-size'
import { sanitizeFileName } from './package'

export type DocExportFormat = 'markdown' | 'html' | 'docx'

export interface DocExportInput {
  title: string
  /** 投影后的正文 Markdown（不含标题） */
  markdown: string
  /**
   * Word 导出时按 asset id 取图片字节；不传则图片位置只保留说明文字。
   * 其它两种格式用不到。
   */
  resolveImage?: (assetId: string) => Promise<Blob | null>
}

export interface DocExportResult {
  blob: Blob
  fileName: string
}

const FORMAT_META: Record<DocExportFormat, { extension: string; mime: string }> = {
  markdown: { extension: '.md', mime: 'text/markdown;charset=utf-8' },
  html: { extension: '.html', mime: 'text/html;charset=utf-8' },
  docx: {
    extension: '.docx',
    mime: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document',
  },
}

function escapeHtml(text: string): string {
  return String(text ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
}

/** 正文首行已经是同名标题时不再重复插入，避免导出稿出现两个大标题。 */
function withTitle(title: string, markdown: string): string {
  const body = String(markdown ?? '').trim()
  const name = title.trim()
  if (!name) return body
  if (body.split('\n', 1)[0]?.trim() === `# ${name}`) return body
  return body ? `# ${name}\n\n${body}` : `# ${name}`
}

/**
 * 把图片占位降级成说明文字。
 *
 * `asset://` 只在应用内可解析，Markdown / HTML 两个单文件格式没有资产通道，
 * 留着会变成点不开的坏图，因此只保留说明（没有说明就整行去掉）。
 * 行内图片（文字中夹一张图）同样处理，避免正文里出现破图。
 */
function dropImagePlaceholders(markdown: string): string {
  return String(markdown ?? '')
    .split('\n')
    .flatMap((line) => {
      const replaced = line.replace(ASSET_IMAGE_INLINE_RE, (_match, alt: string) => alt.trim())
      // 整行只有一张没有说明的图：连空行一起省掉
      if (line.trim() && !replaced.trim()) return []
      return [replaced]
    })
    .join('\n')
}

/** Markdown 导出：标题 + 正文，保留全部 Markdown 语法（图片降级为说明）。 */
export function buildMarkdownDocument(input: DocExportInput): string {
  const text = withTitle(input.title, dropImagePlaceholders(input.markdown))
  return text ? `${text}\n` : ''
}

/** 只放行白名单协议，避免导出稿里的 `javascript:` 链接被误点。 */
const SAFE_HREF = /^(https?:|mailto:|tel:|#|\/|\.{1,2}\/)/i

/**
 * HTML 渲染器：原始 HTML 一律转义成文本，链接只保留安全协议。
 * 导出文件会脱离应用的安全策略独立打开，因此不能依赖 DOMPurify（它需要 DOM）。
 */
function createExportRenderer() {
  const renderer = new marked.Renderer()
  renderer.html = ({ text }: { text: string }) => escapeHtml(text)
  renderer.link = ({ href, title, tokens }) => {
    const label = renderer.parser.parseInline(tokens)
    if (!SAFE_HREF.test(href)) return label
    const titleAttr = title ? ` title="${escapeHtml(title)}"` : ''
    return `<a href="${escapeHtml(href)}"${titleAttr}>${label}</a>`
  }
  return renderer
}

const HTML_STYLE = `
  * { box-sizing: border-box; }
  body {
    margin: 0; padding: 48px 24px; background: #fff; color: #1f2329;
    font: 16px/1.75 -apple-system, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif;
  }
  main { max-width: 760px; margin: 0 auto; }
  h1, h2, h3, h4, h5, h6 { line-height: 1.35; margin: 1.4em 0 0.6em; }
  h1 { font-size: 28px; padding-bottom: 0.3em; border-bottom: 1px solid #e5e6eb; }
  h2 { font-size: 22px; }
  h3 { font-size: 18px; }
  p, ul, ol, blockquote, pre, table { margin: 0 0 1em; }
  blockquote {
    padding: 0.2em 1em; margin-left: 0; color: #646a73;
    border-left: 3px solid #e5e6eb; background: #f7f8fa;
  }
  code {
    padding: 0.1em 0.35em; border-radius: 3px; background: #f2f3f5;
    font-family: "SFMono-Regular", Consolas, "Liberation Mono", monospace; font-size: 0.9em;
  }
  pre { padding: 12px 16px; overflow-x: auto; border-radius: 6px; background: #f7f8fa; }
  pre code { padding: 0; background: none; }
  table { width: 100%; border-collapse: collapse; font-size: 0.95em; }
  th, td { padding: 8px 12px; border: 1px solid #dee0e3; text-align: left; }
  th { background: #f7f8fa; }
  hr { border: none; border-top: 1px solid #e5e6eb; margin: 2em 0; }
  a { color: #3370ff; }
  @media print {
    body { padding: 0; }
    main { max-width: none; }
  }
`

/** HTML 导出：单文件、内联样式，双击即可在浏览器里阅读或打印。 */
export function buildHtmlDocument(input: DocExportInput): string {
  const markdown = withTitle(input.title, dropImagePlaceholders(input.markdown))
  const body = marked.parse(markdown, {
    gfm: true,
    async: false,
    renderer: createExportRenderer(),
  }) as string
  return [
    '<!DOCTYPE html>',
    '<html>',
    '<head>',
    '<meta charset="utf-8">',
    '<meta name="viewport" content="width=device-width, initial-scale=1">',
    `<title>${escapeHtml(input.title.trim() || 'untitled')}</title>`,
    `<style>${HTML_STYLE}</style>`,
    '</head>',
    `<body><main>${body}</main></body>`,
    '</html>',
    '',
  ].join('\n')
}

// ---- Word（docx）----

const DOCX_ROOT_RELS = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`

/**
 * 内容类型清单。
 *
 * 图片必须按扩展名片登记 `<Default>`，否则 Word 认为包里存在未知部件而拒绝打开。
 * 没有图片时保持原来的最小清单，避免多出无用的默认项。
 */
function buildContentTypes(extensions: string[]): string {
  const defaults = [
    '<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>',
    '<Default Extension="xml" ContentType="application/xml"/>',
    ...extensions.map(
      (extension) => `<Default Extension="${extension}" ContentType="${mimeOfExtension(extension)}"/>`,
    ),
  ].join('')
  return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">${defaults}<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`
}

/** 主文档关系：每个媒体文件一条 image 关系，`r:embed` 引用这些 id。 */
function buildDocumentRels(parts: DocxImagePart[]): string {
  const rels = parts
    .map(
      (part) =>
        `<Relationship Id="${part.relId}" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="${part.path}"/>`,
    )
    .join('')
  return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">${rels}</Relationships>`
}

/** 包内的一张图片：关系 id + 媒体路径 + 版面尺寸（EMU）。 */
export interface DocxImagePart {
  relId: string
  /** 相对 `word/` 的路径，如 `media/image1.png` */
  path: string
  name: string
  widthEmu: number
  heightEmu: number
}

/**
 * 内嵌图片的 drawing XML。
 *
 * Word 的图片必须放在 `w:r/w:drawing/wp:inline` 里，并写出 `wp:extent`
 * 的具体宽高：只给一个 `<w:r>` 或省略 extent，打开时图片会显示成红叉。
 * 这里把 wp/a/pic/r 四个命名空间就地声明，省得改 document 根节点。
 */
function imageDrawingXml(part: DocxImagePart, index: number, alt: string): string {
  const descr = alt ? ` descr="${escapeXml(alt)}"` : ''
  const ns =
    'xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" ' +
    'xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" ' +
    'xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture" ' +
    'xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"'
  return (
    `<w:p><w:pPr><w:spacing w:before="0" w:after="120"/><w:jc w:val="center"/></w:pPr><w:r><w:drawing ${ns}>` +
    `<wp:inline distT="0" distB="0" distL="0" distR="0">` +
    `<wp:extent cx="${part.widthEmu}" cy="${part.heightEmu}"/>` +
    '<wp:effectExtent l="0" t="0" r="0" b="0"/>' +
    `<wp:docPr id="${index}" name="Picture ${index}"${descr}/>` +
    '<wp:cNvGraphicFramePr><a:graphicFrameLocks noChangeAspect="1"/></wp:cNvGraphicFramePr>' +
    '<a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture">' +
    '<pic:pic>' +
    `<pic:nvPicPr><pic:cNvPr id="${index}" name="${escapeXml(part.name)}"${descr}/><pic:cNvPicPr/></pic:nvPicPr>` +
    `<pic:blipFill><a:blip r:embed="${part.relId}"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill>` +
    `<pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="${part.widthEmu}" cy="${part.heightEmu}"/></a:xfrm>` +
    '<a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr>' +
    '</pic:pic></a:graphicData></a:graphic>' +
    '</wp:inline></w:drawing></w:r></w:p>'
  )
}

function escapeXml(text: string): string {
  return String(text ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&apos;')
}

interface ParagraphOptions {
  /** 字号，单位半磅（21 = 10.5pt） */
  size?: number
  bold?: boolean
  italics?: boolean
  /** 左缩进，单位 twips */
  indent?: number
  mono?: boolean
  /** 底纹填充色 */
  shading?: string
}

function paragraphXml(text: string, options: ParagraphOptions = {}): string {
  const body = String(text ?? '').trim()
  if (!body) return ''
  const size = options.size ?? 21
  const fonts = options.mono
    ? '<w:rFonts w:ascii="Consolas" w:hAnsi="Consolas" w:eastAsia="Microsoft YaHei"/>'
    : '<w:rFonts w:ascii="Arial" w:hAnsi="Arial" w:eastAsia="Microsoft YaHei"/>'
  const rPr = [
    fonts,
    options.bold ? '<w:b/>' : '',
    options.italics ? '<w:i/>' : '',
    `<w:sz w:val="${size}"/><w:szCs w:val="${size}"/>`,
  ].join('')
  const pPr = [
    '<w:spacing w:before="0" w:after="120"/>',
    options.indent ? `<w:ind w:left="${options.indent}"/>` : '',
    options.shading ? `<w:shd w:val="clear" w:color="auto" w:fill="${options.shading}"/>` : '',
  ].join('')
  return `<w:p><w:pPr>${pPr}</w:pPr><w:r><w:rPr>${rPr}</w:rPr><w:t xml:space="preserve">${escapeXml(body)}</w:t></w:r></w:p>`
}

function dividerXml(): string {
  return '<w:p><w:pPr><w:pBdr><w:bottom w:val="single" w:sz="6" w:space="1" w:color="D9D9D9"/></w:pBdr></w:pPr></w:p>'
}

/** 表格 XML：首行加粗，四周与内框都用细线，宽度铺满正文区。 */
function tableXml(header: string[], rows: string[][]): string {
  const border = ['top', 'left', 'bottom', 'right', 'insideH', 'insideV']
    .map((side) => `<w:${side} w:val="single" w:sz="4" w:space="0" w:color="DEE0E3"/>`)
    .join('')
  const cell = (text: string, bold = false) => {
    // Word 要求每个单元格至少含一个段落，空值也要留一个空段落
    const body = paragraphXml(text, { bold }) || '<w:p/>'
    return `<w:tc><w:tcPr><w:tcW w:w="0" w:type="auto"/></w:tcPr>${body}</w:tc>`
  }
  const lines = [
    `<w:tr>${header.map((text) => cell(text, true)).join('')}</w:tr>`,
    ...rows.map((row) => `<w:tr>${row.map((text) => cell(text)).join('')}</w:tr>`),
  ]
  return `<w:tbl><w:tblPr><w:tblW w:w="5000" w:type="pct"/><w:tblBorders>${border}</w:tblBorders></w:tblPr>${lines.join(
    '',
  )}</w:tbl>`
}

type DocBlock =
  | { kind: 'heading'; level: number; text: string }
  | { kind: 'paragraph'; text: string }
  | { kind: 'list'; ordered: boolean; items: string[] }
  | { kind: 'quote'; text: string }
  | { kind: 'code'; lines: string[] }
  | { kind: 'table'; header: string[]; rows: string[][] }
  | { kind: 'divider' }
  | { kind: 'image'; assetId: string; alt: string }

const HEADING_RE = /^(#{1,6})\s+(.*)$/
const QUOTE_RE = /^\s{0,3}>\s?(.*)$/
const UL_RE = /^\s*[-*+]\s+(.*)$/
const OL_RE = /^\s*\d+[.)]\s+(.*)$/
const HR_RE = /^(-{3,}|\*{3,}|_{3,})$/
/** 图片占位（与投影层共用 `asset://`）：`![说明](asset://<assetId>)`。 */
const ASSET_IMAGE_INLINE_RE = /!\[([^\]]*)\]\(asset:\/\/([^)\s]+)\)/g
/** 独占一行的图片：说明可空，用于在正文里定位图片块。 */
const IMAGE_RE = /^!\[([^\]]*)\]\(asset:\/\/([^)\s]+)\)$/

/** 去掉行内标记：Word 只保留纯文本，避免把 `**` 之类原样打出来。 */
function stripInline(text: string): string {
  return String(text ?? '')
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/(\*\*|__)(.*?)\1/g, '$2')
    .replace(/(\*|_)(.*?)\1/g, '$2')
    .replace(/`([^`]*)`/g, '$1')
    .trim()
}

function splitTableRow(line: string): string[] {
  return line
    .trim()
    .replace(/^\|/, '')
    .replace(/\|$/, '')
    .split('|')
    .map((cell) => stripInline(cell))
}

/** 表格分隔行：只由 `|`、`-`、`:` 与空白组成，且至少含一个 `-`。 */
function isTableSeparator(line: string | undefined): boolean {
  const text = String(line ?? '').trim()
  if (!text.includes('-')) return false
  return /^\|?[\s:|-]+\|?$/.test(text)
}

/**
 * Markdown → 块序列（只覆盖投影与 AI 常见产出的语法）。
 *
 * 与 AI 写入链路里的 `markdownToBlocks` 相比，这里额外识别表格与分割线，
 * 因为数据库正文在导出里就是 Markdown 表格。
 */
export function parseDocBlocks(markdown: string): DocBlock[] {
  const lines = String(markdown ?? '').replace(/\r\n/g, '\n').split('\n')
  const blocks: DocBlock[] = []
  let i = 0

  while (i < lines.length) {
    const line = lines[i]
    if (!line.trim()) {
      i += 1
      continue
    }

    if (/^```/.test(line.trim())) {
      const body: string[] = []
      i += 1
      while (i < lines.length && !/^```/.test(lines[i].trim())) {
        body.push(lines[i])
        i += 1
      }
      i += 1 // 跳过收尾围栏；缺失时也前进，避免死循环
      blocks.push({ kind: 'code', lines: body })
      continue
    }

    if (HR_RE.test(line.trim())) {
      blocks.push({ kind: 'divider' })
      i += 1
      continue
    }

    const heading = HEADING_RE.exec(line)
    if (heading) {
      blocks.push({ kind: 'heading', level: heading[1].length, text: stripInline(heading[2]) })
      i += 1
      continue
    }

    // 图片块先于段落判断：它本身也以 `!` 开头，混进段落就会被压成纯文本
    const image = IMAGE_RE.exec(line.trim())
    if (image) {
      blocks.push({ kind: 'image', alt: stripInline(image[1]), assetId: image[2] })
      i += 1
      continue
    }

    if (line.trim().startsWith('|') && isTableSeparator(lines[i + 1])) {
      const header = splitTableRow(line)
      const rows: string[][] = []
      i += 2
      while (i < lines.length && lines[i].trim().startsWith('|')) {
        rows.push(splitTableRow(lines[i]))
        i += 1
      }
      blocks.push({ kind: 'table', header, rows })
      continue
    }

    if (QUOTE_RE.test(line)) {
      const parts: string[] = []
      while (i < lines.length) {
        const match = QUOTE_RE.exec(lines[i])
        if (!match) break
        if (match[1].trim()) parts.push(stripInline(match[1]))
        i += 1
      }
      if (parts.length > 0) blocks.push({ kind: 'quote', text: parts.join(' ') })
      continue
    }

    if (UL_RE.test(line) || OL_RE.test(line)) {
      const ordered = OL_RE.test(line)
      const items: string[] = []
      while (i < lines.length) {
        const match = (ordered ? OL_RE : UL_RE).exec(lines[i])
        if (!match) break
        items.push(stripInline(match[1]))
        i += 1
      }
      blocks.push({ kind: 'list', ordered, items })
      continue
    }

    const paragraph: string[] = []
    while (i < lines.length) {
      const current = lines[i]
      if (!current.trim()) break
      if (HEADING_RE.test(current) || QUOTE_RE.test(current) || IMAGE_RE.test(current.trim())) break
      if (UL_RE.test(current) || OL_RE.test(current) || HR_RE.test(current.trim())) break
      if (/^```/.test(current.trim())) break
      paragraph.push(stripInline(current))
      i += 1
    }
    if (paragraph.length > 0) blocks.push({ kind: 'paragraph', text: paragraph.join(' ') })
  }

  return blocks
}

const HEADING_SIZE: Record<number, number> = { 1: 36, 2: 32, 3: 28, 4: 24, 5: 24, 6: 24 }

/** 图片按 asset id 查到的包内部件；缺失（取不到字节）时不出现在表里。 */
export type DocxImageMap = Map<string, DocxImagePart>

/**
 * 生成 `word/document.xml`；拆出来便于单测直接断言 XML 内容。
 *
 * `images` 为空时图片块降级为说明文字，保证正文不出现空洞。
 */
export function buildDocxDocumentXml(
  title: string,
  markdown: string,
  images: DocxImageMap = new Map(),
): string {
  const parts: string[] = []
  const name = title.trim()
  if (name) parts.push(paragraphXml(name, { size: 36, bold: true }))
  // wp:docPr 的 id 在整篇里必须唯一，不能拿块序号凑
  let pictureCount = 0

  for (const block of parseDocBlocks(markdown)) {
    switch (block.kind) {
      case 'heading':
        parts.push(
          paragraphXml(block.text, { size: HEADING_SIZE[block.level] ?? 24, bold: true }),
        )
        break
      case 'paragraph':
        parts.push(paragraphXml(block.text))
        break
      case 'quote':
        parts.push(paragraphXml(block.text, { italics: true, indent: 360 }))
        break
      case 'list':
        block.items.forEach((item, index) => {
          const bullet = block.ordered ? `${index + 1}. ` : '• '
          parts.push(paragraphXml(`${bullet}${item}`, { indent: 360 }))
        })
        break
      case 'code':
        for (const codeLine of block.lines) {
          parts.push(paragraphXml(codeLine || ' ', { mono: true, shading: 'F5F6F7' }))
        }
        break
      case 'table':
        // Word 表格之间必须有段落隔开，否则紧邻的表格会被合并
        parts.push(tableXml(block.header, block.rows), '<w:p/>')
        break
      case 'divider':
        parts.push(dividerXml())
        break
      case 'image': {
        const part = images.get(block.assetId)
        if (part) {
          pictureCount += 1
          parts.push(imageDrawingXml(part, pictureCount, block.alt))
        } else if (block.alt) {
          // 图片取不到时留一行说明，读者至少知道这里原本有图
          parts.push(paragraphXml(`[图片] ${block.alt}`, { italics: true, indent: 360 }))
        }
        break
      }
    }
  }

  return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>${parts.join(
    '',
  )}<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440" w:header="720" w:footer="720" w:gutter="0"/></w:sectPr></w:body></w:document>`
}

/**
 * 把正文里的图片占位换成真实的包内部件。
 *
 * 三条硬约束（与画布导出包一致）：
 *   1. 单张图片取不到只降级为说明文字，绝不中断整篇导出；
 *   2. 非位图（SVG / PDF）Word 的 drawing 无法直接放，按「取不到」处理；
 *   3. 同一 asset 被多次引用时只存一份媒体文件，靠同一 relId 复用。
 */
export async function collectDocxImages(
  markdown: string,
  resolveImage: (assetId: string) => Promise<Blob | null>,
): Promise<{ images: DocxImageMap; files: Array<{ path: string; data: ArrayBuffer }> }> {
  const assetIds = new Set<string>()
  for (const block of parseDocBlocks(markdown)) {
    if (block.kind === 'image') assetIds.add(block.assetId)
  }

  const images: DocxImageMap = new Map()
  const files: Array<{ path: string; data: ArrayBuffer }> = []

  for (const assetId of assetIds) {
    const blob = await resolveImage(assetId).catch(() => null)
    if (!blob || blob.size === 0) continue
    const data = await blob.arrayBuffer()
    const bytes = new Uint8Array(data)
    // Blob.type 未必可信（老资产常常是空串），读不出时按魔数兜底
    const mime = normalizeImageMime(blob.type) ?? sniffImageMime(bytes)
    if (!mime) continue
    const extension = extensionOfMime(mime)
    if (!extension) continue

    const index = images.size + 1
    const size: EmuSize = toEmuSize(readImageSize(bytes) ?? { width: 480, height: 320 })
    const path = `media/image${index}.${extension}`
    images.set(assetId, {
      relId: `rIdImg${index}`,
      path,
      name: `image${index}.${extension}`,
      widthEmu: size.widthEmu,
      heightEmu: size.heightEmu,
    })
    files.push({ path: `word/${path}`, data })
  }

  return { images, files }
}

/** Word 导出：Content_Types + 根关系 + 主文档 + 内嵌图片，够 Word/WPS 正常打开。 */
export async function buildDocxBytes(input: DocExportInput): Promise<ArrayBuffer> {
  const zip = new JSZip()
  // 没传 resolver 就当「图片不可用」，正文只留说明文字
  const { images, files } = input.resolveImage
    ? await collectDocxImages(input.markdown, input.resolveImage)
    : { images: new Map<string, DocxImagePart>(), files: [] }

  const extensions = [...new Set(files.map((file) => file.path.split('.').pop() ?? ''))].filter(Boolean)

  zip.file('[Content_Types].xml', buildContentTypes(extensions))
  zip.file('_rels/.rels', DOCX_ROOT_RELS)
  zip.file('word/document.xml', buildDocxDocumentXml(input.title, input.markdown, images))
  if (files.length > 0) {
    zip.file('word/_rels/document.xml.rels', buildDocumentRels([...images.values()]))
    for (const file of files) zip.file(file.path, file.data)
  }

  // 先出字节再由调用方包成正确 MIME 的 Blob：jszip 自己的 blob 类型是 application/zip
  return zip.generateAsync({ type: 'arraybuffer', compression: 'DEFLATE' })
}

/** 组装导出结果（不触发下载，下载由调用方决定）。 */
export async function buildDocumentExport(
  format: DocExportFormat,
  input: DocExportInput,
): Promise<DocExportResult> {
  const meta = FORMAT_META[format]
  const fileName = `${sanitizeFileName(input.title, 'workbench')}${meta.extension}`

  if (format === 'docx') {
    return { blob: new Blob([await buildDocxBytes(input)], { type: meta.mime }), fileName }
  }
  const content = format === 'html' ? buildHtmlDocument(input) : buildMarkdownDocument(input)
  return { blob: new Blob([content], { type: meta.mime }), fileName }
}
