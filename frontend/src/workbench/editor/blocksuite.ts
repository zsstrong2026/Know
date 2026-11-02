/**
 * BlockSuite 最小挂载模块（P0-2 spike，P0-3 扩展注入点，P0-5 双根切换）。
 *
 * 链路：workspace(内存) → store(空白 page/surface/note/paragraph) → BlockStdScope → EditorHost
 * P0-3 只增加「可注入」的三个钩子，不把业务内容硬编码进来：
 *   - seed(ctx)     初始化内容（空白 doc 之后、readonly 之前执行）
 *   - assets        自定义资源解析器，落到 blobSync 的 BlobSource 上
 *   - onChange      文档变更通知（供自动保存防抖使用）
 *
 * P0-5 增加路线 2 的形态切换（handle.setMode）。上游 `BlockStdScope` 在构造时就用
 * extension 数组绑定了「哪种 root 视图」（page → affine-page-root，edgeless →
 * affine-edgeless-root，见 affine/blocks/root/src/view.ts），没有运行时改 mode 的入口，
 * 因此切换 root 必须重建 `BlockStdScope` + `EditorHost`；但 store / doc / workspace 全部复用，
 * 且切换过程完全不触碰 Y.Doc（不 encodeStateAsUpdate、不 applyUpdate）。
 */
import { StoreExtensionManager, ViewExtensionManager } from '@blocksuite/affine/ext-loader'
import { getInternalStoreExtensions } from '@blocksuite/affine/extensions/store'
import { getInternalViewExtensions } from '@blocksuite/affine/extensions/view'
import {
  CommunityCanvasTextFonts,
  EditorSettingExtension,
  FontConfigExtension,
} from '@blocksuite/affine/shared/services'
import { BlockStdScope } from '@blocksuite/affine/std'
import { insertEdgelessTextCommand } from '@blocksuite/affine-gfx-text'
import { TextSelection } from '@blocksuite/std'
import { GfxControllerIdentifier } from '@blocksuite/std/gfx'
import { Text } from '@blocksuite/store'
import { TestWorkspace } from '@blocksuite/store/test'
import { signal } from '@preact/signals-core'
import * as Y from 'yjs'
import { ParagraphViewExtension } from '@blocksuite/affine-block-paragraph/view'
import { localizedParagraphPlaceholder, translateSlashMenuItem } from './slash-menu-locale'
// 斜杠菜单挂在 body 上，必须用全局样式覆盖其浮层背景
import './editor-popover.css'

// 上游类型在 TS6 下无法被检查（见 src/types/blocksuite-shim.d.ts），
// 这里用宽松别名承接内部标注；对外导出的接口（MountEditorOptions / CanvasHandle）仍是精确类型。
type BsExtension = any
type BsStore = any
type BsDoc = any

export type EditorKind = 'canvas' | 'database'
export type EditorMode = 'page' | 'edgeless'

/** seed 的入参：store 已建成，尚未设置 readonly */
export interface EditorSeedContext {
  store: BsStore
  doc: BsDoc
}

/**
 * 编辑器级偏好（§9.14.2）。
 *
 * 这些项被 BlockSuite 内部读取，只能通过 EditorSettingProvider 注入，
 * 不能靠 CSS 变量实现；改动需要重建视图层才生效。
 */
export interface EditorRuntimeSettings {
  /** 滚轮直接缩放（否则滚轮平移、Ctrl/⌘ + 滚轮缩放） */
  edgelessScrollZoom: boolean
  /** 代码块默认显示行号 */
  codeBlockLineNumbers: boolean
}

/** 新建笔记的初始块属性；只在创建路径使用，不追溯已有笔记。 */
export interface NoteStyleSeed {
  /** 底色：CSS 颜色字符串，transparent 表示去底色 */
  background: string
  /** 圆角像素 */
  borderRadius: number
  /** 边框粗细像素 */
  borderSize: number
  /** 边框样式：solid | dashed | none */
  borderStyle: string
  /** 阴影：'' 表示无，其余为 --affine-note-shadow-* 变量名 */
  shadowType: string
}

/**
 * 资源解析器：把 image/attachment 的 sourceId 映射成真实 Blob。
 * 真实实现会是「带鉴权 fetch 后端资产」；spike 用本地生成的 PNG 代替。
 */
