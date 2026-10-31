/**
 * 批量归档的纯逻辑（§9.8.1）。
 *
 * 这里只做「可归档性判定 / 预检分组 / 结果汇总」三件事，
 * 真正的请求编排在 use-batch-archive.ts 里。抽出来是为了能单测分组口径，
 * 而不是把规则散落在对话框模板里。
 */
import type { ArchiveStatus } from '@/types/workbench'

/** 批量里对某个条目要执行的动作。`skip` 表示只计入跳过，不发请求。 */
export type BatchArchiveOp = 'create' | 'update' | 'retry' | 'recreate' | 'skip'

export interface BatchArchiveCandidate {
  /** 列表内唯一键（不是数据库主键承诺） */
  key: string
  sourceType: 'item' | 'doc'
  sourceId: string
  title: string
  status: ArchiveStatus
  /** 归档基线版本；stale 时用它作为更新基线 */
  baseVersion: number
}

/**
 * 判定单条在批量里的动作。
 *
 * 与 §9.8.1 的表格一一对应：
 * none → 首次；stale → 更新；partial → 重试；deleted → 重新；
 * active / processing / source_deleted → 跳过。
 */
export function batchOpFor(status: ArchiveStatus | undefined): BatchArchiveOp {
  switch (status) {
    case 'none':
      return 'create'
    case 'stale':
      return 'update'
    case 'partial':
      return 'retry'
    case 'deleted':
      return 'recreate'
    default:
      // active / processing / source_deleted：都不该被重新写一次。
      return 'skip'
  }
}

export interface BatchArchivePlan {
  /** 需要真正发请求的条目 */
  actionable: Array<BatchArchiveCandidate & { op: Exclude<BatchArchiveOp, 'skip'> }>
  /** 会被跳过的条目（用于「将跳过 N 项」） */
  skipped: BatchArchiveCandidate[]
}

/** 按 §9.8.1 的表格把选中项拆成「要执行」与「将跳过」两组。 */
export function planBatchArchive(selected: BatchArchiveCandidate[]): BatchArchivePlan {
  const actionable: BatchArchivePlan['actionable'] = []
  const skipped: BatchArchivePlan['skipped'] = []
  for (const candidate of selected) {
    const op = batchOpFor(candidate.status)
    if (op === 'skip') {
      skipped.push(candidate)
      continue
    }
    actionable.push({ ...candidate, op })
  }
  return { actionable, skipped }
}

/** 预检分组的计数，用于对话框里的「首次归档 1 · 更新归档 1 …」。 */
export interface BatchArchiveCounts {
  create: number
  update: number
  retry: number
  recreate: number
  skip: number
  /** 发请求的总条数（不含 skip） */
  total: number
}

export function countBatchArchive(plan: BatchArchivePlan): BatchArchiveCounts {
  const counts: BatchArchiveCounts = { create: 0, update: 0, retry: 0, recreate: 0, skip: 0, total: 0 }
  for (const item of plan.actionable) {
    counts[item.op] += 1
    counts.total += 1
  }
  counts.skip = plan.skipped.length
  return counts
}

/**
 * 选择上限（§9.8.1）：超过 20 条只允许处理前 20 项，不静默截断。
 *
 * 返回 kept 与 dropped，调用方必须把 dropped 的数量明确告诉用户。
 */
export function capBatchSelection(
  candidates: BatchArchiveCandidate[],
  max = 20,
): { kept: BatchArchiveCandidate[]; dropped: number } {
  if (candidates.length <= max) return { kept: candidates, dropped: 0 }
  return { kept: candidates.slice(0, max), dropped: candidates.length - max }
}

/** 单条执行结果。`skipped` 用于“执行时才发现已是最新归档”的情况。 */
export type BatchArchiveStatus = 'archived' | 'failed' | 'skipped'

export type BatchArchiveReason =
  | 'conflict'
  | 'too_large'
  | 'quota'
  | 'forbidden'
  | 'network'
  | 'not_ready'
  | 'changed'
  | 'up_to_date'

export interface BatchArchiveResult {
  key: string
  title: string
  status: BatchArchiveStatus
  reason?: BatchArchiveReason
  message?: string
}

export interface BatchArchiveSummary {
  archived: number
  failed: number
  skipped: number
  /** 只含 failed，供“重试失败项”直接复用 */
  retryable: BatchArchiveResult[]
}

export function summarizeBatchResults(results: BatchArchiveResult[]): BatchArchiveSummary {
  const summary: BatchArchiveSummary = { archived: 0, failed: 0, skipped: 0, retryable: [] }
  for (const result of results) {
    if (result.status === 'archived') summary.archived += 1
    else if (result.status === 'failed') {
      summary.failed += 1
      summary.retryable.push(result)
    } else summary.skipped += 1
  }
  return summary
}

/**
 * 把服务端错误码映射成失败原因（§9.8.1 文案边界）。
 *
 * 2400 在批量里表示“配额/工作库不可用”：条目不存在发生在执行时，
 * 那时按 not_ready 处理更准确，因此不在此处映射。
 */
export function reasonFromErrorCode(code: number): BatchArchiveReason {
  switch (code) {
    case 2401:
      return 'conflict'
    case 2402:
      return 'too_large'
    case 2400:
      return 'quota'
    case 2405:
      return 'changed'
    case 1002:
      return 'forbidden'
    default:
      return 'network'
  }
}
