/**
 * 编辑器会话：BlockSuite 挂载 + 保存队列 + 冲突/重载（§9.7）。
 *
 * 条目编辑页与 Journals 主区共用同一份实现，避免把「取快照、base64、409 处理」
 * 抄两遍。视图只负责顶栏、归档与路由，内容生命周期全部交给这里。
 */
import { ref, shallowRef, watch, type Ref } from 'vue'
import i18n from '@/i18n'
import { getWorkbenchContent, saveWorkbenchContent } from '@/api/workbench'
import {
  createSaveQueue,
  currentClientId,
  type SaveQueue,
  type SaveState,
} from '@/workbench/save-queue'
import { projectMarkdown } from './projection'
import { noteStyleSeedFromPrefs } from './prefs'
import {
  ensureWorkbenchSourceColumns,
  migrateLegacyTextColumns,
  removeLegacyTodoStatusOption,
  removeObsoleteWorkbenchColumns,
  seedWorkbenchDatabase,
  type DatabaseSeedLabels,
} from './database-seed'
import { createCanvasAssetResolver, type CanvasAssetResolver } from './assets'
import { touchWorkbenchAssets } from '@/api/workbench'
import { useWorkbenchPrefs } from '@/workbench/use-workbench-prefs'
import type {
  CanvasHandle,
  EditorKind,
  EditorMode,
  EditorSeedContext,
} from './blocksuite'
import type { WorkbenchItem } from '@/types/workbench'

/** 偏好 → BlockSuite 的编辑器级设置（§9.14.2）。 */
function runtimeSettingsFromPrefs(prefs = useWorkbenchPrefs().editorPrefs.value) {
  return {
    edgelessScrollZoom: prefs.scrollZoom,
    codeBlockLineNumbers: prefs.codeLineNumbers,
  }
}

export interface EditorSessionOptions {
  itemId: Ref<string>
  /** 编辑器宿主元素；挂载前必须已渲染 */
  host: Ref<HTMLElement | null>
  /** 条目元数据；kind / current_mode 从这里取 */
  item: Ref<WorkbenchItem | null>
  /** 指定初始形态；缺省按 item.current_mode 兜底 */
  mode?: Ref<EditorMode>
  /** 保存冲突回调（视图弹三选项） */
  onConflict?: () => void
}

export interface EditorSession {
  ready: Ref<boolean>
  saveState: Ref<SaveState>
  /** 最近一次成功保存的时间戳（ms）；没保存过是 null。事项页用它显示「已保存 · 16:03」 */
  savedAt: Ref<number | null>
  editor: Ref<CanvasHandle | null>
  /** 最近一次成功保存后的快照版本，归档时作为 base_version */
  baseVersion: Ref<number>
  /** 当前 Y.Doc 的 editor schema 版本；导出包的 manifest 需要它 */
  schemaVersion: Ref<number>
  /**
   * 按 asset id 取 Blob（P2-4 导出用）。
   *
   * 与编辑器渲染共用同一个 resolver 与同一份缓存：刚渲染过的图片不会为导出再下一次。
   */
  resolveAsset(assetId: string): Promise<Blob | null>
  /** 当前形态；编辑页顶栏切换需要读它做高亮（§10.14.5） */
  mode: Ref<EditorMode>
  /** 首次挂载：拉快照 → 建队列 → 渲染 */
  mount(options?: MountOptions): Promise<void>
  /**
   * 用模板快照直接挂载（§9.16.2）。
   *
   * 目标文档必须仍是空的；已存在内容时抛 `template-target-not-empty`，
   * 由视图重载文档并提示，绝不覆盖用户内容。
   */
  mountFromTemplate(tpl: TemplateMountOptions): Promise<void>
  /**
   * 切换形态：只重建视图层，不动 Y.Doc、不产生版本（§10.14.5）。
   * 返回是否真的发生了切换（同形态调用是空操作）。
   */
  setMode(next: EditorMode): boolean
  /** 丢弃本地改动，按服务端最新快照重建 */
  reload(): Promise<void>
  /** 冲突时“用我的版本覆盖” */
  forceSave(): Promise<void>
  /** 跳过防抖立即落盘（切换 Journal / 离开页面前调用） */
  flush(): Promise<void>
  /** 离开当前条目是否安全：冲突或保存失败时不允许静默丢弃（§9.13.1） */
  canLeave(): boolean
  /** 把当前 Y.Doc 投影成 Markdown，供归档使用 */
  markdown(): string
  /**
   * 同上，但保留图片位置（`![说明](asset://<id>)`）。
   *
   * 只给文档导出用：归档/搜索正文不需要图片地址，默认投影会把它滤掉。
   */
  markdownWithImages(): string
  /** 当前正文的非空白字符数；文档信息行用，不额外请求接口 */
  markdownCount(): number
  /** 上传粘贴/拖拽的文件，返回 assetId（P2-3）；失败时抛错由调用方提示 */
  uploadAsset(file: File): Promise<string>
  /** 上报本次会话引用到的资产，刷新服务端 last_seen_at（P2-3） */
  reportAssetRefs(): void
  destroy(): void
}

