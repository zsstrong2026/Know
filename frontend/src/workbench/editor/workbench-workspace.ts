/**
 * 工作台专用的 Workspace 实现。
 *
 * 为什么需要它：vendored BlockSuite 里唯一满足 `Workspace` 接口的实现是
 * `@blocksuite/store/test` 的 `TestWorkspace`，而它的源码注释明确写着
 * "Test only, Do not use this in production"，且把 block 树藏在
 * `rootDoc.getMap('spaces')` 的 subdoc 里。工作台的真实持久化在服务端
 * （`workbench_items.ydoc` 快照），前端只需要一个内存 Workspace，因此这里
 * 把所需语义显式实现，去掉对 test-only 入口的依赖。
 *
 * 与 TestWorkspace 的行为差异（都是刻意为之）：
 *   - 每个条目的 block 树直接挂在自己的 Y.Doc 上，不再嵌一层 subdoc，
 *     于是「快照边界 == 条目边界」，`Y.applyUpdate` / `encodeStateAsUpdate`
 *     的目标一目了然；
 *   - 不创建 DocEngine / AwarenessEngine：工作台的保存链路是
 *     「Y.Doc 快照 → HTTP」，不需要 BlockSuite 自己的同步往返；
 *   - 元数据仍写在 `meta` 这个 Y.Map 上，保证上游 root block 的标题联动不变。
 *
 * 类型说明：`src/types/blocksuite-shim.d.ts` 把所有 `@blocksuite/*` 声明成
 * namespace，因此这里与 blocksuite.ts 一致，用宽松别名承接内部标注，
 * 对外导出的 `WorkbenchWorkspace` 仍带清晰的方法签名。
 */
import { BlockSuiteError, ErrorCode } from '@blocksuite/global/exceptions'
import { NoopLogger } from '@blocksuite/global/utils'
import { BlobEngine, MemoryBlobSource } from '@blocksuite/sync'
import { AwarenessStore, StoreContainer, createYProxy } from '@blocksuite/store'
import { Subject } from 'rxjs'
import { Awareness } from 'y-protocols/awareness.js'
import * as Y from 'yjs'

/** 上游类型在 TS6 下无法被检查（见 blocksuite-shim.d.ts），用宽松别名承接。 */
type BsDoc = any
type BsStore = any
type BsBlobSource = any
type BsExtension = any
type BsIdGenerator = () => string
type BsWorkspaceMeta = any
type BsDocMeta = any

/** 建 Workspace 的可选项。 */
export interface WorkbenchWorkspaceOptions {
  id?: string
  idGenerator?: BsIdGenerator
  /** 资源解析与落盘入口；缺省用内存实现（刷新后从服务端重新取） */
  blobSources?: { main: BsBlobSource }
  /** 新文件上传入口（粘贴/拖拽）；缺省沿用上游的内容哈希 key（仅适合 spike） */
  uploadAsset?: (file: File) => Promise<string>
}

/** 默认 id 生成器：只要求全局唯一，不要求可读。 */
function defaultIdGenerator(): string {
  return `wb-${Math.random().toString(36).slice(2, 10)}${Date.now().toString(36)}`
}

/**
 * 条目元数据。
 *
 * 只实现上游真正会读的字段：`pages` 里的 docMeta 决定 root block 的标题，
 * 三个 Subject 用于让上游感知文档增删。
 */
class WorkbenchMeta {
  docMetaAdded = new Subject<string>()
  docMetaRemoved = new Subject<string>()
  docMetaUpdated = new Subject<void>()

  readonly id = 'meta'

  private readonly _yMap: Y.Map<any>
  private readonly _proxy: any
  private _prevDocs = new Set<string>()

  constructor(readonly doc: Y.Doc) {
    this._yMap = doc.getMap(this.id)
    // createYProxy 让 Y.Map 的读写表现得像普通对象，上游 meta 语义依赖它。
    this._proxy = createYProxy(this._yMap)
    this._yMap.observeDeep(() => this._sync())
  }

  get docMetas(): BsDocMeta[] {
    return (this._proxy.pages ?? []) as BsDocMeta[]
  }

  get properties(): any {
    return this._proxy.properties ?? { tags: { options: [] } }
  }

