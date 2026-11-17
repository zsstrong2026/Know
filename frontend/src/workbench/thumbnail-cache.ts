/**
 * 列表封面缓存：同一张缩略图在翻页/筛选间复用，避免反复走网络。
 *
 * key 带 thumbnail_version，内容更新后自然命中新图；object URL 在模块级
 * 缓存中保留，数量超过上限时回收最旧的一张（超出覆盖范围后才会被淘汰）。
 */
import { getWorkbenchThumbnail } from '@/api/workbench'
import type { WorkbenchItem } from '@/types/workbench'

const MAX_CACHED = 300
const cache = new Map<string, string>()
const inflight = new Map<string, Promise<string | null>>()

function thumbKey(item: WorkbenchItem): string | null {
  if (!item.thumbnail_version) return null
  return `${item.id}:${item.thumbnail_version}`
}

/** 同步读缓存；命中时顺便做一次 LRU 提升。 */
export function cachedThumbnail(item: WorkbenchItem): string | null {
  const key = thumbKey(item)
  if (!key) return null
  const url = cache.get(key)
  if (!url) return null
  cache.delete(key)
  cache.set(key, url)
  return url
}

/** 异步加载封面；失败返回 null，卡片回退到 CSS 骨架。 */
export async function loadThumbnail(item: WorkbenchItem): Promise<string | null> {
  const key = thumbKey(item)
  if (!key) return null
  const hit = cache.get(key)
  if (hit) return hit

  const existing = inflight.get(key)
  if (existing) return existing

  const job = getWorkbenchThumbnail(item.id, item.thumbnail_version)
    .then((blob) => {
      const url = URL.createObjectURL(blob)
      cache.set(key, url)
      while (cache.size > MAX_CACHED) {
        const oldest = cache.keys().next().value
        if (oldest === undefined) break
        const oldUrl = cache.get(oldest)
        cache.delete(oldest)
        if (oldUrl) URL.revokeObjectURL(oldUrl)
      }
      return url
    })
    .catch(() => null)
    .finally(() => inflight.delete(key))

  inflight.set(key, job)
  return job
}