export interface AssetResolver {
  resolve(assetId: string): Promise<Blob | null> | Blob | null
  /** 可选：新上传的资产落盘（spike 不做持久化） */
  save?(assetId: string, blob: Blob): Promise<void> | void
}

export interface MountEditorOptions {
  /** 编辑器挂载容器，宿主负责给容器高度 */
  element: HTMLElement
  /** 文档 id，同时作为 workspace id */
  itemId: string
  kind: EditorKind
  mode: EditorMode
  /** Yjs 快照；为空则创建空白文档 */
  snapshot?: Uint8Array | null
  editable?: boolean
  /** 初始化内容回调：只在没有快照、由本函数新建空白 doc 时执行 */
  seed?: (ctx: EditorSeedContext) => void
  /** 编辑器级偏好（滚轮缩放、代码行号）；缺省用 BlockSuite 自己的默认 */
  settings?: EditorRuntimeSettings
  /** 新建笔记的初始样式；缺省用 BlockSuite 默认样式 */
  noteStyle?: NoteStyleSeed
  /** 图片/附件资源解析器；注入到 workspace.blobSync 的 main BlobSource */
  assets?: AssetResolver
  /** 文档发生变更（props / block 增删）时回调，用于自动保存防抖 */
  onChange?: () => void
}

/**
 * 计数器：给 spike 提供「切换不是重放快照」的可观测证据。
 * applyUpdateCalls 只在「把快照写回 Y.Doc」时自增（见 applySnapshot），setMode 不会碰它。
 */
export interface EditorStats {
  /** 把快照写入 Y.Doc 的次数（正常只有首次 mount 恢复时为 1） */
  applyUpdateCalls: number
  /** BlockStdScope + EditorHost 的创建次数（首次挂载 + 每次 setMode 各 1） */
  viewMounts: number
  /** 视图卸载次数 */
  viewUnmounts: number
  /** setMode 生效次数（同 mode 重复调用不计数） */
  modeSwitches: number
}

export interface CanvasHandle {
  destroy(): void
  getSnapshot(): Uint8Array
  /** 在 page / edgeless 之间切换 root；复用同一 store/doc，不写 Y.Doc */
  setMode(mode: EditorMode): void
  getMode(): EditorMode
  stats(): EditorStats
  /**
   * 当前选区列表。
   *
   * BlockSuite 把 selection 挂在 std（视图作用域）而不是 store 上，
   * 且 setMode 会重建 std，因此必须由本模块统一转发当前那一份，
   * 上层不要自己去 store 上找 selection。
   */
  getSelections(): unknown[]
  /**
   * 就地更新编辑器级偏好（滚轮缩放、代码行号）。
   *
   * 只改 setting signal，不重建视图、不写 Y.Doc；BlockSuite 内部通过 effect 响应。
   */
  updateSettings(settings: EditorRuntimeSettings): void
  /**
   * 就地更新「新建笔记样式」。
   *
   * 只影响之后新建的笔记；已有笔记不会被追溯修改，也不会写旧块。
   */
  updateNoteStyle(style: NoteStyleSeed): void
  /** 供上层读取 block model / 做投影，不参与渲染 */
  store: BsStore
  doc: BsDoc
  workspace: any
}

/**
 * 模板快照里含图片/附件块时，套用前把它们替换成文本占位（§9.16.3）。
 *
 * 模板不复制资产文件，直接沿用引用会得到一堆破图；这里在**解码后的 Y.Doc 副本**上
 * 改块，不碰原快照字节，也不影响模板本身。
 * 返回替换掉的块数，用于日志与「图片未包含在模板中」的提示判断。
 */
export const TEMPLATE_ASSET_PLACEHOLDER = '图片未包含在模板中，请重新插入'

export function replaceAssetBlocks(snapshot: Uint8Array): { bytes: Uint8Array; replaced: number } {
  const tmp = new Y.Doc()
  Y.applyUpdate(tmp, snapshot)
  // 块结构是 Y.Map：`sys:flavour` 存 flavour，`prop:*` 存各 props。
  const blocks = tmp.getMap<Y.Map<unknown>>('blocks')
  let replaced = 0
  tmp.transact(() => {
    blocks.forEach((block) => {
      if (!(block instanceof Y.Map)) return
      const flavour = block.get('sys:flavour')
      if (typeof flavour !== 'string' || !TEMPLATE_ASSET_FLAVOURS.includes(flavour)) return

      // 清掉原有 props（资产 id / 尺寸等），换成普通段落占位。
      const propKeys: string[] = []
      block.forEach((_value, key) => {
        if (key.startsWith('prop:')) propKeys.push(key)
      })
      propKeys.forEach((key) => block.delete(key))

      block.set('sys:flavour', 'affine:paragraph')
      block.set('prop:type', 'text')
      block.set('prop:text', new Text(TEMPLATE_ASSET_PLACEHOLDER))
      replaced += 1
    })
  })
  const bytes = Y.encodeStateAsUpdate(tmp)
  tmp.destroy()
  return { bytes, replaced }
}

