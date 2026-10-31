/**
 * 工作台偏好的一次性装载（编辑器 + AI 共用）。
 *
 * 放在一个 composable 里，是因为设置页与工作台外壳都要读它；
 * 让两边各自读 localStorage 会出现「设置页刚改完、外壳还是旧值」的错位。
 */
import { computed, ref } from 'vue'
import {
  DEFAULT_EDITOR_PREFS,
  editorPrefsKey,
  editorPrefsStyle,
  loadEditorPrefs,
  parseEditorPrefs,
  saveEditorPrefs,
  type EditorPrefs,
} from '@/workbench/editor/prefs'
import {
  DEFAULT_AI_PREFS,
  loadPrefs as loadAIPrefs,
  parsePrefs as parseAIPrefs,
  prefsKey as aiPrefsKey,
  savePrefs as saveAIPrefs,
  type WorkbenchAIPrefs,
} from '@/workbench/ai/prefs'

/**
 * 当前登录主体；缺任一维时偏好不落盘（避免跨账号串用）。
 *
 * 注意不能读 `weknora_user_id`：这个键全仓没有写入点。
 * 用户 id 在 `weknora_user` 的 JSON 里；租户优先取显式选中的
 * `weknora_selected_tenant_id`，没有（例如 home 租户）时回退用户自身的 tenant_id。
 */
function currentScope(): { tenantId: string; userId: string } {
  try {
    const rawUser = localStorage.getItem('weknora_user')
    const user = rawUser ? (JSON.parse(rawUser) as { id?: string; tenant_id?: string }) : null
    const userId = String(user?.id ?? '')
    const tenantId = String(
      localStorage.getItem('weknora_selected_tenant_id') ?? user?.tenant_id ?? '',
    )
    return { tenantId, userId }
  } catch {
    return { tenantId: '', userId: '' }
  }
}

/** 拿 storage；Node / SSR 等没有 localStorage 的环境返回 null，偏好走默认值。 */
function storageOrNull(): Storage | null {
  try {
    return typeof localStorage === 'undefined' ? null : localStorage
  } catch {
    return null
  }
}

/** 模块级单例：整个应用共用一份，改一处处处生效。 */
const editorPrefs = ref<EditorPrefs>({ ...DEFAULT_EDITOR_PREFS })
const aiPrefs = ref<WorkbenchAIPrefs>({ ...DEFAULT_AI_PREFS })
/** 上次装载时的主体；登录 / 切换空间后要按新主体重读，否则会沿用上一个账号的偏好。 */
let loadedScope = ''

function scopeKey(tenantId: string, userId: string): string {
  return `${tenantId}:${userId}`
}

function ensureLoaded() {
  const { tenantId, userId } = currentScope()
  const key = scopeKey(tenantId, userId)
  if (loadedScope === key) return
  loadedScope = key
  const storage = storageOrNull()
  editorPrefs.value = loadEditorPrefs(storage, editorPrefsKey(tenantId, userId))
  aiPrefs.value = loadAIPrefs(storage, aiPrefsKey(tenantId, userId))
}

/** 编辑器偏好的 CSS 变量；挂在工作台根节点上，不遍历已有块。 */
const editorStyle = computed(() => editorPrefsStyle(editorPrefs.value))

export function useWorkbenchPrefs() {
  ensureLoaded()
  const { tenantId, userId } = currentScope()

  function updateEditorPrefs(patch: Partial<EditorPrefs>) {
    // 落盘前统一走一遍解析：越界数值钳制、非法枚举回默认（§9.14.2）。
    editorPrefs.value = parseEditorPrefs({ ...editorPrefs.value, ...patch })
    saveEditorPrefs(storageOrNull(), editorPrefsKey(tenantId, userId), editorPrefs.value)
  }

  function resetEditorPrefs() {
    editorPrefs.value = { ...DEFAULT_EDITOR_PREFS }
    saveEditorPrefs(storageOrNull(), editorPrefsKey(tenantId, userId), editorPrefs.value)
  }

  function updateAIPrefs(patch: Partial<WorkbenchAIPrefs>) {
    aiPrefs.value = { ...aiPrefs.value, ...patch }
    saveAIPrefs(storageOrNull(), aiPrefsKey(tenantId, userId), aiPrefs.value)
  }

  function resetAIPrefs() {
    aiPrefs.value = { ...DEFAULT_AI_PREFS }
    saveAIPrefs(storageOrNull(), aiPrefsKey(tenantId, userId), aiPrefs.value)
  }

  return {
    editorPrefs,
    aiPrefs,
    editorStyle,
    updateEditorPrefs,
    resetEditorPrefs,
    updateAIPrefs,
    resetAIPrefs,
  }
}

/** 供非组件场景（例如创建条目前的标题推导）同步读取当前偏好。 */
export function currentEditorPrefs(): EditorPrefs {
  ensureLoaded()
  return editorPrefs.value
}

/** 测试与账号切换时重置单例，避免旧账号的偏好残留。 */
export function __resetPrefsForTest(raw?: unknown) {
  loadedScope = ''
  editorPrefs.value = raw ? (raw as EditorPrefs) : { ...DEFAULT_EDITOR_PREFS }
  aiPrefs.value = { ...DEFAULT_AI_PREFS }
}

export { parseAIPrefs }