  get docs(): unknown[] | undefined {
    return this._proxy.pages
  }

  initialize(): void {
    if (!this._proxy.pages) this._proxy.pages = []
  }

  addDocMeta(meta: BsDocMeta, index?: number): void {
    this.doc.transact(() => {
      const pages: BsDocMeta[] = (this._proxy.pages ??= [])
      if (index === undefined) pages.push(meta)
      else pages.splice(index, 0, meta)
    }, this.doc.clientID)
  }

  getDocMeta(id: string): BsDocMeta | undefined {
    return this.docMetas.find(row => row.id === id)
  }

  setDocMeta(id: string, props: Record<string, unknown>): void {
    this.doc.transact(() => {
      const row = (this._proxy.pages ?? []).find((item: BsDocMeta) => item.id === id)
      if (row) Object.assign(row, props)
    }, this.doc.clientID)
  }

  removeDocMeta(id: string): void {
    const pages = this._proxy.pages as BsDocMeta[] | undefined
    if (!pages) return
    const index = pages.findIndex(item => item.id === id)
    if (index < 0) return
    this.doc.transact(() => pages.splice(index, 1), this.doc.clientID)
  }

  setProperties(meta: any): void {
    this._proxy.properties = meta
    this.docMetaUpdated.next()
  }

  /** 把 Y.Map 的变更翻译成三个 Subject，供上游订阅。 */
  private _sync(): void {
    const next = new Set<string>()
    for (const row of this.docMetas) {
      if (!this._prevDocs.has(row.id)) this.docMetaAdded.next(row.id)
      next.add(row.id)
    }
    for (const id of this._prevDocs) {
      if (!next.has(id)) this.docMetaRemoved.next(id)
    }
    this._prevDocs = next
    this.docMetaUpdated.next()
  }
}

/**
 * 单个条目的 Doc。
 *
 * block 树直接挂在 `rootDoc` 的 `blocks` Y.Map 上（Store 内部读的就是
 * `doc.yBlocks`），因此 `spaceDoc === rootDoc`，不再有 subdoc 层。
 */
class WorkbenchDoc implements BsDoc {
  private readonly _storeContainer: any
  private _ready = false

  constructor(
    readonly id: string,
    private readonly _workspace: WorkbenchWorkspace,
    readonly rootDoc: Y.Doc,
    readonly awarenessStore: any,
  ) {
    this._storeContainer = new StoreContainer(this)
  }

  get meta(): BsDocMeta | undefined {
    return this.workspace.meta.getDocMeta(this.id)
  }

  get workspace(): WorkbenchWorkspace {
    return this._workspace
  }

  /** block 树所在的 Y.Doc；快照读写都以它为准。 */
  get spaceDoc(): Y.Doc {
    return this.rootDoc
  }

  get yBlocks(): Y.Map<any> {
    return this.rootDoc.getMap('blocks')
  }

  get ready(): boolean {
    return this._ready
  }

  get loaded(): boolean {
    return this._ready
  }

  load(initFn?: () => void): this {
    if (this._ready) return this
    initFn?.()
    this._ready = true
    return this
  }

  getStore(options: Record<string, any> = {}): BsStore {
    // storeExtensions 是 Workspace 的额外契约（上游 TestDoc 也这样取），
    // 必须在每次取 store 时并入，否则自定义块 schema 不会注册。
    const extensions: BsExtension[] = [
      ...this._workspace.storeExtensions,
      ...(options.extensions ?? []),
    ]
    return this._storeContainer.getStore({
      ...options,
      id: options.id ?? this.id,
      extensions,
    })
  }

  removeStore(options: Record<string, any>): void {
    this._storeContainer.removeStore(options)
  }

  clear(): void {
    this.yBlocks.clear()
  }

  dispose(): void {
    this.clear()
    this._ready = false
  }

  remove(): void {
    this.dispose()
  }
}