/** 模板里需要替换成文本占位的资产类块。 */
const TEMPLATE_ASSET_FLAVOURS = [
  'affine:image',
  'affine:attachment',
  'affine:embed',
  'affine:embed-html',
  'affine:embed-youtube',
  'affine:embed-loom',
  'affine:embed-figma',
  'affine:embed-github',
  'affine:embed-synced-doc',
  'affine:embed-linked-doc',
]

// 模块级单例：管理器与注册状态只初始化一次，保证 ensureBlockSuiteElements 幂等
const storeManager = new StoreExtensionManager(getInternalStoreExtensions())
const viewManager = new ViewExtensionManager(getInternalViewExtensions())
let specsByMode: Record<EditorMode, BsExtension[]> | null = null
let storeExtensions: BsExtension[] | null = null

/**
 * 幂等注册 BlockSuite 所需的自定义元素与 specs。
 *
 * 上游 ViewExtensionProvider 自带 `static effectRan` 守卫，首次 setup 时才执行
 * effect() 里的 customElements.define；这里只需要保证 manager.get() 只调一次。
 */
export function ensureBlockSuiteElements(): void {
  if (specsByMode) return
  // 空段落占位跟随应用语言（"Type '/' for commands" 在中文环境显示为中文）
  viewManager.configure(ParagraphViewExtension, {
    getPlaceholder: localizedParagraphPlaceholder,
  })
  // 两种 mode 都提前 setup：切 mode 时元素已注册，避免运行时抛未定义元素
  specsByMode = {
    page: [...viewManager.get('page'), FontConfigExtension(CommunityCanvasTextFonts)],
    edgeless: [...viewManager.get('edgeless'), FontConfigExtension(CommunityCanvasTextFonts)],
  }
  storeExtensions = storeManager.get('store')
}

/** 空白文档结构：page > (surface, note > paragraph) */
function initBlankDoc(store: BsStore) {
  if (store.root) return
  const rootId = store.addBlock('affine:page', { title: new Text() })
  store.addBlock('affine:surface', {}, rootId)
  const noteId = store.addBlock('affine:note', {}, rootId)
  store.addBlock('affine:paragraph', {}, noteId)
}

/**
 * 不冲突的块 id 生成器。
 *
 * 上游 `createAutoIncrementIdGenerator` 从 "0" 开始自增；恢复快照后 Y.Doc 里
 * 已存在 id 为 "0"/"1"... 的块，新建块会撞上它们并抛
 * `BlockSuiteError: Should not add existing block: 0`。
 *
 * 这里每个 workspace 用一次随机前缀：id 只是字符串键，没有可读性要求，
 * 而前缀能保证「恢复的快照块」与「本次会话新建的块」永不相交。
 */
function createSafeIdGenerator(seed: string): () => string {
  let counter = 0
  return () => `${seed}-${counter++}`
}

/**
 * 用 AssetResolver 造一个 BlobSource，挂到 TestWorkspace 的 blobSync.main。
 *
 * 上游图片渲染链是：ImageBlockComponent.connectedCallback → resourceController.setEngine(std.store.blobSync)
 *   → sourceId$ 触发 refreshData() → blobSync.get(sourceId) → URL.createObjectURL(blob)。
 * 因此只要 blobSync 能按 id 返回 Blob，「刷新后重新挂载」时图片会重新解析。
 */
function createAssetBlobSource(resolve: AssetResolver['resolve'], save?: AssetResolver['save']) {
  const cache = new Map<string, Blob>()
  return {
    name: 'spike-assets',
    readonly: false,
    async get(key: string): Promise<Blob | null> {
      const cached = cache.get(key)
      if (cached) return cached
      const blob = await resolve(key)
      if (blob) cache.set(key, blob)
      return blob ?? null
    },
    async set(key: string, value: Blob): Promise<string> {
      cache.set(key, value)
      await save?.(key, value)
      return key
    },
    async delete(key: string): Promise<void> {
      cache.delete(key)
    },
    async list(): Promise<string[]> {
      return [...cache.keys()]
    },
  }
}

