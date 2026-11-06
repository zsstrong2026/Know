/**
 * 文件夹拖拽的落点计算（P4-2）。
 *
 * 组件只负责把「拖了谁、落在谁的上/中/下」告诉这里，由纯函数算成
 * `{ parentId, position }` 发给服务端。这样最容易出错的两种情况——
 * 拖进自己的子孙（成环）和「拖到自己原来的位置」——都能被单测钉住。
 *
 * position 的口径与服务端一致：**目标父级下、排除自己之后**的插入下标。
 */

export interface FolderLike {
  id: string
  parent_id: string | null
}

/** 落点分区：行的上 28% 是 before、下 28% 是 after、中间是「放进这个文件夹」。 */
export type FolderDropZone = 'before' | 'after' | 'inside'

export interface FolderDropPlan {
  parentId: string | null
  position: number
}

/** 同一层内的兄弟，保持服务端返回的顺序（已按 sort_order 稳定排序）。 */
function siblingsOf(folders: FolderLike[], parentId: string | null): FolderLike[] {
  return folders.filter((folder) => folder.parent_id === parentId)
}

/**
 * candidate 是否就是 ancestor 本身或它的子孙。
 *
 * 用「向上走到根」而不是「向下遍历」：层级上限是 5，向上走最多 5 步，
 * 而且脏数据造成的环也会被 seen 集合兜住。
 */
export function isSelfOrDescendant(
  folders: FolderLike[],
  ancestorId: string,
  candidateId: string,
): boolean {
  const byId = new Map(folders.map((folder) => [folder.id, folder] as const))
  const seen = new Set<string>()
  let cursor: string | null = candidateId
  while (cursor) {
    if (cursor === ancestorId) return true
    if (seen.has(cursor)) return false
    seen.add(cursor)
    cursor = byId.get(cursor)?.parent_id ?? null
  }
  return false
}

/**
 * 算出拖拽结果。
 *
 * 返回 null 表示这次拖拽无效（拖到自己、拖进子孙、id 不存在），调用方应静默忽略
 * 而不是发请求——服务端虽然也会挡，但没必要用一次 400 来告诉用户。
 */
export function resolveFolderDrop(
  folders: FolderLike[],
  draggingId: string,
  targetId: string,
  zone: FolderDropZone,
): FolderDropPlan | null {
  if (!draggingId || draggingId === targetId) return null
  const target = folders.find((folder) => folder.id === targetId)
  if (!target) return null
  if (!folders.some((folder) => folder.id === draggingId)) return null
  // 拖到自己的子孙上会成环
  if (isSelfOrDescendant(folders, draggingId, targetId)) return null

  const parentId = zone === 'inside' ? target.id : target.parent_id
  // 排除自己之后再算下标：否则「拖到自己原位」会偏一位
  const siblings = siblingsOf(folders, parentId).filter((folder) => folder.id !== draggingId)
  if (zone === 'inside') {
    return { parentId, position: siblings.length }
  }
  const index = siblings.findIndex((folder) => folder.id === targetId)
  if (index < 0) {
    // 目标不在同一层（理论上不会发生）：退化成追加到末尾，避免整次拖拽丢失
    return { parentId, position: siblings.length }
  }
  return { parentId, position: zone === 'before' ? index : index + 1 }
}
