/**
 * 保存队列（§9.7）。
 *
 * 与 P0 spike 的 autosave 不同，这里必须处理服务端乐观锁：
 *   - 每次保存都带上「已知的 snapshot_version」作为 base_version；
 *   - 2401 冲突时不自动覆盖，把冲突交给 UI 让用户三选一；
 *   - 成功后推进 base_version，并把后续变更合并成一次保存。
 *
 * 纯逻辑，不依赖 Vue 与 DOM，方便单测。
 */
import { WORKBENCH_ERROR, workbenchConflictVersion, workbenchErrorCode } from './error-code'
import type { SaveWorkbenchContentResult } from '@/types/workbench'

/** 编辑后 2 秒写一次；与 P0 spike 的 AUTOSAVE_DELAY_MS 保持一致。 */
export const SAVE_DELAY_MS = 2000

export type SaveState = 'idle' | 'pending' | 'saving' | 'saved' | 'error' | 'conflict'

export interface SavePayload {
  /** base64 快照 */
  ydoc: string
  textContent: string
  editorSchemaVersion: number
}

export interface SaveQueueOptions {
  itemId: string
  /** 由调用方生成并持久化的标签页标识，用于服务端日志与排查 */
  clientId: string
  /** 初始基线版本，来自条目元数据 */
  baseVersion: number
  /** 取当前快照；每次真正落盘前调用 */
  read: () => SavePayload
  /** 状态变化回调，供 UI 渲染“已保存/保存中/冲突” */
  onState?: (state: SaveState, detail?: { error?: string; currentVersion?: number }) => void
  /** 注入保存函数，便于测试；默认由调用方在构造时传入真实接口 */
  save: SaveFunction
  delayMs?: number
  /**
   * 空文档延迟首次保存（§9.16.2）。
   *
   * 只给模板流程的「从空白文档开始」用：挂载时的空白快照不落盘，
   * 直到用户真的编辑。否则点一下就会产生 version 1，
   * 工具栏的「使用模板」立刻消失，用户没有反悔机会。
   */
  deferBlankCommit?: boolean
}

/** 保存函数签名：与 api/workbench.ts 的 saveWorkbenchContent 完全一致。 */
export type SaveFunction = (
  id: string,
  payload: {
    base_version: number
    editor_schema_version: number
    ydoc: string
    text_content: string
    client_id: string
    force?: boolean
  },
) => Promise<SaveWorkbenchContentResult>

export interface SaveQueue {
  /** 每次编辑器变更调用；内部防抖 */
  markDirty(): void
  /** 立即保存（失焦 / 离开页面），跳过防抖 */
  flush(): Promise<void>
  /** 冲突后由用户选择“用我的版本覆盖” */
  forceSave(): Promise<void>
  /** 冲突后由用户选择“重载服务端版本”，放弃本地改动 */
  acceptRemote(): void
  /** 服务端版本被外部改变后同步基线（例如重载成功后） */
  setBaseVersion(version: number): void
  /** 当前基线版本：归档提交时作为 base_version */
  getBaseVersion(): number
  state(): SaveState
  dispose(): void
}

export function createSaveQueue(options: SaveQueueOptions): SaveQueue {
  const {
    itemId,
    clientId,
    read,
    onState,
    save,
    delayMs = SAVE_DELAY_MS,
  } = options

  let baseVersion = options.baseVersion
  let state: SaveState = 'idle'
  let timer: ReturnType<typeof setTimeout> | null = null
  let pending = false
  let inFlight: Promise<void> | null = null
  let disposed = false
  // 延迟首次保存：只有用户真正编辑过（markDirty 被调用）才解除。
  let blankCommitDeferred = options.deferBlankCommit === true

  const setState = (next: SaveState, detail?: { error?: string; currentVersion?: number }) => {
    state = next
    onState?.(next, detail)
  }

  const write = async (force = false): Promise<void> => {
    if (disposed) return
    // 已有请求在飞：直接复用，避免同一快照被写两次。
    if (inFlight) return inFlight
    pending = false
    const payload = read()
    setState('saving')
    inFlight = (async () => {
      try {
        const result = await save(itemId, {
          base_version: baseVersion,
          editor_schema_version: payload.editorSchemaVersion,
          ydoc: payload.ydoc,
          text_content: payload.textContent,
          client_id: clientId,
          force,
        })
        baseVersion = result.snapshot_version
        setState('saved')
      } catch (error) {
        const code = workbenchErrorCode(error)
        if (code === WORKBENCH_ERROR.versionConflict) {
          // 冲突不自动重试：交给 UI 让用户选“覆盖/重载/另存副本”。
          setState('conflict', { currentVersion: workbenchConflictVersion(error) ?? undefined })
          return
        }
        setState('error', { error: String(error) })
      } finally {
        inFlight = null
      }
    })()
    return inFlight
  }

  return {
    markDirty() {
      if (disposed) return
      // 用户真的改了内容：解除延迟，后续变更按正常防抖落盘。
      blankCommitDeferred = false
      if (timer) clearTimeout(timer)
      pending = true
      setState('pending')
      timer = setTimeout(() => {
        timer = null
        void write()
      }, delayMs)
    },

    async flush() {
      if (timer) {
        clearTimeout(timer)
        timer = null
      }
      // 延迟期间没被编辑过：flush 也不写，避免离开页面时凭空产生 version 1。
      if (blankCommitDeferred) return
      if (pending) await write()
    },

    async forceSave() {
      // 强制覆盖：服务端跳过 base_version 校验，成功后把基线推到新版本。
      if (timer) {
        clearTimeout(timer)
        timer = null
      }
      await write(true)
    },

    acceptRemote() {
      // 放弃本地改动：清掉待写标记，状态回到已保存，基线由调用方重载后 setBaseVersion。
      if (timer) {
        clearTimeout(timer)
        timer = null
      }
      pending = false
      setState('saved')
    },

    setBaseVersion(version: number) {
      baseVersion = version
    },

    getBaseVersion: () => baseVersion,

    state: () => state,

    dispose() {
      disposed = true
      if (timer) clearTimeout(timer)
      timer = null
    },
  }
}

/** 生成并复用本标签页的 client_id（sessionStorage，刷新后仍是同一个标签页）。 */
export function currentClientId(): string {
  const key = 'workbench_client_id'
  const existing = sessionStorage.getItem(key)
  if (existing) return existing
  const created = `tab-${Math.random().toString(36).slice(2)}-${Date.now().toString(36)}`
  sessionStorage.setItem(key, created)
  return created
}
