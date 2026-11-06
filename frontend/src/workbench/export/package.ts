/**
 * 画布 / 事项导出包（P2-4）。
 *
 * 设计约束（实现方案 §6.3、交接文档 §4.2）：
 *   1. **服务端不解析 Y.Doc**，所以导出包由前端组装：快照字节 + 资产 + 清单；
 *   2. 导出是**只读**行为：不改 Y.Doc、不产生版本、不写归档；
 *   3. 资产读不到（已被删除 / 网络失败）不能让整包失败——清单里标 `missing`，
 *      其余内容照常导出，这是「大画布不崩」的具体含义。
 *
 * PNG：**首版明确不支持**。BlockSuite v0.27.4 没有暴露「把整个 edgeless 视口
 * 渲染成位图」的公开 API（`affine/gfx`、`affine/edgeless` 里都没有 toPng/exportToPng）；
 * 自行按 gfx 元素序列化 SVG 再栅格化，保真度与预算都不可控。导出包里的
 * `content.ydoc` + `content.md` + 资产已能完整还原画布，需要图片时由用户在编辑器内
 * 截图即可。将来要补，入口应放在这里而不是另起一套导出链路。
 *
 * 纯逻辑与 zip 组装都和 DOM 无关，可以直接跑 `tsx --test`。
 */
import JSZip from 'jszip'

/** 包格式标识：外部工具据此判断这是不是我们的导出包。 */
export const EXPORT_FORMAT = 'weknora-workbench-export'
export const EXPORT_FORMAT_VERSION = 1

/** 与 third_party/blocksuite/UPSTREAM.md 的 pin 保持一致；升级时一起改。 */
export const BLOCKSUITE_VERSION = 'v0.27.4'

/** 带资产引用的 block；两者都用 `sourceId` 指向 workbench_item_assets。 */
const ASSET_FLAVOURS = new Set(['affine:image', 'affine:attachment'])

export interface ExportBlockLike {
  flavour?: string
  props?: Record<string, unknown>
  children?: ExportBlockLike[]
}

/** 画布里被引用的一份资产（同一 asset 被多个 block 引用时只出现一次）。 */
export interface ExportAssetRef {
  assetId: string
  /** 用户在编辑器里看到的名字；没有就用空串，由调用方回退到 assetId。 */
  name: string
  flavour: string
}

export interface BlockStats {
  /** 除根块以外的块数量，和 manifest 里的 `blocks` 同口径。 */
  blocks: number
  assets: ExportAssetRef[]
}

/**
 * 遍历块树，统计块数并收集资产引用。
 *
 * 只认 `sourceId` 字符串属性：上游 image/attachment 的资产就是它，
 * 其它字段（caption/name）只用于给人看的文件名。
 */
export function collectBlockStats(root: ExportBlockLike | null | undefined): BlockStats {
  if (!root) return { blocks: 0, assets: [] }
  const assets = new Map<string, ExportAssetRef>()
  let blocks = 0
  // 用游标而不是 shift()：大画布几千个块时，反复 shift 是 O(n²)
  const queue: ExportBlockLike[] = Array.isArray(root.children) ? [...root.children] : []
  for (let index = 0; index < queue.length; index += 1) {
    const block = queue[index]
    if (!block) continue
    blocks += 1
    const flavour = String(block.flavour ?? '')
    if (ASSET_FLAVOURS.has(flavour)) {
      const assetId = typeof block.props?.sourceId === 'string' ? block.props.sourceId : ''
      if (assetId && !assets.has(assetId)) {
        const rawName = block.props?.name ?? block.props?.caption ?? ''
        assets.set(assetId, { assetId, name: String(rawName), flavour })
      }
    }
    if (Array.isArray(block.children)) queue.push(...block.children)
  }
  return { blocks, assets: [...assets.values()] }
}

/** Windows 保留设备名：作为文件名时会失败，统一加后缀避开。 */
const RESERVED_NAMES = new Set([
  'con',
  'prn',
  'aux',
  'nul',
  'com1',
  'com2',
  'com3',
  'com4',
  'com5',
  'com6',
  'com7',
  'com8',
  'com9',
  'lpt1',
  'lpt2',
  'lpt3',
  'lpt4',
  'lpt5',
  'lpt6',
  'lpt7',
  'lpt8',
  'lpt9',
])

/** 单个文件名的长度上限，留出目录与扩展名的余量。 */
const MAX_NAME_LENGTH = 80

/**
 * 把任意标题变成安全文件名。
 *
 * 除了替换非法字符，还要挡住路径穿越（`../`）与 Windows 保留名：
 * 导出包会被解压到用户的下载目录，一个 `..\\..\\x` 就能写到目录外面。
 */
