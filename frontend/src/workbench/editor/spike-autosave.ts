/**
 * P0-3 spike 的最小自动保存队列。
 *
 * 契约（对齐方案 §10.4.3 / §10.4.4）：
 *   - 编辑后 2 秒防抖写一次
 *   - 快照先做 8MB 大小检查，超限直接进 error 状态并拒绝保存（不静默截断）
 *   - 默认持久化到 localStorage（base64），可通过 persist 注入真实后端
 */

export const AUTOSAVE_DELAY_MS = 2000
/** 后端契约：Y.Doc 二进制 ≤ 8MB（方案 §10.4.4） */
export const MAX_SNAPSHOT_BYTES = 8 * 1024 * 1024

export type SaveStatus = 'idle' | 'pending' | 'saving' | 'saved' | 'error'

export interface SnapshotRecord {
  base64: string
  bytes: number
  savedAt: number
}

export interface SnapshotPersist {
  load(key: string): Promise<SnapshotRecord | null> | SnapshotRecord | null
  save(key: string, record: SnapshotRecord): Promise<void> | void
  clear(key: string): Promise<void> | void
}

export interface AutosaveOptions {
  key: string
  /** 取当前快照；每次真正落盘前调用一次 */
  getSnapshot: () => Uint8Array
  persist?: SnapshotPersist
  delayMs?: number
  onStatus?: (status: SaveStatus, detail?: { bytes?: number; error?: string }) => void
}

export interface AutosaveHandle {
  /** 每次编辑器变更调用；内部做防抖 */
  markDirty(): void
  /** 立即保存（失焦 / 手动保存），会跳过防抖 */
  flush(): Promise<void>
  stats(): { saves: number; lastBytes: number; status: SaveStatus; error?: string }
  dispose(): void
}

/** Uint8Array → base64，分块避免 apply 参数过长 */
export function toBase64(bytes: Uint8Array): string {
  let binary = ''
  const chunk = 0x8000
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunk))
  }
  return btoa(binary)
}

export function fromBase64(text: string): Uint8Array {
  const binary = atob(text)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i += 1) bytes[i] = binary.charCodeAt(i)
  return bytes
}

/** 默认持久化：localStorage 里存 `{base64, bytes, savedAt}` */
export function createLocalStoragePersist(): SnapshotPersist {
  return {
    load(key) {
      const raw = localStorage.getItem(key)
      if (!raw) return null
      try {
        return JSON.parse(raw) as SnapshotRecord
      } catch {
        return null
      }
    },
    save(key, record) {
      localStorage.setItem(key, JSON.stringify(record))
    },
    clear(key) {
      localStorage.removeItem(key)
    },
  }
}

export function createAutosave(options: AutosaveOptions): AutosaveHandle {
  const { key, getSnapshot, delayMs = AUTOSAVE_DELAY_MS } = options
  const persist = options.persist ?? createLocalStoragePersist()

  let timer: ReturnType<typeof setTimeout> | null = null
  let disposed = false
  let pending = false
  const state = { saves: 0, lastBytes: 0, status: 'idle' as SaveStatus, error: undefined as string | undefined }

  const setStatus = (status: SaveStatus, detail?: { bytes?: number; error?: string }) => {
    state.status = status
    state.error = detail?.error
    options.onStatus?.(status, detail)
  }

  const write = async () => {
    if (disposed) return
    pending = false
    const snapshot = getSnapshot()
    state.lastBytes = snapshot.byteLength
    // 大小检查必须在序列化之前：先拦掉，避免无谓的 base64 膨胀与 localStorage 写入
    if (snapshot.byteLength > MAX_SNAPSHOT_BYTES) {
      setStatus('error', {
        bytes: snapshot.byteLength,
        error: `快照 ${snapshot.byteLength} 字节超过 ${MAX_SNAPSHOT_BYTES} 字节上限，已拒绝保存`,
      })
      return
    }
    setStatus('saving', { bytes: snapshot.byteLength })
    try {
      await persist.save(key, {
        base64: toBase64(snapshot),
        bytes: snapshot.byteLength,
        savedAt: Date.now(),
      })
      state.saves += 1
      setStatus('saved', { bytes: snapshot.byteLength })
    } catch (error) {
      setStatus('error', { bytes: snapshot.byteLength, error: String(error) })
    }
  }

  const schedule = () => {
    if (disposed) return
    if (timer) clearTimeout(timer)
    pending = true
    setStatus('pending')
    timer = setTimeout(() => {
      timer = null
      void write()
    }, delayMs)
  }

  return {
    markDirty: schedule,
    async flush() {
      if (timer) {
        clearTimeout(timer)
        timer = null
      }
      if (pending) await write()
    },
    stats: () => ({ ...state }),
    dispose() {
      disposed = true
      if (timer) clearTimeout(timer)
      timer = null
    },
  }
}