/**
 * 视图层：一个 mode 对应一套 BlockStdScope + EditorHost。
 *
 * 上游在 std 构造时按 extension 决定 root 视图，所以换 root = 换 std；
 * store 由调用方持有，本函数不读写 Y.Doc。
 */
interface AttachedView {
  detach(): void
  /** 该视图作用域（BlockStdScope）上的选区读取入口 */
  selections(): unknown[]
}

/**
 * 把工作台偏好包成 BlockSuite 的 EditorSettingExtension。
 *
 * BlockSuite 内部（代码块行号、Edgeless 滚轮缩放）只从 EditorSettingProvider 读，
 * 因此必须走扩展注入。signal 由 mountEditor 持有并在 setMode 时复用，
 * 这样改偏好只需更新 signal，不必重建视图。
 */
function createSettingExtension(setting$: unknown): BsExtension {
  return EditorSettingExtension({
    setting$,
    set: (key: string, value: unknown) => {
      // signal 的值是不可变对象：整体替换才能触发依赖它的 effect。
      const current = (setting$ as { value: Record<string, unknown> }).value
      ;(setting$ as { value: Record<string, unknown> }).value = { ...current, [key]: value }
    },
  })
}

/**
 * 给斜杠菜单挂上按当前语言翻译的转换器。
 *
 * 菜单组件是 Lit 自定义元素，首帧后才出现在 host 里；
 * 先同步试一次，找不到再按帧重试有限次数（切 mode 时也会重新挂）。
 */
function installSlashMenuLocale(root: HTMLElement): void {
  const apply = (): boolean => {
    const widget = root.querySelector('affine-slash-menu-widget') as
      | { configItemTransform?: (item: any) => any }
      | null
    if (!widget) return false
    widget.configItemTransform = translateSlashMenuItem as (item: any) => any
    return true
  }
  if (apply()) return

  let frames = 0
  const retry = () => {
    if (apply() || frames >= 20) return
    frames += 1
    if (typeof requestAnimationFrame === 'function') requestAnimationFrame(retry)
    else window.setTimeout(retry, 16)
  }
  if (typeof requestAnimationFrame === 'function') requestAnimationFrame(retry)
  else window.setTimeout(retry, 16)
}

/**
 * 斜杠菜单兜底。
 *
 * 上游只监听 beforeInput；在部分输入法/合成输入下该事件不会带出完整
 * 的 "/" 文本，菜单就不会弹。这里再监听一次 input：确认 slash 已经落进
 * 编辑器后，直接调用 widget 的处理入口（和上游走同一条菜单构建链路）。
 */
function installSlashMenuFallback(element: HTMLElement): () => void {
  /** 字符落进编辑器后，再调用上游的菜单构建入口。 */
  const tryOpen = (target: EventTarget | null) => {
    window.setTimeout(() => {
      if (document.querySelector('affine-slash-menu')) return
      const widget = element.querySelector('affine-slash-menu-widget') as
        | {
            _handleInput?: (editor: any, isCompositionEnd: boolean) => void
            _getInlineEditor?: (evt: Event) => any
          }
        | null
      if (!widget?._handleInput) return
      const inlineHost = (target as HTMLElement | null)?.closest?.('.inline-editor') as
        | { inlineEditor?: any }
        | null
      const editor =
        inlineHost?.inlineEditor
        ?? widget._getInlineEditor?.(new InputEvent('beforeInput', { data: '/' }))
      if (!editor) return
      const open = () => {
        const w = widget as any
        try {
          // 上游要求 inlineRange 已跟上真实光标；输入事件后选区同步是异步的，
          // 这里主动同步一次，避免 getTextPoint 读到 "/" 之前的前缀而直接 return。
          w.host?.selection && editor.syncInlineRange?.()
          // 上游还要求 host 上有 TextSelection；焦点异常时它可能缺失，
          // 缺失就直接补一条，保证 _handleInput 能走到菜单构建分支。
          if (!w.host?.selection?.find?.(TextSelection)) {
            const blockId = (target as HTMLElement | null)
              ?.closest?.('[data-block-id]')?.getAttribute('data-block-id')
            const range = editor.getInlineRange?.()
            if (blockId && range) {
              w.host?.selection?.set?.([
                new TextSelection({ from: { blockId, index: range.index, length: 0 }, to: null }),
              ])
            }
          }
          widget._handleInput?.(editor, true)
        } catch (error) {
          console.error('[wb-slash] handleInput failed', error)
        }
      }
      // 上游的菜单构建要求 inlineRange / DOM 文本都已同步，先等一帧渲染再试
      if (typeof editor.waitForUpdate === 'function') {
        editor.waitForUpdate().then(open, open)
      } else {
        open()
      }
    }, 60)
  }

  const onInput = (event: Event) => {
    if ((event as InputEvent).data === '/') tryOpen(event.target)
  }

  const onKeyDown = (event: KeyboardEvent) => {
    if (event.key !== '/') return
    const target = event.target as HTMLElement | null
    if (!target?.closest('.inline-editor, .affine-page-root, .affine-editor-container')) return
    // 编辑器内的 "/" 不交给全局命令面板；字符仍由编辑器自己插入
    event.stopPropagation()
    tryOpen(target)
  }

  element.addEventListener('input', onInput, true)
  element.addEventListener('keydown', onKeyDown)
  return () => {
    element.removeEventListener('input', onInput, true)
    element.removeEventListener('keydown', onKeyDown)
  }
}