export function sanitizeFileName(name: string, fallback = 'untitled'): string {
  const cleaned = String(name ?? '')
    // 路径分隔符、Windows 非法字符、控制字符一律换掉
    .replace(/[<>:"/\\|?*\u0000-\u001f]/g, '-')
    // 连续分隔符合并，避免出现一长串 '-'
    .replace(/-{2,}/g, '-')
    .replace(/\s+/g, ' ')
    .trim()
    // 首尾的点与空格在 Windows 上会被吞掉，导致文件名与清单不一致
    .replace(/^[.\s]+|[.\s]+$/g, '')
    .slice(0, MAX_NAME_LENGTH)
  if (!cleaned) return fallback
  const stem = cleaned.split('.')[0]?.toLowerCase() ?? ''
  return RESERVED_NAMES.has(stem) ? `${cleaned}_` : cleaned
}

function extensionOf(name: string): string {
  const match = /\.([A-Za-z0-9]{1,8})$/.exec(String(name ?? ''))
  return match ? `.${match[1].toLowerCase()}` : ''
}

/** 常见类型 → 扩展名；只在原文件名没带扩展名时兜底（例如粘贴进来的截图）。 */
const MIME_EXTENSIONS: Record<string, string> = {
  'image/png': '.png',
  'image/jpeg': '.jpg',
  'image/gif': '.gif',
  'image/webp': '.webp',
  'image/svg+xml': '.svg',
  'application/pdf': '.pdf',
  'text/plain': '.txt',
  'text/markdown': '.md',
  'application/zip': '.zip',
  'video/mp4': '.mp4',
  'audio/mpeg': '.mp3',
}

function extensionFromMime(mime: string | undefined): string {
  return MIME_EXTENSIONS[String(mime ?? '').toLowerCase()] ?? ''
}

/**
 * 资产在包内的路径。
 *
 * 以 asset id 为文件名：它本身唯一且是十六进制/uuid，天然不会撞名，
 * 也不会因为用户重命名而出现两个同名文件互相覆盖。
 * 扩展名优先取原文件名；没带扩展名时按 blob 的 mime 兜底——
 * 粘贴进来的截图往往只有类型、没有名字，缺扩展名在解压后很难直接打开。
 */
export function assetEntryPath(assetId: string, name: string, mime?: string): string {
  const extension = extensionOf(name) || extensionFromMime(mime)
  return `assets/${sanitizeFileName(assetId, 'asset')}${extension}`
}

export interface ManifestAsset {
  id: string
  path: string
  name: string
  mime: string
  size: number
  /** true 表示文件没取到（已删除或网络失败），包里没有这个条目 */
  missing: boolean
}

export interface ManifestInput {
  item: {
    id: string
    title: string
    kind: string
    mode: string
    snapshotVersion: number
  }
  schemaVersion: number
  stats: BlockStats
  ydocBytes: number
  markdownCharacters: number
  assets: ManifestAsset[]
  exportedAt?: Date
}

/** 导出包内的文件布局；改这里要同步改 `assembleZip`。 */
export const PACKAGE_PATHS = {
  manifest: 'manifest.json',
  ydoc: 'content.ydoc',
  markdown: 'content.md',
  assetsDir: 'assets/',
} as const

/**
 * 组装清单。
 *
 * 键名用 snake_case：这份 json 会被外部脚本/其它语言消费，和仓库里的
 * WorkbenchItem（Go json tag）保持一致风格，便于对照。
 */
export function buildManifest(input: ManifestInput): Record<string, unknown> {
  const exportedAt = (input.exportedAt ?? new Date()).toISOString()
  const missing = input.assets.filter((asset) => asset.missing)
  return {
    format: EXPORT_FORMAT,
    format_version: EXPORT_FORMAT_VERSION,
    exported_at: exportedAt,
    item: {
      id: input.item.id,
      title: input.item.title,
      kind: input.item.kind,
      mode: input.item.mode,
      snapshot_version: input.item.snapshotVersion,
    },
    editor: {
      schema_version: input.schemaVersion,
      blocksuite: BLOCKSUITE_VERSION,
    },
    content: {
      blocks: input.stats.blocks,
      ydoc: { path: PACKAGE_PATHS.ydoc, bytes: input.ydocBytes },
      markdown: { path: PACKAGE_PATHS.markdown, characters: input.markdownCharacters },
      assets: {
        total: input.assets.length,
        missing: missing.length,
        dir: PACKAGE_PATHS.assetsDir,
      },
    },
    assets: input.assets,
    /** 明确记下首版不支持的导出形态，避免外部工具以为漏了文件 */
    capabilities: {
      png: false,
      note: 'PNG 导出未实现：BlockSuite v0.27.4 未提供视口位图导出 API',
    },
  }
}

export interface PackageFile {
  path: string
  data: Uint8Array
}

export interface ZipInput {
  manifest: Record<string, unknown>
  ydoc: Uint8Array
  markdown: string
  assets: PackageFile[]
}

/**
 * 组装 zip。
 *
 * 文本文件用 UTF-8；资产按二进制原样写入（不做二次编码，避免体积膨胀）。
 * 缺失的资产在被过滤掉之前已经进了 manifest 的 `missing` 列表，清单仍然完整。
 */
export async function assembleZip(input: ZipInput): Promise<Blob> {
  const zip = new JSZip()
  zip.file(PACKAGE_PATHS.manifest, `${JSON.stringify(input.manifest, null, 2)}\n`)
  zip.file(PACKAGE_PATHS.ydoc, input.ydoc)
  zip.file(PACKAGE_PATHS.markdown, input.markdown)
  for (const asset of input.assets) {
    if (!asset.path || !asset.data?.length) continue
    zip.file(asset.path, asset.data)
  }
  return zip.generateAsync({
    type: 'blob',
    // 快照与文本压缩收益大；图片本身已压缩，deflate 对它们接近无操作但不会变坏
    compression: 'DEFLATE',
    compressionOptions: { level: 6 },
  })
}

/** 下载文件名：`标题-工作台导出.zip`，标题非法时退回 item id。 */
export function packageFileName(title: string, fallbackId: string): string {
  return `${sanitizeFileName(title, sanitizeFileName(fallbackId, 'workbench'))}-工作台导出.zip`
}
