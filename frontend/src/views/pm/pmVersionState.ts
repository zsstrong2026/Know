// 版本管理纯函数层：按钮可用性、评审记录解析、草稿校验、列表筛选排序。
// 不依赖浏览器 API，便于 Node 单测；组件只负责绑定与网络请求。
import type {
  PmReviewConclusion,
  PmVersion,
  PmVersionStatus,
} from '@/api/pm';

// ===== 状态与结论 =====

// 全部 6 态，顺序与后端 types.VersionStatus* 常量一致
export const VERSION_STATUSES: PmVersionStatus[] = [
  'planning',
  'reviewing',
  'finalized',
  'developing',
  'launched',
  'shelved',
];

export const VERSION_CONCLUSIONS: PmReviewConclusion[] = ['pass', 'conditional', 'reject'];

// 定稿允许的结论：通过或有条件通过
export function isPassConclusion(conclusion: string): boolean {
  return conclusion === 'pass' || conclusion === 'conditional';
}

// ===== 按钮可用性（随版本状态变化） =====

type VersionActionTarget = Pick<PmVersion, 'status' | 'review_conclusion'> & {
  artifact_round?: number;
};

// 记录意见：规划中/评审中（规划中需要首轮评审才能进入评审中）
export function canReview(v: Pick<PmVersion, 'status'>): boolean {
  return v.status === 'planning' || v.status === 'reviewing';
}

// 定稿：评审中且最近一轮结论为通过/有条件通过
export function canFinalize(v: VersionActionTarget): boolean {
  return v.status === 'reviewing' && isPassConclusion(v.review_conclusion);
}

// 启动开发：定稿后
export function canDevelop(v: Pick<PmVersion, 'status'>): boolean {
  return v.status === 'finalized';
}

// 上线：开发中
export function canLaunch(v: Pick<PmVersion, 'status'>): boolean {
  return v.status === 'developing';
}

// 搁置：规划中/评审中（定稿后锁定）
export function canShelve(v: Pick<PmVersion, 'status'>): boolean {
  return v.status === 'planning' || v.status === 'reviewing';
}

// 生成/重新生成产物：仅规划中/评审中，定稿后锁定
export function canGenerate(v: Pick<PmVersion, 'status'>): boolean {
  return v.status === 'planning' || v.status === 'reviewing';
}

export function canRegenerate(v: VersionActionTarget): boolean {
  return canGenerate(v) && (v.artifact_round ?? 0) > 0;
}

// 恢复规划：已搁置版本
export function canRestore(v: Pick<PmVersion, 'status'>): boolean {
  return v.status === 'shelved';
}

// 需求集合可编辑（取消/搁置/变更）：仅规划中/评审中
export function canEditRequirementSet(v: Pick<PmVersion, 'status'>): boolean {
  return v.status === 'planning' || v.status === 'reviewing';
}

// ===== 评审记录解析 =====

export type VersionReviewOpinionStatus = 'pending' | 'resolved';

export interface VersionReviewOpinion {
  // 在原始 items 数组中的下标，用于回写处理状态（0 起）
  index: number;
  text: string;
  status: VersionReviewOpinionStatus;
  note: string;
}

export interface VersionReviewRound {
  round: number;
  conclusion: PmReviewConclusion | '';
  reviewers: string;
  reviewed_at: string;
  opinions: VersionReviewOpinion[];
  // 功能变更明细
  changeNote: string;
}

function asRecord(value: unknown): Record<string, unknown> | null {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;
}

