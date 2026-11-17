/**
 * AI 语义分段的网络层。
 *
 * 复用内联 AI 的 `custom` 动作：服务端已有统一的提示词护栏（把文档内容当数据）、
 * 鉴权与模型选择，这里不再单独加一个后端端点。
 */
import { streamInlineAI } from './use-inline-ai'
import {
  SEGMENT_INSTRUCTION,
  buildSegmentBatches,
  parseSegmentReply,
  type SegmentBatch,
} from './paragraph-plan'

export interface RequestSegmentsOptions {
  /** 当前文档 id：让后端按文档作用域选模型/记账 */
  itemId?: string
  /** 工作台设置里的默认智能体 */
  agentId?: string
  /** 调用方负责超时与取消（AbortError 会原样抛出） */
  signal?: AbortSignal
}

/**
 * 请求 AI 做语义分段。
 *
 * 返回与 paragraphs 等长的数组：命中的位置给出分段结果，未命中为 null（交给规则分段）。
 * 整批都没有有效结果时返回 null，调用方直接走规则排版。
 */
export async function requestAISegments(
  paragraphs: string[],
  options: RequestSegmentsOptions = {},
): Promise<Array<string[] | null> | null> {
  const batches = buildSegmentBatches(paragraphs)
  if (batches.length === 0) return null

  // 多批并行：每批都很小，串行会把等待时间翻倍
  const replies = await Promise.all(
    batches.map((batch) => runBatch(batch, options).catch(() => new Map<number, string[]>())),
  )

  const merged: Array<string[] | null> = paragraphs.map(() => null)
  let hits = 0
  for (const reply of replies) {
    for (const [index, chunks] of reply) {
      merged[index] = chunks
      hits += 1
    }
  }
  return hits > 0 ? merged : null
}

async function runBatch(
  batch: SegmentBatch,
  options: RequestSegmentsOptions,
): Promise<Map<number, string[]>> {
  const result = await streamInlineAI(
    {
      action: 'custom',
      text: batch.text,
      instruction: SEGMENT_INSTRUCTION,
      item_id: options.itemId,
      agent_id: options.agentId,
    },
    // 分段不需要逐字预览，增量直接丢弃
    () => {},
    options.signal,
  )
  return parseSegmentReply(result.text, batch)
}