/**
 * 无限画布：单击空白处直接创建可编辑文本块。
 *
 * 上游默认只在空白双击时创建（DblClickAddEdgelessText），这里按产品要求
 * 把入口扩展到单击，但复用上游同一个命令，保证块结构和进入编辑态的方式
 * 与 AFFiNE 完全一致：
 *   - 拖动超过阈值（框选 / 平移）不创建
 *   - 点在已有元素、笔记、工具栏上不创建
 *   - 空白处新建的空文本块在失焦时由上游逻辑自动删除
 */
function installEdgelessBlankClickText(std: any, element: HTMLElement): () => void {
  const gfx = std.get(GfxControllerIdentifier)
  let down: { x: number; y: number } | null = null

  const onPointerDown = (event: PointerEvent) => {
    down = event.button === 0 ? { x: event.clientX, y: event.clientY } : null
  }

  const onClick = (event: MouseEvent) => {
    const origin = down
    down = null
    if (!origin) return
    // 框选、平移、轻微抖动都不应该落成文本块
    if (Math.abs(event.clientX - origin.x) > 4 || Math.abs(event.clientY - origin.y) > 4) return

    const target = event.target as HTMLElement | null
    if (
      target?.closest(
        'affine-edgeless-toolbar, affine-edgeless-zoom-toolbar-widget, affine-note, ' +
          'affine-edgeless-text, .inline-editor, button, input, textarea, [role="toolbar"]',
      )
    ) {
      return
    }

    // clientX/clientY 是相对浏览器窗口的坐标，必须先减去画布视口的 left/top；
    // 直接传给 toModelCoord 会多算一段左侧导航 + 顶部偏移，文本块就落到右下很远的位置。
    const [x, y] = gfx.viewport.toModelCoordFromClientCoord([event.clientX, event.clientY])
    // 点在画布已有元素（形状 / 图片等）上时交给元素自己的交互
    if (gfx.getElementByPoint(x, y)) return

    std.command.exec(insertEdgelessTextCommand, { x, y })
  }

  element.addEventListener('pointerdown', onPointerDown, true)
  element.addEventListener('click', onClick, true)
  return () => {
    element.removeEventListener('pointerdown', onPointerDown, true)
    element.removeEventListener('click', onClick, true)
  }
}