/**
 * 造 BlobEngine，并接管「粘贴/拖拽导入」的 key。
 *
 * 上游所有文件导入入口都只调用 `blobSync.set(file)`，BlobEngine 会用文件内容的
 * sha256 当 key，并把该 key 写进 image/attachment 块的 sourceId；而服务端资产的
 * id 是 uuid，刷新后 resolver 拿哈希去下载只会 404（画布显示 “Image not found”）。
 *
 * 因此只拦截「调用方没给 key」的写入：先上传换成 assetId，再用显式 key 写入，
 * 这样 Y.Doc 里保存的 sourceId 就是下次打开仍能解析的资产 id。
 * 显式 key 的调用（上游导入包等）保持原语义，不重复上传。
 */
function createBlobEngine(
  main: BsBlobSource,
  uploadAsset?: (file: File) => Promise<string>,
): any {
  const engine: any = new BlobEngine(main, [], new NoopLogger())
  if (!uploadAsset) return engine
  const set = engine.set.bind(engine)
  engine.set = async (valueOrKey: string | Blob, value?: Blob) => {
    if (typeof valueOrKey === 'string') return set(valueOrKey, value)
    const blob = valueOrKey
    // 剪贴板图片通常就是 File；其他 Blob 补一个名字，服务端需要落库文件名。
    const file =
      blob instanceof File
        ? blob
        : new File([blob], `asset-${Date.now()}`, { type: blob.type || 'application/octet-stream' })
    const assetId = await uploadAsset(file)
    return set(assetId, file)
  }
  return engine
}

/** 工作台 Workspace：内存承载 Doc，资源经 blobSources 解析。 */
export class WorkbenchWorkspace {
  readonly id: string
  readonly idGenerator: BsIdGenerator
  readonly blobSync: any
  readonly awarenessStore: any
  readonly doc: Y.Doc
  readonly meta: WorkbenchMeta

  /** Store 扩展由调用方注入（BlockSuite 的 storeExtensions 契约）。 */
  storeExtensions: BsExtension[] = []

  slots = { docListUpdated: new Subject<void>() }

  private readonly _docs = new Map<string, WorkbenchDoc>()

  constructor(options: WorkbenchWorkspaceOptions = {}) {
    this.id = options.id ?? ''
    this.idGenerator = options.idGenerator ?? defaultIdGenerator
    this.doc = new Y.Doc({ guid: this.id })
    this.awarenessStore = new AwarenessStore(new Awareness(this.doc))
    this.blobSync = createBlobEngine(
      options.blobSources?.main ?? new MemoryBlobSource(),
      options.uploadAsset,
    )
    this.meta = new WorkbenchMeta(this.doc)

    // 元数据是文档集合的唯一真相：createDoc 只写 meta，Doc 由订阅建出。
    this.meta.docMetaAdded.subscribe(docId => this._openDoc(docId))
    this.meta.docMetaRemoved.subscribe(docId => {
      this._docs.get(docId)?.dispose()
      this._docs.delete(docId)
    })
    this.meta.docMetaUpdated.subscribe(() => this.slots.docListUpdated.next())
  }

  get docs(): Map<string, BsDoc> {
    return this._docs as unknown as Map<string, BsDoc>
  }

  createDoc(docId?: string): BsDoc {
    const id = docId ?? this.idGenerator()
    if (this._docs.has(id)) {
      throw new BlockSuiteError(ErrorCode.DocCollectionError, 'doc already exists')
    }
    this.meta.addDocMeta({ id, title: '', tags: [], createDate: Date.now() })
    const doc = this._docs.get(id)
    if (!doc) {
      throw new BlockSuiteError(ErrorCode.DocCollectionError, `doc meta not created: ${id}`)
    }
    return doc
  }

  getDoc(docId: string): BsDoc | null {
    return this._docs.get(docId) ?? null
  }

  removeDoc(docId: string): void {
    this._docs.get(docId)?.dispose()
    this._docs.delete(docId)
    this.meta.removeDocMeta(docId)
  }

  dispose(): void {
    for (const doc of this._docs.values()) doc.dispose()
    this._docs.clear()
    this.blobSync.stop()
    this.awarenessStore.destroy()
  }

  private _openDoc(docId: string): void {
    if (this._docs.has(docId)) return
    // 一个条目一个 Y.Doc：快照边界与条目边界一致，恢复时不会串内容。
    this._docs.set(
      docId,
      new WorkbenchDoc(docId, this, new Y.Doc({ guid: docId }), this.awarenessStore),
    )
  }
}