export interface MountOptions {
  /**
   * 空文档时延迟首次保存，直到用户真正编辑（§9.16.2）。
   *
   * 只给「从空白文档开始」用：否则点一下就产生 version 1，
   * 工具栏的「使用模板」会立刻消失，用户没有反悔机会。
   */
  deferBlankSave?: boolean
}

/** 模板套用所需的输入：已经校验过的快照字节。 */
export interface TemplateMountOptions {
  snapshot: Uint8Array
  editorSchemaVersion: number
}

function kindOf(item: WorkbenchItem | null): EditorKind {
  return (item?.kind ?? 'canvas') as EditorKind
}

/**
 * 数据库条目的初始内容：3 列 + 3 视图的空骨架（P3-2）。
 *
 * 画布条目返回 undefined —— 空白画布就是正确的初始状态，不需要 seed。
 * seed 只在「没有快照」时由 mountEditor 调用，因此恢复历史条目不会重复插入。
 */
function seedFor(item: WorkbenchItem | null, labels: DatabaseSeedLabels) {
  if (kindOf(item) !== 'database') return undefined
  // 数据库块自带标题，默认英文 “Untitled”；这里用条目名填充，避免与顶栏标题不一致。
  return (ctx: EditorSeedContext) => {
    seedWorkbenchDatabase(ctx, { ...labels, databaseTitle: item?.title ?? labels.databaseTitle })
  }
}

/**
 * 数据库列名的 i18n 文案。
 *
 * seed 在 store 建立后立即执行，此时 Vue 的 i18n 实例可用；
 * 用 t() 而不是硬编码中文，六语言用户都能看到母语列名。
 * 导出供离屏会话（item-extract）在事项条目尚未建库时用同一套列定义建库。
 */
export function editorSeedLabels(): DatabaseSeedLabels {
  return {
    titleColumn: i18n.global.t('workbench.database.columns.title'),
    statusColumn: i18n.global.t('workbench.database.columns.status'),
    dateColumn: i18n.global.t('workbench.database.columns.date'),
    ownerColumn: i18n.global.t('workbench.database.columns.owner'),
    priorityColumn: i18n.global.t('workbench.database.columns.priority'),
    relationColumn: i18n.global.t('workbench.database.columns.relation'),
    sourceColumn: i18n.global.t('workbench.database.columns.source'),
    sourceDocColumn: i18n.global.t('workbench.database.columns.sourceDoc'),
    sourceOptions: [
      i18n.global.t('workbench.database.source.manual'),
      i18n.global.t('workbench.database.source.ai'),
      i18n.global.t('workbench.database.source.archive'),
    ],
    databaseTitle: i18n.global.t('workbench.item.untitled'),
    statusOptions: [
      i18n.global.t('workbench.database.status.todo'),
      i18n.global.t('workbench.database.status.doing'),
      i18n.global.t('workbench.database.status.done'),
    ],
    viewNames: {
      table: i18n.global.t('workbench.database.views.table'),
      kanban: i18n.global.t('workbench.database.views.kanban'),
      calendar: i18n.global.t('workbench.database.views.calendar'),
    },
  }
}

function defaultMode(item: WorkbenchItem | null): EditorMode {
  if (item?.current_mode === 'page' || item?.current_mode === 'edgeless') return item.current_mode
  // 与后端 DefaultModeForKind 一致：画布默认无限画布，数据库默认文档
  return kindOf(item) === 'canvas' ? 'edgeless' : 'page'
}

