/**
 * 工作台 AI 偏好（§9.14.5、§9.14.6）。
 *
 * P1 只存 localStorage，键为 `know.workbench.ai-prefs.v1:<tenantId>:<userId>`；
 * 与编辑器偏好分开存，任一损坏不影响另一个。读取时逐项校验类型与白名单，
 * 非法值一律回默认，绝不因为一条坏数据让整个工作台打不开。
 */

export interface WorkbenchAIPrefs {
  /** 是否启用工作台 AI（侧栏、内联、画布共用） */
  enabled: boolean
  /** 默认智能体 id；空表示用租户默认模型 */
  defaultAgentId: string
  /** 回答是否显示来源引用（只影响展示） */
  citations: boolean
  /** 写入前是否先预览确认；关闭后仍保留一次撤销 */
  confirmInsert: boolean
}

export const DEFAULT_AI_PREFS: WorkbenchAIPrefs = {
  enabled: true,
  defaultAgentId: 'builtin-quick-answer',
  citations: true,
  confirmInsert: true,
}

const PREFIX = 'know.workbench.ai-prefs.v1'

/** 生成存储键；缺租户或用户时返回空串（不写全局键，避免跨账号串用）。 */
export function prefsKey(tenantId: string | number, userId: string): string {
  if (!tenantId || !userId) return ''
  return `${PREFIX}:${tenantId}:${userId}`
}

/**
 * 解析偏好。
 *
 * 用逐字段校验而不是整体 `as`：一个字段坏了不该把其他三项也重置。
 */
export function parsePrefs(raw: unknown): WorkbenchAIPrefs {
  const source = (raw ?? {}) as Record<string, unknown>
  return {
    enabled: typeof source.enabled === 'boolean' ? source.enabled : DEFAULT_AI_PREFS.enabled,
    defaultAgentId:
      typeof source.defaultAgentId === 'string'
        ? source.defaultAgentId
        : DEFAULT_AI_PREFS.defaultAgentId,
    citations:
      typeof source.citations === 'boolean' ? source.citations : DEFAULT_AI_PREFS.citations,
    confirmInsert:
      typeof source.confirmInsert === 'boolean'
        ? source.confirmInsert
        : DEFAULT_AI_PREFS.confirmInsert,
  }
}

/** 从 localStorage 读取；JSON 损坏或键为空时回默认。 */
export function loadPrefs(storage: Storage | null, key: string): WorkbenchAIPrefs {
  if (!storage || !key) return { ...DEFAULT_AI_PREFS }
  try {
    const raw = storage.getItem(key)
    if (!raw) return { ...DEFAULT_AI_PREFS }
    return parsePrefs(JSON.parse(raw))
  } catch {
    return { ...DEFAULT_AI_PREFS }
  }
}

/** 写回 localStorage；quota 或隐私模式下静默失败（偏好不是关键数据）。 */
export function savePrefs(storage: Storage | null, key: string, prefs: WorkbenchAIPrefs): void {
  if (!storage || !key) return
  try {
    storage.setItem(key, JSON.stringify(prefs))
  } catch {
    // 忽略：写不进去只影响下次打开时的默认值。
  }
}

/**
 * 校验默认智能体是否仍可用，不可用时按顺序回退（§9.14.5）。
 *
 * 回退顺序：原选择 → 第一个内置 → 第一个可用的「我创建」 → 空串。
 * 空串表示“用租户默认模型”，仍可工作，因此不会把条目页卡成不可用。
 */
export function resolveDefaultAgent(
  selected: string,
  available: Array<{ id: string; is_builtin?: boolean; ready?: boolean }>,
): string {
  const usable = available.filter((agent) => agent.ready !== false)
  if (selected && usable.some((agent) => agent.id === selected)) return selected
  const builtin = usable.find((agent) => agent.is_builtin)
  if (builtin) return builtin.id
  return usable[0]?.id ?? ''
}