function readText(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

// 单条意见：兼容纯文本与 { text, status, note } 两种历史写法
function readOpinion(value: unknown, index: number): VersionReviewOpinion | null {
  if (typeof value === 'string') {
    const text = value.trim();
    return text ? { index, text, status: 'pending', note: '' } : null;
  }
  const record = asRecord(value);
  if (!record) return null;
  const text = readText(record.text).trim();
  if (!text) return null;
  return {
    index,
    text,
    status: record.status === 'resolved' ? 'resolved' : 'pending',
    note: readText(record.note),
  };
}

// 解析 review_opinions：标准结构为 { items: [...], change_note }，同时兼容数组/字符串
function readRoundOpinions(raw: unknown): { opinions: VersionReviewOpinion[]; changeNote: string } {
  if (typeof raw === 'string') {
    const opinion = readOpinion(raw, 0);
    return { opinions: opinion ? [opinion] : [], changeNote: '' };
  }
  if (Array.isArray(raw)) {
    const opinions = raw
      .map((item, index) => readOpinion(item, index))
      .filter((item): item is VersionReviewOpinion => item !== null);
    return { opinions, changeNote: '' };
  }
  const record = asRecord(raw);
  if (!record) return { opinions: [], changeNote: '' };
  const items = Array.isArray(record.items) ? record.items : [];
  const opinions = items
    .map((item, index) => readOpinion(item, index))
    .filter((item): item is VersionReviewOpinion => item !== null);
  return { opinions, changeNote: readText(record.change_note) };
}

// 按轮次倒序返回（最新一轮在最前），非法数据直接跳过
export function readReviewRounds(raw: unknown): VersionReviewRound[] {
  if (!Array.isArray(raw)) return [];
  const rounds: VersionReviewRound[] = [];
  for (const item of raw) {
    const record = asRecord(item);
    if (!record) continue;
    const round = Number(record.round);
    if (!Number.isFinite(round) || round <= 0) continue;
    const conclusion = readText(record.conclusion);
    const parsed = readRoundOpinions(record.opinions);
    rounds.push({
      round,
      conclusion: VERSION_CONCLUSIONS.includes(conclusion as PmReviewConclusion)
        ? (conclusion as PmReviewConclusion)
        : '',
      reviewers: readText(record.reviewers),
      reviewed_at: readText(record.reviewed_at),
      opinions: parsed.opinions,
      changeNote: parsed.changeNote,
    });
  }
  return rounds.sort((a, b) => b.round - a.round);
}

// 全部轮次中待处理的意见条数（定稿弹窗提示用）
export function countPendingOpinions(rounds: VersionReviewRound[]): number {
  return rounds.reduce(
    (total, round) => total + round.opinions.filter((opinion) => opinion.status === 'pending').length,
    0,
  );
}

// ===== 新建版本草稿 =====

export interface VersionDraft {
  name: string;
  plan_date: string;
  plan_launch_date: string;
  release_note: string;
  requirement_ids: string[];
}

export function emptyVersionDraft(): VersionDraft {
  return { name: '', plan_date: '', plan_launch_date: '', release_note: '', requirement_ids: [] };
}

// 返回首个错误对应的 i18n key；校验通过返回空串
export function validateVersionDraft(draft: VersionDraft): string {
  if (!draft.name.trim()) return 'pm.version.form.errName';
  if (!draft.plan_date) return 'pm.version.form.errPlanDate';
  if (!draft.plan_launch_date) return 'pm.version.form.errPlanLaunchDate';
  // 计划上线日必须晚于规划日（与后端校验一致）
  if (draft.plan_launch_date <= draft.plan_date) return 'pm.version.form.errPlanLaunchDate';
  return '';
}

// ===== 列表筛选与排序 =====

export type VersionCreatedRange = '' | '7d' | '30d' | '90d';

export interface VersionFilterState {
  keyword: string;
  status: PmVersionStatus | '';
  created: VersionCreatedRange;
}

export function emptyVersionFilter(): VersionFilterState {
  return { keyword: '', status: '', created: '' };
}

const CREATED_RANGE_DAYS: Record<Exclude<VersionCreatedRange, ''>, number> = {
  '7d': 7,
  '30d': 30,
  '90d': 90,
};

// 关键字命中版本号/名称/版本说明；创建时间按自然日回溯
export function filterVersions<T extends Pick<PmVersion, 'version_no' | 'name' | 'release_note' | 'status' | 'created_at'>>(
  items: T[],
  filter: VersionFilterState,
  now: Date = new Date(),
): T[] {
  const keyword = filter.keyword.trim().toLowerCase();
  const days = filter.created ? CREATED_RANGE_DAYS[filter.created] : 0;
  const since = days > 0 ? now.getTime() - days * 24 * 60 * 60 * 1000 : 0;
  return items.filter((item) => {
    if (keyword) {
      const haystack = `${item.version_no} ${item.name} ${item.release_note}`.toLowerCase();
      if (!haystack.includes(keyword)) return false;
    }
    if (filter.status && item.status !== filter.status) return false;
    if (since > 0) {
      const created = Date.parse(item.created_at);
      if (Number.isNaN(created) || created < since) return false;
    }
    return true;
  });
}

function timeValue(value: string | null | undefined): number {
  if (!value) return 0;
  const parsed = Date.parse(value);
  return Number.isNaN(parsed) ? 0 : parsed;
}

// 列表按创建时间倒序，不修改原数组
export function sortVersions<T extends Pick<PmVersion, 'created_at'>>(items: T[]): T[] {
  return [...items].sort((a, b) => timeValue(b.created_at) - timeValue(a.created_at));
}

// ===== 展示辅助 =====

// 日期展示：yyyy-MM-dd；空值统一占位
export function formatPmDate(value: string | null | undefined): string {
  if (!value) return '—';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '—';
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
}

// 未定稿版本没有版本号，列表展示占位文案
export function versionDisplayNo(v: Pick<PmVersion, 'version_no'>): string {
  return v.version_no || '';
}

// 版本号格式校验（X.Y.Z，允许 v 前缀）
export function normalizeVersionNo(raw: string): string {
  const trimmed = raw.trim();
  const matched = trimmed.match(/^v?(\d+)\.(\d+)\.(\d+)$/i);
  if (!matched) return '';
  return `v${matched[1]}.${matched[2]}.${matched[3]}`;
}
