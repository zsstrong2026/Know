// 需求池纯函数层：看板分列、筛选排序、弹窗校验、AI 分析结果读写。
// 不依赖浏览器 API，便于 Node 单测；组件只负责绑定与网络请求。
import type {
  PmPriority,
  PmRequirement,
  PmRequirementSource,
  PmRequirementStatus,
  PmValueAchieved,
} from '@/api/pm';
import type { PmStatusTheme } from './pmStatus';

// ===== 状态分组 =====

// 全部 7 态，顺序与后端常量一致
export const REQUIREMENT_STATUSES: PmRequirementStatus[] = [
  'pending_analysis',
  'confirmed',
  'scheduled',
  'developing',
  'launched',
  'rejected',
  'closed',
];

// 终止态：不进入默认看板，由「已归档」筛选在列表视图查看
export const ARCHIVED_REQUIREMENT_STATUSES: PmRequirementStatus[] = ['rejected', 'closed'];
export const ACTIVE_REQUIREMENT_STATUSES: PmRequirementStatus[] = REQUIREMENT_STATUSES.filter(
  (status) => !ARCHIVED_REQUIREMENT_STATUSES.includes(status),
);

// 下拉选项数据源：标签文案由组件按 i18n key 拼接
export const REQUIREMENT_PRIORITIES: PmPriority[] = ['P0', 'P1', 'P2', 'P3'];
export const REQUIREMENT_SOURCES: PmRequirementSource[] = [
  'customer',
  'internal',
  'data',
  'competitor',
  'strategy',
  'other',
];
export const REQUIREMENT_VALUES: PmValueAchieved[] = ['achieved', 'partial', 'not_achieved'];

export interface RequirementBoardColumn<T> {
  status: PmRequirementStatus;
  items: T[];
}

// 按状态分列：默认只输出流转中的 6 列；看板可显式传入 8 态以对齐原型
export function groupByStatus<T extends { status: string }>(
  items: T[],
  statuses: PmRequirementStatus[] = ACTIVE_REQUIREMENT_STATUSES,
): Array<RequirementBoardColumn<T>> {
  return statuses.map((status) => ({
    status,
    items: items.filter((item) => item.status === status),
  }));
}

// ===== 新建弹窗草稿 =====

export interface RequirementDraft {
  title: string;
  description: string;
  source: PmRequirementSource | '';
  proposer: string;
  priority: PmPriority;
}

export function emptyRequirementDraft(): RequirementDraft {
  return { title: '', description: '', source: '', proposer: '', priority: 'P2' };
}

function isFilled(value: unknown): boolean {
  return typeof value === 'string' && value.trim().length > 0;
}

// 「AI 分析」入口：标题与来源必填（初步想法不强制）
export function canAnalyzeRequirement(draft: { title?: string; source?: string }): boolean {
  return isFilled(draft.title) && isFilled(draft.source);
}

// 保存草稿：仅要求标题，其余字段可后补，落库状态为「待分析」
export function canSaveDraft(draft: { title?: string }): boolean {
  return isFilled(draft.title);
}

// ===== 列表筛选与排序 =====

export type RequirementSort = 'updated_desc' | 'updated_asc' | 'priority' | 'created_desc';

export const REQUIREMENT_SORTS: RequirementSort[] = ['updated_desc', 'updated_asc', 'priority', 'created_desc'];

export interface RequirementFilterState {
  keyword: string;
  priority: PmPriority | '';
  status: PmRequirementStatus | '';
  source: PmRequirementSource | '';
  valueAchieved: PmValueAchieved | '';
  sort: RequirementSort;
}

export function emptyRequirementFilter(): RequirementFilterState {
  return { keyword: '', priority: '', status: '', source: '', valueAchieved: '', sort: 'updated_desc' };
}

// 关键字命中标题/描述/提出人；其余条件为空表示不限
export function filterRequirements<T extends Pick<PmRequirement, 'title' | 'description' | 'proposer' | 'priority' | 'status' | 'source' | 'value_achieved'>>(
  items: T[],
  filter: RequirementFilterState,
): T[] {
  const keyword = filter.keyword.trim().toLowerCase();
  return items.filter((item) => {
    if (keyword) {
      const haystack = `${item.title} ${item.description} ${item.proposer}`.toLowerCase();
      if (!haystack.includes(keyword)) return false;
    }
    if (filter.priority && item.priority !== filter.priority) return false;
    if (filter.status && item.status !== filter.status) return false;
    if (filter.source && item.source !== filter.source) return false;
    if (filter.valueAchieved && item.value_achieved !== filter.valueAchieved) return false;
    return true;
  });
}