function attachView(params: {
  store: BsStore
  mode: EditorMode
  element: HTMLElement
  specs: Record<EditorMode, BsExtension[]>
  setting$: unknown
}): AttachedView {
  const { store, mode, element, specs, setting$ } = params
  const std = new BlockStdScope({
    store,
    extensions: [...specs[mode], createSettingExtension(setting$)],
  })
  const host = std.render()
  host.style.display = 'block'
  host.style.height = '100%'
  // 上游 page/edgeless 视图用 ViewportElementExtension 从 host 的祖先元素上查找
  // '.affine-page-viewport' / '.affine-edgeless-viewport'，找不到会抛 BlockSuiteError
  // （见 affine/shared/src/services/viewport-element-service.ts:24），
  // 因此在挂载容器上补这个类名（detach 时还原），等价于 AFFiNE 里滚动容器的用法。
  const viewportClass = mode === 'page' ? 'affine-page-viewport' : 'affine-edgeless-viewport'
  const hadViewportClass = element.classList.contains(viewportClass)
  element.classList.add(viewportClass)
  element.appendChild(host)
  // 菜单文案跟随当前 i18n 语言（中文环境不再显示英文命令）
  installSlashMenuLocale(element)
  // 无限画布：空白处单击直接落一个可编辑文本块（AFFiNE 的空白双击效果）
  const removeBlankClickText =
    mode === 'edgeless' ? installEdgelessBlankClickText(std, element) : null

  if (mode === 'edgeless') {
    // 页面模式的内容都放在 note 里，切到画布后是一张卡片；
    // 视口默认从 (0,0) 开始，卡片会只露出左上角。这里挂载完成后
    // 适配一次视图，让页面内容以完整卡片的形式出现在画布中央。
    const fit = () => {
      if (!host.isConnected) return
      try {
        (std as any).get?.(GfxControllerIdentifier)?.fitToScreen?.()
      } catch {
        // 空画布或 surface 尚未就绪时忽略；下次挂载会再试
      }
    }
    requestAnimationFrame(() => requestAnimationFrame(fit))
  }

  return {
    detach() {
      removeBlankClickText?.()
      // host.remove() 触发 disconnectedCallback → std.unmount()，释放该 std 自己的订阅
      host.remove()
      if (!hadViewportClass) element.classList.remove(viewportClass)
    },
    selections() {
      // std.selection 是 StoreSelectionExtension；.value 才是选区数组。
      const list = (std as unknown as { selection?: { value?: unknown[] } }).selection?.value
      return Array.isArray(list) ? list : []
    },
  }
}

/** 唯一的快照入口：所有 Y.applyUpdate 都必须走这里，便于计数 */
function applySnapshot(doc: BsDoc, snapshot: Uint8Array, stats: EditorStats): void {
  stats.applyUpdateCalls += 1
  Y.applyUpdate(doc.spaceDoc, snapshot)
}

/**
 * 给「刚被创建」的笔记套上偏好样式（§9.14.2）。
 *
 * 笔记由上游工具栏创建，不经过工作台代码，因此只能在其 add 事件后补写一次
 * 块属性。只在 store.load 之后订阅，所以快照恢复出来的历史笔记不会命中；
 * 样式写进该笔记自身，改偏好也不会追溯。
 */
function styleNewNote(store: BsStore, id: string, style: NoteStyleSeed): void {
  const model = store.getModelById(id)
  if (!model || model.flavour !== 'affine:note') return
  store.transact(() => {
    model.props.background = style.background
    model.props.edgeless = {
      ...model.props.edgeless,
      style: {
        borderRadius: style.borderRadius,
        borderSize: style.borderSize,
        borderStyle: style.borderStyle,
        shadowType: style.shadowType,
      },
    }
  })
}

