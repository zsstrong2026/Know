/**
 * 导入资料（P4-1，实现方案 §12.3）。
 *
 * 两条路径共用一套「选文件 → 校验 → 逐文件导入 → 失败可重试」的状态机：
 *   - 加入工作知识库：交给已有的上传队列（stores/uploadTasks），进度、取消、
 *     重试都由它负责，这里只做入口校验；
 *   - 作为画布附件：本模块逐个上传为条目资产，失败项保留在列表里可单独重试。
 *
 * 这里刻意不依赖 Vue / axios，便于用 `tsx --test` 直接验证校验与重试口径。
 */

/** 与后端 MAX_FILE_SIZE_MB 默认值一致；超过时前端先拦，避免白传一遍。 */
export const IMPORT_MAX_BYTES = 50 * 1024 * 1024

/** 附件路径的上限：与后端 types.WorkbenchMaxAssetBytes（20MB）保持同一口径。 */
export const ATTACHMENT_MAX_BYTES = 20 * 1024 * 1024

/** 方案 §12.3.1 支持的格式；`accept` 与校验都从这里取，避免两处口径不一致。 */
export const IMPORT_ACCEPT = '.pdf,.docx,.pptx,.xlsx,.md,.markdown,.html,.htm'

const IMPORT_EXTENSIONS = IMPORT_ACCEPT.split(',')

export type ImportTaskState = 'pending' | 'uploading' | 'done' | 'failed'

export interface ImportTask {
  /** 列表内唯一键：同名文件可以选两次，不能用名字当 key。 */
  key: string
  file: File
  state: ImportTaskState
  /** 上传成功后的条目资产 id（附件路径用）。 */
  assetId?: string
  error?: string
}

export interface RejectedFile {
  name: string
  reason: 'empty' | 'too_large' | 'unsupported'
}

export interface PickedFiles {
  accepted: File[]
  rejected: RejectedFile[]
}

/** 取小写扩展名（含点）；没有扩展名返回空串。 */
export function fileExtension(name: string): string {
  const index = name.lastIndexOf('.')
  return index <= 0 ? '' : name.slice(index).toLowerCase()
}

/** 校验一批文件：空文件、超限、非支持格式分别拒绝，并保留可导入的部分。 */
export function pickImportFiles(files: File[], maxBytes = IMPORT_MAX_BYTES): PickedFiles {
  const accepted: File[] = []
  const rejected: RejectedFile[] = []
  for (const file of files) {
    if (file.size <= 0) {
      rejected.push({ name: file.name, reason: 'empty' })
      continue
    }
    if (file.size > maxBytes) {
      rejected.push({ name: file.name, reason: 'too_large' })
      continue
    }
    if (!IMPORT_EXTENSIONS.includes(fileExtension(file.name))) {
      rejected.push({ name: file.name, reason: 'unsupported' })
      continue
    }
    accepted.push(file)
  }
  return { accepted, rejected }
}

/** 用「文件名 + 序号」生成稳定 key，重试时沿用同一个任务行。 */
export function createImportTasks(files: File[]): ImportTask[] {
  return files.map((file, index) => ({ key: `${index}:${file.name}`, file, state: 'pending' }))
}

/** 还有多少项没处理完（pending / uploading）；用于禁用「导入」按钮。 */
export function pendingCount(tasks: ImportTask[]): number {
  return tasks.filter((task) => task.state === 'pending' || task.state === 'uploading').length
}

/** 失败项数量；决定是否显示「重试失败项」。 */
export function failedCount(tasks: ImportTask[]): number {
  return tasks.filter((task) => task.state === 'failed').length
}

/** 成功项数量。 */
export function doneCount(tasks: ImportTask[]): number {
  return tasks.filter((task) => task.state === 'done').length
}

/** 把失败项重置为待处理，供「重试失败项」复用同一条链路。 */
export function resetFailedTasks(tasks: ImportTask[]): ImportTask[] {
  return tasks.map((task) =>
    task.state === 'failed' ? { key: task.key, file: task.file, state: 'pending' } : task,
  )
}

/** 一份上传失败的原因文案 key 由调用方决定，这里只保留 message 本身。 */
export interface ImportUploadResult {
  assetId: string
}

/**
 * 逐个上传（串行）。
 *
 * 串行而不是并发：单个附件通常几十 MB，并发会把带宽切碎且更难给出稳定的
 * 进度文案；数量本来就不多，串行更可预测。
 *
 * `onChange` 每次状态变化都会回调一次，调用方据此刷新列表。
 */
export async function runImportTasks(
  tasks: ImportTask[],
  upload: (file: File) => Promise<ImportUploadResult>,
  onChange: (tasks: ImportTask[]) => void,
): Promise<ImportTask[]> {
  const next = tasks.map((task) => ({ ...task }))
  for (let index = 0; index < next.length; index += 1) {
    const task = next[index]
    if (task.state !== 'pending') continue
    task.state = 'uploading'
    onChange([...next])
    try {
      const result = await upload(task.file)
      task.state = 'done'
      task.assetId = result.assetId
      task.error = undefined
    } catch (error) {
      task.state = 'failed'
      task.error = error instanceof Error ? error.message : String(error ?? '')
    }
    onChange([...next])
  }
  return next
}

/** 成功上传的附件元数据，交给编辑器插入 attachment 块。 */
export interface ImportedAttachment {
  assetId: string
  name: string
  size: number
  type: string
}

/** 从任务列表里收集可插入的附件；只取 done 且带 assetId 的项。 */
export function collectAttachments(tasks: ImportTask[]): ImportedAttachment[] {
  const result: ImportedAttachment[] = []
  for (const task of tasks) {
    if (task.state !== 'done' || !task.assetId) continue
    result.push({
      assetId: task.assetId,
      name: task.file.name,
      size: task.file.size,
      type: task.file.type || 'application/octet-stream',
    })
  }
  return result
}