export function useEditorSession(options: EditorSessionOptions): EditorSession {
  const { itemId, host, item } = options
  const ready = ref(false)
  const saveState = ref<SaveState>('idle')
  /** 最近一次成功落盘的时间；只在 saved 状态推进，冲突/失败不刷新。 */
  const savedAt = ref<number | null>(null)
  // shallowRef：编辑器实例内部有大量不可序列化字段，深度代理只会拖慢渲染。
  const editor = shallowRef<CanvasHandle | null>(null)
  const baseVersion = ref(0)
  /** 挂载时从快照里带出来的 schema 版本；导出包的 manifest 会写它。 */
  const editorSchemaVersion = ref(0)
  // 当前形态：由挂载时确定，切换时更新；顶栏据此高亮。
  const mode = ref<EditorMode>(options.mode?.value ?? defaultMode(item.value))
  let queue: SaveQueue | null = null
  /**
   * 画布资产桥（P2-3）。
   *
   * 每个会话一份：resolver 内部缓存已解析的 Blob，重挂载（reload/切模板）时
   * 重新创建，避免把上一次会话的失效资产带进新视图。
   */
  let canvasAssets: CanvasAssetResolver | null = null
  /** 本次会话真正解析到的 asset id，用于在卸载前批量上报引用。 */
  const seenAssets = new Set<string>()

  function ensureCanvasAssets(): CanvasAssetResolver {
    if (canvasAssets) return canvasAssets
    canvasAssets = createCanvasAssetResolver(itemId.value, {
      onResolved: (assetId) => seenAssets.add(assetId),
    })
    return canvasAssets
  }

  // 设置页改「滚轮缩放 / 代码行号 / 笔记样式」后立刻生效：
  // 前两项只改 setting signal，笔记样式只影响之后新建的笔记，都不重建视图。
  const { editorPrefs } = useWorkbenchPrefs()
  watch(
    () => [
      editorPrefs.value.scrollZoom,
      editorPrefs.value.codeLineNumbers,
      editorPrefs.value.noteBackground,
      editorPrefs.value.noteRadius,
      editorPrefs.value.noteShadow,
      editorPrefs.value.noteBorderStyle,
      editorPrefs.value.noteBorderWidth,
    ] as const,
    () => {
      editor.value?.updateSettings(runtimeSettingsFromPrefs(editorPrefs.value))
      editor.value?.updateNoteStyle(noteStyleSeedFromPrefs(editorPrefs.value))
    },
  )

  /**
   * 挂载编辑器。
   *
   * `overrideSnapshot` 供模板套用使用：跳过 GET content 取到的字节，直接用调用方
   * 校验过的快照。此时队列仍以服务端真实版本为 base_version，因此并发保存会正常 2401。
   */
  async function mountWith(
    overrideSnapshot: Uint8Array | null | undefined,
    mountOptions: MountOptions,
    schemaVersionOverride?: number,
  ): Promise<void> {
    if (!host.value || !item.value) return
    // 一次会话只允许一个编辑器实例：从空白切到模板（或重挂载）时先销毁旧的，
    // 否则 DOM 里会留下两个 BlockSuite 编辑器，模板内容渲染在视口之外。
    editor.value?.destroy()
    editor.value = null
    ready.value = false
    queue?.dispose()
    queue = null
    // 重挂载（刷新/切模板/恢复版本）换一份资产缓存：旧会话解析过的 Blob 可能与
    // 服务端已删除的资产对应，复用会让破图一直显示。
    canvasAssets = null
    seenAssets.clear()
    // 动态 import：只有真正打开内容时才加载 BlockSuite，首屏不背这份体积。
    // 模块图与内容请求互不依赖，并行发起：整体节省一个串行往返。
    // 模块加载是这里最贵的一段（vendored 源码有上千个模块），不能排在请求后面。
    const [bs, content] = await Promise.all([
      import('./blocksuite'),
      getWorkbenchContent(itemId.value),
    ])
    const { mountEditor } = bs
    baseVersion.value = content.snapshot_version
    const snapshot =
      overrideSnapshot !== undefined
        ? overrideSnapshot
        : content.ydoc
          ? base64ToBytes(content.ydoc)
          : null
    const targetMode = options.mode?.value ?? defaultMode(item.value)
    mode.value = targetMode
    const schemaVersion = schemaVersionOverride ?? content.editor_schema_version
    editorSchemaVersion.value = schemaVersion

    queue = createSaveQueue({
      itemId: itemId.value,
      clientId: currentClientId(),
      baseVersion: content.snapshot_version,
      save: saveWorkbenchContent,
      read: () => ({
        ydoc: bytesToBase64(editor.value?.getSnapshot() ?? new Uint8Array()),
        // text_content 存纯文本投影：它既是列表搜索的 LIKE 目标，也是归档正文的来源。
        textContent: projectMarkdown(editor.value?.store).markdown,
        editorSchemaVersion: schemaVersion,
      }),
      onState: (state) => {
        saveState.value = state
        // 保存成功即推进基线，归档时的 base_version 才不会落后
        if (state === 'saved') {
          // 队列里的时间戳才是真正落盘的那一刻；回退到 Date.now() 只兜首帧时序。
          savedAt.value = queue?.lastSavedAt() ?? Date.now()
          syncBaseVersion()
        }
        if (state === 'conflict') options.onConflict?.()
      },
      // 空文档且显式要求延迟：只有用户真的改了内容才落盘（§9.16.2）。
      deferBlankCommit: mountOptions.deferBlankSave === true,
    })

    const seedLabels = editorSeedLabels()
    editor.value = await mountEditor({
      element: host.value,
      itemId: itemId.value,
      kind: kindOf(item.value),
      mode: targetMode,
      snapshot,
      // 滚轮缩放、代码行号由 BlockSuite 内部读取，只能走 EditorSettingProvider
      settings: runtimeSettingsFromPrefs(editorPrefs.value),
      // 新建笔记的初始样式；已有笔记不会被追溯改写
      noteStyle: noteStyleSeedFromPrefs(editorPrefs.value),
      // 数据库条目首次打开：注入列与视图的空数据库骨架（P3-2）。
      // 只在没有快照时执行，恢复历史条目不会重复插入。
      seed: seedFor(item.value, seedLabels),
      // 图片/附件资产（P2-3）：挂到 workspace.blobSync，刷新后由 resolver 重新拉取。
      assets: ensureCanvasAssets(),
      onChange: () => queue?.markDirty(),
    })

    // 老条目里可能残留按旧原型加的四列（描述 / 需求来源 / 价值达成 / 所属版本），
    // 真实原型只有六列，这里清掉一次；幂等，写回后由 onChange 触发落盘。
    if (kindOf(item.value) === 'database' && editor.value?.store) {
      removeObsoleteWorkbenchColumns(editor.value.store)
      // 早期版本的「负责人 / 关联文档 / 来源文档」是上游不存在的 text 类型，
      // 值写进去也渲染不出来；先纠正类型，再补来源列。
      migrateLegacyTextColumns(editor.value.store)
      // 「来源 / 来源文档」是后加的两列：老步骤要在同一时机补齐，否则用户看不到来源。
      ensureWorkbenchSourceColumns(editor.value.store, seedLabels)
      // 识别事项早期写入自动建出的「待办」状态选项：迁回「待开始」并删掉。
      removeLegacyTodoStatusOption(editor.value.store)
    }

    // 模板路径：快照来自内存 override，服务端仍是 version 0，而 BlockSuite 用快照
    // 建 store 不会触发 onChange。必须在这里显式落盘一次，否则用户一刷新内容就丢。
    // 写入失败交给队列进入 error 状态，编辑器保留模板内容，不阻断挂载。
    if (overrideSnapshot) {
      queue.markDirty()
      try {
        await queue.flush()
      } catch {
        /* 状态已由队列回调置为 error，视图会显示「未保存/重试」 */
      }
    }

    ready.value = true
  }

  async function mount(mountOptions: MountOptions = {}): Promise<void> {
    await mountWith(undefined, mountOptions)
  }

  /**
   * 用模板快照挂载。
   *
   * 服务端版本必须是 0 且没有内容，否则抛错让视图重载：
   * 绝不把模板合进可能已有用户输入的 Y.Doc（§9.16.2 规则 1、2）。
   */
  async function mountFromTemplate(tpl: TemplateMountOptions): Promise<void> {
    if (!host.value || !item.value) return
    // 先确认目标仍是空文档：并发场景下另一个标签页可能已经写入。
    const content = await getWorkbenchContent(itemId.value)
    if (content.snapshot_version > 0 || (content.ydoc ?? '') !== '') {
      throw new Error('template-target-not-empty')
    }
    await mountWith(tpl.snapshot, {}, tpl.editorSchemaVersion)
  }

  /** 丢弃本地改动，按服务端最新快照重建；mountWith 会销毁旧编辑器与队列。 */
  async function reload(): Promise<void> {
    await mount()
  }

  /**
   * 切换形态（§10.14.5）。
   *
   * 只重建视图层：不 flush、不清 dirty、不改 saveState，也不产生版本行。
   * 同形态调用直接返回 false，避免无意义地重建 DOM。
   */
  function setMode(next: EditorMode): boolean {
    if (mode.value === next) return false
    mode.value = next
    editor.value?.setMode(next)
    return true
  }

  async function forceSave(): Promise<void> {
    await queue?.forceSave()
    syncBaseVersion()
  }

  /** 队列在保存成功后会推进内部基线，这里同步一份供归档使用。 */
  function syncBaseVersion(): void {
    if (queue) baseVersion.value = queue.getBaseVersion()
  }

  async function flush(): Promise<void> {
    await queue?.flush()
  }

  /** 冲突与失败都视为“未收敛”；空闲/待保存/保存中/已保存均可安全离开。 */
  function canLeave(): boolean {
    const state = queue?.state() ?? saveState.value
    return state !== 'conflict' && state !== 'error'
  }

  function markdown(): string {
    return projectMarkdown(editor.value?.store).markdown
  }

  function markdownWithImages(): string {
    return projectMarkdown(editor.value?.store, { includeImages: true }).markdown
  }

  /** 字数按非空白字符计：中文场景下比“按空格分词”更接近直觉。 */
  function markdownCount(): number {
    return markdown().replace(/\s+/g, '').length
  }

  function destroy(): void {
    reportAssetRefs()
    queue?.dispose()
    queue = null
    editor.value?.destroy()
    editor.value = null
    ready.value = false
    canvasAssets = null
  }

  /**
   * 上报本次会话解析到的资产，刷新服务端 last_seen_at（P2-3）。
   *
   * 服务端不解析 Y.Doc，只能靠客户端上报“这份文档还在用哪些文件”；
   * 不报就会被 30 天保留期回收，届时打开文档会看到破图。
   * 失败静默：它只是保留策略的输入，不影响保存与编辑。
   */
  function reportAssetRefs(): void {
    if (seenAssets.size === 0) return
    const ids = [...seenAssets]
    seenAssets.clear()
    void touchWorkbenchAssets(itemId.value, ids).catch(() => {
      /* 引用上报失败不影响任何用户可见流程 */
    })
  }

  return {
    ready,
    saveState,
    savedAt,
    editor,
    baseVersion,
    schemaVersion: editorSchemaVersion,
    mode,
    mount,
    mountFromTemplate,
    setMode,
    reload,
    forceSave,
    flush,
    canLeave,
    markdown,
    markdownWithImages,
    markdownCount,
    /** 上传新文件并返回 assetId（导入资料等显式插入场景）；编辑器内的粘贴/拖拽由同一 resolver 接管。 */
    uploadAsset: (file: File) => ensureCanvasAssets().put(file),
    /** 导出包按 asset id 取 Blob：与渲染共用 resolver 与缓存，不重复下载。 */
    resolveAsset: async (assetId: string) => (await ensureCanvasAssets().resolve(assetId)) ?? null,
    /** 主动上报引用（保存成功后调用，避免只在关闭时上报）。 */
    reportAssetRefs,
    destroy,
  }
}

/** base64 → 二进制：服务端只存字节，浏览器侧负责转换。 */
export function base64ToBytes(text: string): Uint8Array {
  const binary = atob(text)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i)
  return bytes
}

/** 二进制 → base64；分块避免超长参数列表导致栈溢出。 */
export function bytesToBase64(bytes: Uint8Array): string {
  let binary = ''
  const chunk = 0x8000
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunk))
  }
  return btoa(binary)
}
