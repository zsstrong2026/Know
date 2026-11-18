/**
 * 条目编辑页「返回墨匣」的去向解析。
 *
 * 编辑页可以从多个入口进入（最近 / 收藏 / 文件夹 / Journals / 关联卡片…），
 * 因此不能固定回「最近」，否则用户从文件夹里打开的文档，返回时会丢失文件夹上下文。
 * 优先级：
 *   1. 浏览历史里上一页仍在墨匣内且不是另一个编辑页 → 直接回退，URL（含搜索/排序 query）原样恢复；
 *   2. 否则按条目自身的 folder_id 回到所属文件夹；
 *   3. 条目不在任何文件夹（或加载失败）时兜底回「最近」。
 *
 * 抽成纯函数便于单测：这里最容易出错的正是「什么样的上一页才算可以回退」。
 */

/** 墨匣（工作台）路由前缀。 */
export const WORKBENCH_PREFIX = '/platform/workbench'

/** 编辑页路由前缀：编辑页之间互相跳转（关联卡片 / 反向链接）不应成为返回目标。 */
const WORKBENCH_ITEM_PREFIX = `${WORKBENCH_PREFIX}/items/`

export type WorkbenchBackTarget =
  | { kind: 'history' }
  | { kind: 'folder'; folderId: string }
  | { kind: 'recent' }

export interface WorkbenchBackContext {
  /** history.state.back：上一页的完整路径；null/undefined 表示没有站内历史 */
  previous: unknown
  /** 当前条目所属文件夹 id；null 表示未归入任何文件夹 */
  folderId: string | null | undefined
}

/** 只有墨匣内的非编辑页（列表 / 文件夹 / Journals / 模板…）才值得回退历史。 */
export function isBackableWorkbenchPath(previous: unknown): boolean {
  // 历史态里的 back 可能是路径字符串、数字（外部历史长度）或空值
  if (typeof previous !== 'string' || previous === '') return false
  const path = previous.split(/[?#]/)[0]
  if (path !== WORKBENCH_PREFIX && !path.startsWith(`${WORKBENCH_PREFIX}/`)) return false
  return !path.startsWith(WORKBENCH_ITEM_PREFIX)
}

export function resolveWorkbenchBack(context: WorkbenchBackContext): WorkbenchBackTarget {
  if (isBackableWorkbenchPath(context.previous)) return { kind: 'history' }
  if (context.folderId) return { kind: 'folder', folderId: context.folderId }
  return { kind: 'recent' }
}
