/**
 * 画布导出的执行层（P2-4）。
 *
 * 与 `package.ts` 的分工：那边只做纯逻辑与 zip 组装（可单测），
 * 这边负责从运行中的编辑器取快照、取资产、触发下载（依赖 DOM/网络，不做单测）。
 *
 * 三条硬约束：
 *   1. 只读：不 markDirty、不 flush、不产生版本；
 *   2. 资产取不到不算失败，记进 manifest 的 missing 后继续；
 *   3. 全程不碰服务端，导出包完全由浏览器组装（服务端不解析 Y.Doc）。
 */
import {
  assembleZip,
  assetEntryPath,
  buildManifest,
  collectBlockStats,
  packageFileName,
  PACKAGE_PATHS,
  type ExportBlockLike,
  type ManifestAsset,
} from './package'

export interface CanvasExportItem {
  id: string
  title: string
  kind: string
  mode: string
  snapshotVersion: number
}

export interface CanvasExportSource {
  /** 块树根（BlockSuite 的 `store.root`） */
  root: ExportBlockLike | null | undefined
  /** 当前 Y.Doc 快照字节 */
  snapshot: Uint8Array
  /** 投影出来的 Markdown */
  markdown: string
  /** 与编辑器渲染共用同一个 resolver，命中缓存时不重复下载 */
  resolveAsset(assetId: string): Promise<Blob | null>
  /** 服务端记录的编辑器 schema 版本 */
  schemaVersion: number
}

export interface CanvasExportResult {
  blob: Blob
  fileName: string
  blocks: number
  assetCount: number
  missingCount: number
}

/** 资产并发下载上限：太多会挤占带宽并让浏览器排队，4 条足够跑满本地服务。 */
const ASSET_CONCURRENCY = 4

/**
 * 按上限并发跑一批任务，保持结果顺序与输入一致。
 *
 * 顺序必须保持：manifest 里的 assets 数组与 zip 内的文件要能一一对上，
 * 排查问题时要靠这个顺序定位。
 */
async function mapWithLimit<T, R>(items: T[], limit: number, run: (item: T) => Promise<R>): Promise<R[]> {
  const results = new Array<R>(items.length)
  let cursor = 0
  const workers = Array.from({ length: Math.min(limit, items.length) }, async () => {
    while (cursor < items.length) {
      const index = cursor
      cursor += 1
      results[index] = await run(items[index])
    }
  })
  await Promise.all(workers)
  return results
}

/** 组装导出包，不触发下载（下载由调用方决定，便于以后改成“导出到指定目录”）。 */
export async function buildCanvasPackage(
  item: CanvasExportItem,
  source: CanvasExportSource,
): Promise<CanvasExportResult> {
  const stats = collectBlockStats(source.root)

  // 取资产：失败（已删除 / 网络）记 missing，不中断整包
  const resolved = await mapWithLimit(stats.assets, ASSET_CONCURRENCY, async (ref) => {
    const blob = await source.resolveAsset(ref.assetId).catch(() => null)
    return { ref, blob }
  })

  const assets: ManifestAsset[] = []
  const files: Array<{ path: string; data: Uint8Array }> = []
  let missingCount = 0

  for (const { ref, blob } of resolved) {
    // 扩展名优先用原文件名，缺的时候按 blob 的 mime 兜底
    const path = assetEntryPath(ref.assetId, ref.name, blob?.type)
    if (!blob) {
      missingCount += 1
      assets.push({
        id: ref.assetId,
        path,
        name: ref.name,
        mime: '',
        size: 0,
        missing: true,
      })
      continue
    }
    const data = new Uint8Array(await blob.arrayBuffer())
    assets.push({
      id: ref.assetId,
      path,
      name: ref.name,
      mime: blob.type || '',
      size: data.byteLength,
      missing: false,
    })
    files.push({ path, data })
  }

  const manifest = buildManifest({
    item,
    schemaVersion: source.schemaVersion,
    stats,
    ydocBytes: source.snapshot.byteLength,
    markdownCharacters: source.markdown.length,
    assets,
  })

  const blob = await assembleZip({
    manifest,
    ydoc: source.snapshot,
    markdown: source.markdown,
    assets: files,
  })

  return {
    blob,
    fileName: packageFileName(item.title, item.id),
    blocks: stats.blocks,
    assetCount: assets.length,
    missingCount,
  }
}

/** 触发浏览器下载；用 objectURL 而不是 data URL，避免大包撑爆 href 长度限制。 */
export function downloadBlob(blob: Blob, fileName: string): void {
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = fileName
  anchor.rel = 'noopener'
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  // 立即回收会让部分浏览器取消下载，留到下一轮事件循环
  setTimeout(() => URL.revokeObjectURL(url), 0)
}

/** 清单里固定文件的名字，供界面/文档引用，避免各处硬编码。 */
export { PACKAGE_PATHS }