const PRIORITY_WEIGHT: Record<string, number> = { P0: 0, P1: 1, P2: 2, P3: 3 };

function timeValue(value: string | null | undefined): number {
  if (!value) return 0;
  const parsed = Date.parse(value);
  return Number.isNaN(parsed) ? 0 : parsed;
}

// 排序不修改原数组；优先级相同时按更新时间倒序兜底
export function sortRequirements<T extends Pick<PmRequirement, 'priority' | 'created_at' | 'updated_at'>>(
  items: T[],
  sort: RequirementSort,
): T[] {
  const list = [...items];
  switch (sort) {
    case 'updated_asc':
      return list.sort((a, b) => timeValue(a.updated_at) - timeValue(b.updated_at));
    case 'priority':
      return list.sort((a, b) => (
        (PRIORITY_WEIGHT[a.priority] ?? 9) - (PRIORITY_WEIGHT[b.priority] ?? 9)
        || timeValue(b.updated_at) - timeValue(a.updated_at)
      ));
    case 'created_desc':
      return list.sort((a, b) => timeValue(b.created_at) - timeValue(a.created_at));
    case 'updated_desc':
    default:
      return list.sort((a, b) => timeValue(b.updated_at) - timeValue(a.updated_at));
  }
}

// ===== AI 分析结果 =====

export interface RequirementAnalysis {
  value: string;
  impact: string;
  risk: string;
  designPlan: string;
}

export function emptyRequirementAnalysis(): RequirementAnalysis {
  return { value: '', impact: '', risk: '', designPlan: '' };
}

// 与后端 ai_analysis JSON 字段对齐（snake_case）
export function readRequirementAnalysis(raw: Record<string, unknown> | null | undefined): RequirementAnalysis {
  const source = raw ?? {};
  const readText = (key: string): string => {
    const value = source[key];
    return typeof value === 'string' ? value : '';
  };
  return {
    value: readText('value'),
    impact: readText('impact'),
    risk: readText('risk'),
    designPlan: readText('design_plan'),
  };
}

export function buildAnalysisRecord(analysis: RequirementAnalysis): Record<string, unknown> {
  return {
    value: analysis.value.trim(),
    impact: analysis.impact.trim(),
    risk: analysis.risk.trim(),
    design_plan: analysis.designPlan.trim(),
  };
}

// 是否已有任一分析结论（决定详情抽屉与列表操作是否展示「AI 分析」）
export function hasAnalysis(raw: Record<string, unknown> | null | undefined): boolean {
  const analysis = readRequirementAnalysis(raw);
  return Object.values(analysis).some((value) => isFilled(value));
}

// 分析输出常见字段标题，按「长名优先」匹配，避免「影响」抢先命中「关联功能影响」
const ANALYSIS_LABELS: Array<{ field: keyof RequirementAnalysis; labels: string[] }> = [
  { field: 'value', labels: ['需求价值', '价值'] },
  { field: 'impact', labels: ['关联功能影响', '关联影响', '影响'] },
  { field: 'risk', labels: ['技术风险', '风险'] },
  { field: 'designPlan', labels: ['实现方案', '具体设计方案', '设计方案'] },
];

function matchAnalysisLabel(line: string): { field: keyof RequirementAnalysis; rest: string } | null {
  // 去掉 Markdown 标题、序号与加粗标记，统一成「字段名：内容」
  const cleaned = line
    .trim()
    .replace(/^#{1,6}\s*/, '')
    .replace(/^\d+[.、)]\s*/, '')
    .replace(/\*\*/g, '')
    .replace(/^[-*]\s*/, '')
    .trim();
  for (const { field, labels } of ANALYSIS_LABELS) {
    for (const label of labels) {
      if (!cleaned.startsWith(label)) continue;
      const rest = cleaned.slice(label.length).replace(/^[\s:：\-—]+/, '').trim();
      return { field, rest };
    }
  }
  return null;
}

