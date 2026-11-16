/**
 * 工作台 store：只保存服务端状态与 UI 状态。
 *
 * 硬约束（§9.4）：
 *   - 不保存 Y.Doc，也不持有编辑器实例（内容归属 ItemView 与 blocksuite.ts）；
 *   - 不判断错误码，错误码分支留给 composables 与视图；
 *   - 所有请求都经过 @/api/workbench，不在 store 里拼 URL。
 */
import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import i18n from '@/i18n'
import {
  createWorkbenchItem,
  deleteWorkbenchFolder,
  deleteWorkbenchItem,
  emptyWorkbenchTrash,
  getJournalDay,
  getJournalsMonth,
  getWorkbenchBootstrap,
  listWorkbenchFolders,
  moveWorkbenchFolder,
  listWorkbenchItems,
  listWorkbenchNotices,
  markAllWorkbenchNoticesRead,
  markWorkbenchNoticeRead,
  restoreWorkbenchItem,
  updateWorkbenchItem,
  updateWorkbenchFolder,
  createWorkbenchFolder,
  type ListWorkbenchItemsParams,
} from '@/api/workbench'
import { currentMonth, formatJournalTitle, todayISO } from '@/workbench/journals/date-only'
import { resolveCreateTitle } from '@/workbench/editor/prefs'
import { currentEditorPrefs } from '@/workbench/use-workbench-prefs'
import type { ItemCandidate, ItemCandidateOrigin } from '@/workbench/ai/item-candidates'
import type {
  WorkbenchBootstrap,
  WorkbenchFolder,
  WorkbenchItem,
  WorkbenchItemKind,
  WorkbenchItemSort,
  WorkbenchJournalEntry,
  WorkbenchNotice,
  WorkbenchScope,
} from '@/types/workbench'

/** scope → 列表查询条件。trash 用 trashed，folder 用 folder_id，其余各有默认。 */
export function scopeToParams(
  scope: WorkbenchScope,
  options: { folderId?: string; q?: string; tagId?: string; sort?: WorkbenchItemSort } = {},
): ListWorkbenchItemsParams {
  const base: ListWorkbenchItemsParams = { q: options.q, tag_id: options.tagId, sort: options.sort }
  switch (scope) {
    case 'database':
      return { ...base, kind: 'database' }
    case 'favorites':
      return { ...base, favorite: true }
    case 'trash':
      return { ...base, trashed: true }
    case 'folder':
      return { ...base, folder_id: options.folderId }
    default:
      // 最近：事项是全局单例视图，不是普通条目，只在「事项」里出现。
      return { ...base, exclude_kind: 'database' }
  }
}

/** 条目 → 月历条目：与后端 toJournalEntry 同口径，缺 current_mode 按 kind 兜底。 */
export function toJournalEntry(item: WorkbenchItem): WorkbenchJournalEntry {
  return {
    id: item.id,
    title: item.title,
    icon: item.icon,
    journal_date: item.journal_date ?? '',
    current_mode: item.current_mode ?? (item.kind === 'canvas' ? 'edgeless' : 'page'),
    updated_at: item.updated_at,
    archive_status: item.archive_status ?? 'none',
    archived_version: item.archived_version ?? 0,
  }
}

/**
 * 把一条 Journal 并入月历数组。
 *
 * 只接受已钉日期的条目；跨月的条目若不在当前浏览月，仍放入数组，
 * 由视图按日期过滤，避免这里再猜一次“当前月”。
 */
export function mergeJournalEntry(
  list: WorkbenchJournalEntry[],
  entry: WorkbenchJournalEntry,
): WorkbenchJournalEntry[] {
  if (!entry.journal_date) return list.filter((row) => row.id !== entry.id)
  const index = list.findIndex((row) => row.id === entry.id)
  if (index < 0) return [entry, ...list]
  const next = [...list]
  next.splice(index, 1, entry)
  return next
}

/**
 * 已确认待写入「事项」库的候选；跨条目传递，不落服务端。
 *
 * 文档页没有事项库的 dataSource，无法直接写库；用户确认后先暂存，
 * 由事项页在数据库工具就绪后取出并应用。
 */
export interface PendingCollect {
  /** 目标（事项）条目 id：事项页只在匹配时消费 */
  itemId: string
  candidates: ItemCandidate[]
  /** 来源标记：手动识别 / 归档自动抽取 / 正文待办规则路径 */
  origin: ItemCandidateOrigin
  /** 来源文档标题，写进「来源文档」列 */
  sourceTitle?: string
}

