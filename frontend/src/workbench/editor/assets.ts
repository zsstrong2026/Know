/**
 * 画布资产桥（P2-3，实现方案 §12.2）。
 *
 * BlockSuite 的图片/附件渲染链是：
 *   ImageBlockComponent → resourceController.setEngine(blobSync) → blobSync.get(sourceId)
 * 因此这里只需要提供一个 `AssetResolver`：把 assetId 换成 Blob，并把新文件上传换回 assetId。
 *
 * 三条约束：
 *   1. 下载只认 asset id，路径由服务端拼装（防目录穿越），因此这里不能拼存储路径；
 *   2. 同一个 asset 只发一次请求：缓存的是 Promise 而不是 Blob，
 *      并发渲染多个引用同一张图的 block 时不会重复下载；
 *   3. 上传失败要清缓存，否则一次网络抖动会永久污染该 asset 的解析结果。
 */
import type { AssetResolver } from './blocksuite'
import { uploadWorkbenchAsset } from '@/api/workbench'
import { buildAuthHeaders } from '@/utils/request'

const BASE = '/api/v1/workbench'

/** 单条资产的下载地址；不要在这里拼 storage_path。 */
export function assetUrl(assetId: string): string {
  return `${BASE}/assets/${encodeURIComponent(assetId)}`
}

export interface CanvasAssetResolver extends AssetResolver {
  /** 上传一个新文件并返回 assetId；粘贴/拖拽入口使用。 */
  put(file: File): Promise<string>
}

/**
 * 创建某条目的资产解析器。
 *
 * `onResolved` 用于把“本次真正解析到的 asset id”回传给上层，
 * 由上层批量上报引用（touchWorkbenchAssets），服务端据此保留文件。
 */
export function createCanvasAssetResolver(
  itemId: string,
  options: { onResolved?: (assetId: string) => void } = {},
): CanvasAssetResolver {
  const cache = new Map<string, Promise<Blob | null>>()

  const resolve = (assetId: string): Promise<Blob | null> => {
    const cached = cache.get(assetId)
    if (cached) return cached
    const pending = fetch(assetUrl(assetId), { headers: buildAuthHeaders() })
      .then(async (response) => {
        if (!response.ok) {
          // 404 表示资产已被删除：返回 null 而不是抛错，
          // 让 BlockSuite 显示占位而不是把整个文档渲染打断。
          return null
        }
        options.onResolved?.(assetId)
        return response.blob()
      })
      .catch(() => {
        // 网络失败不留缓存：刷新或重试时应当能重新拉取。
        cache.delete(assetId)
        return null
      })
    cache.set(assetId, pending)
    return pending
  }

  return {
    resolve,
    async put(file: File): Promise<string> {
      const asset = await uploadWorkbenchAsset(itemId, file)
      // 上传成功后先把 Blob 塞进缓存，避免插入 block 后立刻回源下载自己刚传的文件。
      cache.set(asset.id, Promise.resolve(file))
      options.onResolved?.(asset.id)
      return asset.id
    },
  }
}
