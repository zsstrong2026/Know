/**
 * 离开 workspace 确认文案的纯逻辑（§9.10）。
 *
 * 两处退出入口（组织列表、租户信息）都用它拼 body：
 * 预览拿到数据时列出数量与保留期，拿不到时退化为通用确认文案。
 * 抽成纯函数便于单测，也让两处入口不可能各自写出不一致的说法。
 */
import type { WorkbenchLeavePreview } from '@/types/workbench'

export interface LeaveSummaryTexts {
  /** 带数量的一句话，参数 {kb}{knowledge}{items}{docs} */
  counts: string
  /** 保留期说明，参数 {days} */
  retention: string
  /** 不转交给管理员/其他成员 */
  noTransfer: string
  /** 预览不可用时的兜底说明 */
  fallback: string
}

/** 把预览数据拼成多行确认文案；preview 为 null 时返回兜底文案。 */
export function buildLeaveSummary(
  preview: WorkbenchLeavePreview | null,
  texts: LeaveSummaryTexts,
): string {
  if (!preview) return texts.fallback
  const counts = fill(texts.counts, {
    kb: preview.kb_count,
    knowledge: preview.knowledge_count,
    items: preview.item_count,
    docs: preview.doc_count,
  })
  const retention = fill(texts.retention, { days: preview.purge_after_days })
  return [counts, retention, texts.noTransfer].filter(Boolean).join('\n')
}

/** 最简模板替换：只处理 {key} 形式，不引入 i18n 依赖。 */
function fill(template: string, values: Record<string, string | number>): string {
  return template.replace(/\{(\w+)\}/g, (match, key: string) =>
    key in values ? String(values[key]) : match,
  )
}