export const useWorkbenchStore = defineStore('workbenchStore', () => {
  const bootstrap = ref<WorkbenchBootstrap | null>(null)
  const items = ref<WorkbenchItem[]>([])
  const folders = ref<WorkbenchFolder[]>([])
  const nextCursor = ref('')
  const loading = ref(false)
  const loadingMore = ref(false)
  /** Journals 月历：当前浏览月与其条目（与列表 items 分开，互不覆盖） */
  const journalsMonth = ref(currentMonth())
  const journals = ref<WorkbenchJournalEntry[]>([])
  const journalsTruncated = ref(false)
  const journalsLoading = ref(false)
  /** 右侧 Journals 面板开合；导航行日历按钮与页面内折叠共用 */
  const journalPanelOpen = ref(true)
  /** 未读通知（tenantless）：通知条与侧栏 badge 共用 */
  const notices = ref<WorkbenchNotice[]>([])
  const unreadNotices = computed(() => notices.value.filter((row) => row.read_at === null).length)

  /** 工作知识库 id：bootstrap 未成功时为空，界面隐藏“打开知识库”入口。 */
  const workKbId = computed(() => bootstrap.value?.work_kb.id ?? '')

  /**
   * 发布门禁（§9.1）：bootstrap 未返回时一律按关闭处理，
   * 避免开关关闭的部署在首屏闪一下入口再消失。
   */
  const features = computed(() => bootstrap.value?.features ?? {
    templates: false,
    journals: false,
    ai: false,
    dual_mode: false,
  })

  async function fetchBootstrap() {
    bootstrap.value = await getWorkbenchBootstrap()
    return bootstrap.value
  }

  async function fetchItems(
    scope: WorkbenchScope,
    options: { folderId?: string; q?: string; tagId?: string; sort?: WorkbenchItemSort } = {},
  ) {
    loading.value = true
    try {
      const page = await listWorkbenchItems(scopeToParams(scope, options))
      items.value = page.items ?? []
      nextCursor.value = page.next_cursor ?? ''
    } finally {
      loading.value = false
    }
  }

  /** 加载下一页并追加；没有 next_cursor 时是空操作。 */
  async function loadMore(
    scope: WorkbenchScope,
    options: { folderId?: string; q?: string; tagId?: string; sort?: WorkbenchItemSort } = {},
  ) {
    if (!nextCursor.value || loadingMore.value) return
    loadingMore.value = true
    try {
      const page = await listWorkbenchItems({
        ...scopeToParams(scope, options),
        cursor: nextCursor.value,
      })
      const seen = new Set(items.value.map((item) => item.id))
      for (const item of page.items ?? []) {
        if (!seen.has(item.id)) items.value.push(item)
      }
      nextCursor.value = page.next_cursor ?? ''
    } finally {
      loadingMore.value = false
    }
  }

  async function fetchFolders() {
    folders.value = await listWorkbenchFolders()
  }

  /**
   * 条目归属变化后刷新文件夹树上的文档计数。
   *
   * 做 200ms 防抖：批量移入/删除会连续触发多次，最终只回源一次；
   * 失败静默，计数过期不影响主流程。
   */
  let folderCountTimer: ReturnType<typeof setTimeout> | undefined
  function refreshFolderCountsSoon() {
    if (folderCountTimer) clearTimeout(folderCountTimer)
    folderCountTimer = setTimeout(() => {
      folderCountTimer = undefined
      void fetchFolders().catch(() => undefined)
    }, 200)
  }

  /** 拉未读通知；失败静默（通知条不出现，不影响工作台主流程）。 */
  async function fetchNotices() {
    try {
      const page = await listWorkbenchNotices({ unreadOnly: true, limit: 20 })
      notices.value = page.items ?? []
    } catch {
      // 被移出 workspace 时 tenantless 读取失败也只降级为无通知。
    }
  }

  /** 标记单条已读：本地先移除，失败回滚由下次拉取修正。 */
  async function markNoticeRead(id: string) {
    notices.value = notices.value.filter((row) => row.id !== id)
    await markWorkbenchNoticeRead(id)
  }

  /** 全部已读：清空本地列表。 */
  async function markAllNoticesRead() {
    notices.value = []
    await markAllWorkbenchNoticesRead()
  }

  /**
   * 唯一「事项」条目（kind='database'）。
   *
   * 事项是全局单例：不提供新建入口，进入事项视图时按需创建一次。
   * 用模块级 promise 缓存并发结果，避免多个调用同时创建出两条。
   */
  let thingsItemPromise: Promise<WorkbenchItem> | null = null
  function ensureThingsItem(): Promise<WorkbenchItem> {
    if (!thingsItemPromise) {
      thingsItemPromise = (async () => {
        // 历史上可能已存在多条数据库条目：复用最近更新的一条，其余保留但不再暴露
        const page = await listWorkbenchItems({ kind: 'database', limit: 1 })
        const existing = page.items[0]
        if (existing) return existing
        return createWorkbenchItem({
          kind: 'database',
          title: i18n.global.t('workbench.nav.databases'),
        })
      })().catch((error) => {
        // 失败时清空缓存，下次进入可以重试
        thingsItemPromise = null
        throw error
      })
    }
    return thingsItemPromise
  }

  async function createItem(
    kind: WorkbenchItemKind,
    title?: string,
    folderId?: string | null,
    /** 显式指定初始形态；「新建文档」用 'page'，绕过编辑器偏好的默认形态 */
    modeOverride?: 'page' | 'edgeless',
    /** 传了就顺手把它钉进该日期的 Journals（工作台侧栏的「新建」用当天） */
    journalDate?: string,
  ) {
    // 普通条目读编辑器偏好：空标题可用设备本地日期命名，初始形态取 defaultCreateMode。
    // Journal 不走这里（createJournal 固定 page + 固定标题），因此两者互不影响（§9.14.4）。
    const prefs = currentEditorPrefs()
    const resolvedTitle = resolveCreateTitle(prefs, title ?? '')
    // 占位标题走 i18n：英文界面不该出现中文「未命名」。
    const placeholder =
      kind === 'database'
        ? i18n.global.t('workbench.item.untitledDatabase')
        : i18n.global.t('workbench.item.untitledDocument')
    const item = await createWorkbenchItem({
      kind,
      title: resolvedTitle || placeholder,
      folder_id: folderId ?? null,
      ...(journalDate ? { journal_date: journalDate } : {}),
      // 数据库条目的形态由 kind 决定，不受页面/画布偏好与 override 影响。
      ...(kind === 'canvas' ? { current_mode: modeOverride ?? prefs.defaultCreateMode } : {}),
    })
    items.value = [item, ...items.value]
    // 新建到某个文件夹时，文件夹树上的数量要立刻跟上
    if (folderId) refreshFolderCountsSoon()
    return item
  }

  /**
   * 新建 Journal（§9.13.3）。
   *
   * 固定 kind='canvas'、current_mode='page'、标题「YYYY 年 M 月 D 日」；
   * journal_date 一旦传了就把它钉进月历，不经过普通条目的默认形态。
   */
  async function createJournal(date: string = todayISO()) {
    const item = await createWorkbenchItem({
      kind: 'canvas',
      title: formatJournalTitle(date),
      folder_id: null,
      journal_date: date,
      current_mode: 'page',
    })
    items.value = [item, ...items.value]
    journals.value = mergeJournalEntry(journals.value, toJournalEntry(item))
    return item
  }

  /** 拉取某月 Journals；缺省月由后端按服务器时间给出。 */
  async function fetchJournals(month?: string) {
    journalsLoading.value = true
    try {
      const data = await getJournalsMonth(month)
      journalsMonth.value = data.month
      journals.value = data.entries ?? []
      journalsTruncated.value = Boolean(data.truncated)
      return data
    } finally {
      journalsLoading.value = false
    }
  }

  /** 取某天的 Journal；0 条是合法结果，不是错误。 */
  async function fetchJournalDay(date: string) {
    const data = await getJournalDay(date)
    return data.entries ?? []
  }

  /**
   * 改期 / 取消钉（§9.13.5）。
   *
   * 只写 workbench_items.journal_date，绝不回写 Y.Doc 里的数据库日期列。
   * 乐观更新由调用方负责；这里成功后同步本地 journals 与 items。
   */
  async function setJournalDate(id: string, journalDate: string | null) {
    // UI 的 null 表示取消钉（PUT 语义里 null=不改，必须传空串才清空）
    const item = await updateWorkbenchItem(id, { journal_date: journalDate ?? '' })
    upsertItem(item)
    if (journalDate) {
      journals.value = mergeJournalEntry(journals.value, toJournalEntry(item))
    } else {
      journals.value = journals.value.filter((entry) => entry.id !== id)
    }
    return item
  }

  /** 本地增量更新，避免每次改标题都整表重拉。 */
  function upsertItem(item: WorkbenchItem) {
    const index = items.value.findIndex((row) => row.id === item.id)
    if (index >= 0) items.value.splice(index, 1, item)
    else items.value.unshift(item)
  }

  function removeItem(id: string) {
    items.value = items.value.filter((item) => item.id !== id)
  }

  async function renameItem(id: string, title: string) {
    const item = await updateWorkbenchItem(id, { title })
    upsertItem(item)
    return item
  }

  async function toggleFavorite(item: WorkbenchItem) {
    const updated = await updateWorkbenchItem(item.id, { is_favorite: !item.is_favorite })
    upsertItem(updated)
    return updated
  }

  async function moveItemToFolder(id: string, folderId: string | null) {
    const payload = folderId ? { folder_id: folderId } : { move_to_root: true }
    const item = await updateWorkbenchItem(id, payload)
    upsertItem(item)
    refreshFolderCountsSoon()
    return item
  }

  /**
   * 钉到某天 / 改期 / 取消钉。
   *
   * date 为空串表示取消钉（服务端三态："" → 写 NULL），不是“不改”。
   */
  async function pinItemToDate(id: string, date: string) {
    const item = await updateWorkbenchItem(id, { journal_date: date })
    upsertItem(item)
    return item
  }

  /**
   * 删除条目。purge=false 进回收站（delete_archives 只标映射）；
   * purge=true 且 delete_archives=true 时后端先删归档知识再硬删条目。
   */
  async function removeItemPermanently(id: string, opts: { deleteArchives?: boolean } = {}) {
    await deleteWorkbenchItem(id, { purge: true, delete_archives: opts.deleteArchives })
    removeItem(id)
  }

  async function trashItem(id: string, opts: { deleteArchives?: boolean } = {}) {
    await deleteWorkbenchItem(id, { delete_archives: opts.deleteArchives })
    removeItem(id)
    // 回收站条目不计入文件夹数量，移入后回源一次保持准确
    refreshFolderCountsSoon()
  }

  async function restoreItem(id: string) {
    const item = await restoreWorkbenchItem(id)
    removeItem(id)
    refreshFolderCountsSoon()
    return item
  }

  /** 一键清空回收站：服务端硬删全部条目，本地列表直接清空。 */
  async function emptyTrash() {
    const purged = await emptyWorkbenchTrash()
    items.value = []
    nextCursor.value = ''
    return purged
  }

  async function addFolder(name: string, parentId: string | null) {
    const folder = await createWorkbenchFolder({ name, parent_id: parentId })
    folders.value = [...folders.value, folder]
    return folder
  }

  async function renameFolder(id: string, name: string) {
    const folder = await updateWorkbenchFolder(id, { name })
    // 改名响应不带聚合计数，保留列表里的原值，避免数字闪没
    folders.value = folders.value.map((row) =>
      row.id === folder.id ? { ...folder, item_count: row.item_count } : row,
    )
    return folder
  }

  /**
   * 移动文件夹（P4-2）：换父级 + 指定层内位置。
   *
   * position < 0 表示追加到末尾（老调用方只关心层级时用这个）。
   * 服务端会整层重写 sort_order，所以本地直接回源一次文件夹列表，
   * 比在客户端自己算新顺序更不容易和真实顺序漂移。
   */
  async function moveFolder(id: string, parentId: string | null, position = -1) {
    const folder = await moveWorkbenchFolder(id, { parent_id: parentId, position })
    folders.value = await listWorkbenchFolders()
    return folder
  }

  /** 删除文件夹：后端把其中的子文件夹与条目上移到父级，这里同步树结构。 */
  async function removeFolder(id: string) {
    const removed = folders.value.find((row) => row.id === id)
    await deleteWorkbenchFolder(id)
    folders.value = folders.value
      .filter((row) => row.id !== id)
      // 直接子级的父级应回挂到被删文件夹的父级（后端同口径），而不是一律提到根。
      .map((row) => (row.parent_id === id ? { ...row, parent_id: removed?.parent_id ?? null } : row))
    // 被删文件夹里的文档已上移，相关层级的计数需要回源
    refreshFolderCountsSoon()
  }

  /**
   * 待写入「事项」库的收集结果。
   *
   * 文档页没有事项库的 dataSource，无法直接写库；用户确认后先暂存，
   * 由事项页在数据库工具就绪后取出并应用。
   */
  const pendingCollect = ref<PendingCollect | null>(null)

  function setPendingCollect(payload: PendingCollect) {
    pendingCollect.value = payload
  }

  /** 取出并清空暂存：同一批待办只应用一次。 */
  function takePendingCollect(): PendingCollect | null {
    const payload = pendingCollect.value
    pendingCollect.value = null
    return payload
  }

  return {
    bootstrap,
    items,
    folders,
    nextCursor,
    loading,
    loadingMore,
    journalsMonth,
    journals,
    journalsTruncated,
    journalsLoading,
    journalPanelOpen,
    notices,
    unreadNotices,
    workKbId,
    features,
    fetchBootstrap,
    fetchItems,
    loadMore,
    fetchFolders,
    fetchNotices,
    markNoticeRead,
    markAllNoticesRead,
    ensureThingsItem,
    createItem,
    createJournal,
    fetchJournals,
    fetchJournalDay,
    setJournalDate,
    upsertItem,
    removeItem,
    renameItem,
    toggleFavorite,
    moveItemToFolder,
    pinItemToDate,
    trashItem,
    removeItemPermanently,
    restoreItem,
    emptyTrash,
    addFolder,
    renameFolder,
    moveFolder,
    removeFolder,
    setPendingCollect,
    takePendingCollect,
  }
})