export async function mountEditor(options: MountEditorOptions): Promise<CanvasHandle> {
  const {
    element,
    itemId,
    mode,
    snapshot,
    editable = true,
    seed,
    settings,
    noteStyle: initialNoteStyle,
    assets,
    onChange,
  } = options
  ensureBlockSuiteElements()
  const specs = specsByMode as Record<EditorMode, BsExtension[]>
  const stats: EditorStats = { applyUpdateCalls: 0, viewMounts: 0, viewUnmounts: 0, modeSwitches: 0 }

  // 编辑器级偏好放在一个稳定的 signal 上：setMode 重建视图时复用它，
  // 改偏好只换值不换引用，BlockSuite 里依赖它的 effect 会自动重跑。
  const setting$ = signal<Record<string, unknown>>({
    edgelessScrollZoom: settings?.edgelessScrollZoom ?? false,
    codeBlockLineNumbers: settings?.codeBlockLineNumbers ?? true,
  })

  // 1. 内存 workspace（P0-2 不接持久化；TestWorkspace 是 vendored 代码里唯一的 Workspace 实现）
  //    id 生成器必须带会话级前缀：恢复快照后 Y.Doc 里已有 "0"/"1"… 的历史块，
  //    纯自增会与之冲突并抛 "Should not add existing block"。
  const workspace = new TestWorkspace({
    id: itemId,
    idGenerator: createSafeIdGenerator(`wb${Math.random().toString(36).slice(2, 8)}`),
    // 只有显式传入 assets 时才替换默认的 MemoryBlobSource
    ...(assets ? { blobSources: { main: createAssetBlobSource(assets.resolve, assets.save) } } : {}),
  })
  workspace.storeExtensions = storeExtensions ?? []
  workspace.meta.initialize()

  const doc = workspace.createDoc(itemId)
  // 2. 有快照先写回 spaceDoc，再 getStore，让 Store 建索引时能读到恢复后的 block
  const hasSnapshot = Boolean(snapshot && snapshot.byteLength > 0)
  if (hasSnapshot && snapshot) {
    applySnapshot(doc, snapshot, stats)
  }
  const store = doc.getStore()

  store.load(() => {
    initBlankDoc(store)
    // 只在新建空白 doc 时注入内容；恢复快照时由快照自身决定内容
    if (!hasSnapshot) seed?.({ store, doc })
  })
  store.readonly = !editable

  // 变更订阅：props 变更与 block 增删都要通知（自动保存防抖的输入）。
  // 订阅挂在 store 上而非视图上，切 mode 不会重复订阅。
  const subscriptions: Array<{ unsubscribe(): void }> = []
  if (onChange) {
    const notify = () => onChange()
    subscriptions.push(store.slots.blockUpdated.subscribe(notify))
    subscriptions.push(store.slots.yBlockUpdated.subscribe(notify))
    // 正文输入只改 Y.Text，不触发上面两个 slot（它们只覆盖 Y.Map props 与 block 增删），
    // 因此必须直接监听 Y.Doc 的 update 事件，否则打字永远不进入保存队列。
    const onYDocUpdate = () => notify()
    doc.spaceDoc.on('update', onYDocUpdate)
    subscriptions.push({ unsubscribe: () => doc.spaceDoc.off('update', onYDocUpdate) })
  }

  // 笔记偏好：可变引用，设置页改完后只影响之后新建的笔记。
  let noteStyle: NoteStyleSeed | null = initialNoteStyle ?? null
  // 只有在 store.load 之后才挂监听：恢复快照产生的 block 不走这条路径，
  // 因此历史笔记永远不会被改写（§9.14.2「不追溯已有内容」）。
  const onNoteAdded = ({ id, isLocal }: { id: string; isLocal: boolean }) => {
    if (!noteStyle || !isLocal) return
    styleNewNote(store, id, noteStyle)
  }
  subscriptions.push(store.slots.blockUpdated.subscribe((payload: {
    type?: string
    id?: string
    isLocal?: boolean
  }) => {
    if (payload?.type === 'add' && payload.id) {
      onNoteAdded({ id: payload.id, isLocal: payload.isLocal !== false })
    }
  }))

  // 3. 渲染：render() 只创建 EditorHost，插入 DOM 后 connectedCallback 才真正 mount
  let currentMode: EditorMode = mode
  let view: AttachedView = attachView({ store, mode, element, specs, setting$ })
  const removeSlashFallback = installSlashMenuFallback(element)
  stats.viewMounts += 1

  return {
    getSnapshot: () => Y.encodeStateAsUpdate(doc.spaceDoc),
    store,
    doc,
    workspace,
    stats: () => ({ ...stats }),
    getMode: () => currentMode,
    // 转调当前视图：切 mode 后 std 被重建，这里始终读最新的那一份。
    getSelections: () => view.selections(),
    updateSettings: (next: EditorRuntimeSettings) => {
      setting$.value = {
        ...setting$.value,
        edgelessScrollZoom: next.edgelessScrollZoom,
        codeBlockLineNumbers: next.codeBlockLineNumbers,
      }
    },
    updateNoteStyle: (next: NoteStyleSeed) => {
      noteStyle = next
    },
    /**
     * 换 root：复用同一 store/doc/workspace，只重建视图层。
     * 不调用 encodeStateAsUpdate / applyUpdate，因此未保存变更天然保留。
     */
    setMode: (next: EditorMode) => {
      if (next === currentMode) return
      view.detach()
      stats.viewUnmounts += 1
      view = attachView({ store, mode: next, element, specs, setting$ })
      stats.viewMounts += 1
      currentMode = next
      stats.modeSwitches += 1
    },
    destroy: () => {
      subscriptions.forEach(sub => sub.unsubscribe())
      removeSlashFallback()
      view.detach()
      stats.viewUnmounts += 1
      store.dispose()
      doc.remove()
      workspace.dispose()
    },
  }
}