// 从 AI 回复正文中解析结构化字段：支持「## 1. 需求价值」「**风险**：…」等写法
export function parseAnalysisSections(text: string): Partial<RequirementAnalysis> {
  const result: Partial<RequirementAnalysis> = {};
  let current: keyof RequirementAnalysis | null = null;
  const buffers: Partial<Record<keyof RequirementAnalysis, string[]>> = {};

  for (const line of text.split(/\r?\n/)) {
    const matched = matchAnalysisLabel(line);
    if (matched) {
      current = matched.field;
      buffers[current] = matched.rest ? [matched.rest] : [];
      continue;
    }
    if (current) {
      (buffers[current] ??= []).push(line);
    }
  }

  const join = (key: keyof RequirementAnalysis) => (buffers[key] ?? []).join('\n').trim();
  const value = join('value');
  const impact = join('impact');
  const risk = join('risk');
  const designPlan = join('designPlan');
  if (value) result.value = value;
  if (impact) result.impact = impact;
  if (risk) result.risk = risk;
  if (designPlan) result.designPlan = designPlan;
  return result;
}

// ===== 展示与操作辅助 =====

export function priorityTheme(priority: PmPriority | ''): PmStatusTheme {
  if (priority === 'P0') return 'danger';
  if (priority === 'P1') return 'warning';
  if (priority === 'P2') return 'primary';
  return 'default';
}

export function valueTheme(value: PmValueAchieved | ''): PmStatusTheme {
  if (value === 'achieved') return 'success';
  if (value === 'partial') return 'warning';
  if (value === 'not_achieved') return 'danger';
  return 'default';
}

export function sourceLabelKey(source: PmRequirementSource | ''): string {
  return `pm.requirements.source.${source || 'other'}`;
}

export function valueLabelKey(value: PmValueAchieved | ''): string {
  return value ? `pm.requirements.value.${value}` : 'pm.requirements.value.none';
}

// 待分析需求可「确认需求」，确认后进入已确认，后续状态由版本进度自动流转
export function canConfirm(status: PmRequirementStatus): boolean {
  return status === 'pending_analysis';
}

// AI 分析作为编辑辅助：仅终止态不可用
export function canAnalyze(status: PmRequirementStatus): boolean {
  return !ARCHIVED_REQUIREMENT_STATUSES.includes(status);
}

// 终止态可重新激活
export function canReactivate(status: PmRequirementStatus): boolean {
  return ARCHIVED_REQUIREMENT_STATUSES.includes(status);
}

// 上线验证仅对已上线需求开放
export function canVerify(status: PmRequirementStatus): boolean {
  return status === 'launched';
}

// ===== 需求附件 =====

// 与后端 PMRequirementAttachmentMaxSize 保持一致（10MB）
export const ATTACHMENT_MAX_SIZE = 10 * 1024 * 1024;

// 与后端扩展名白名单保持一致，供 input[accept] 与本地预校验使用
const ATTACHMENT_EXTENSIONS = [
  '.png', '.jpg', '.jpeg', '.gif', '.webp',
  '.pdf', '.doc', '.docx', '.xls', '.xlsx', '.ppt', '.pptx',
  '.txt', '.md', '.csv', '.json',
];
export const ATTACHMENT_ACCEPT = ATTACHMENT_EXTENSIONS.join(',');

export function isAllowedAttachment(fileName: string): boolean {
  const name = fileName.toLowerCase();
  return ATTACHMENT_EXTENSIONS.some((ext) => name.endsWith(ext));
}

// 按扩展名判断能否内联预览（后端 contentType 可能为空或不可靠）
export function isImageAttachment(nameOrType: string): boolean {
  const value = nameOrType.toLowerCase();
  if (value.startsWith('image/')) return true;
  return /\.(png|jpe?g|gif|webp)$/.test(value);
}

// 文件大小展示：<1MB 显示 KB，其余显示 MB，保留一位小数
export function formatFileSize(size: number): string {
  if (!Number.isFinite(size) || size <= 0) return '0 KB';
  if (size < 1024 * 1024) return `${Math.max(1, Math.round(size / 1024))} KB`;
  return `${(size / 1024 / 1024).toFixed(1)} MB`;
}

// 列表时间展示：yyyy-MM-dd HH:mm；非法或空值统一显示占位符
export function formatPmTime(value: string | null | undefined): string {
  if (!value) return '—';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '—';
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}
