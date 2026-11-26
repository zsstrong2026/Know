// SOP 工作台的纯逻辑工具：标签映射、状态派生、节点操作应用、校验与格式化。
// 全部是纯函数，供页面/组件复用，并在 helpers.test.ts 里以 node:test 固化契约。
import type {
  ExecState,
  ItemStatus,
  NodeStatus,
  RunStatus,
  SopArtifactCheck,
  SopDefinition,
  SopEdge,
  SopNode,
  SopNodeOp,
  SopTriggerConfig,
  DiffCard,
} from './index';

export const EXEC_STATES: ExecState[] = ['none', 'running', 'intervene', 'pr', 'closed'];

export const EXEC_STATE_LABELS: Record<ExecState, string> = {
  none: '尚未执行',
  running: '正在推进',
  intervene: '需要介入',
  pr: '待处理 PR',
  closed: '已闭环',
};

export const SOURCE_LABELS: Record<string, string> = {
  customer: '客户反馈',
  internal: '平台新建',
  data: '数据洞察',
  competitor: '竞品分析',
  strategy: '战略规划',
  other: '其他',
};

export const ITEM_STATUS_LABELS: Record<ItemStatus, string> = {
  backlog: '待梳理',
  todo: '待启动',
  in_progress: '进行中',
  in_review: '评审中',
  blocked: '受阻',
  done: '已完成',
  closed: '已关闭',
  cancelled: '已取消',
};

export const RUN_STATUS_LABELS: Record<RunStatus, string> = {
  queued: '排队中',
  running: '进行中',
  waiting: '待确认',
  paused: '已暂停',
  succeeded: '已完成',
  failed: '失败',
  cancelled: '已取消',
};

export const NODE_STATUS_LABELS: Record<NodeStatus, string> = {
  pending: '等待中',
  running: '执行中',
  waiting_approval: '待审批',
  pending_input: '待补充',
  succeeded: '已完成',
  failed: '失败',
  cancelled: '已取消',
  skipped: '已跳过',
};

/**
 * 执行情况五态派生（spec §3.2），逐分支对齐后端 types.DeriveExecState：
 * 事项终态优先；无运行或运行已取消视为尚未执行；paused 计入正在推进；
 * waiting/failed 需要介入；succeeded 待处理 PR；其余（queued 等瞬态）尚未执行。
 */
export function deriveExecState(
  itemStatus: string,
  runStatus: string,
  hasRun: boolean,
): ExecState {
  if (itemStatus === 'done' || itemStatus === 'closed') return 'closed';
  if (!hasRun || runStatus === 'cancelled') return 'none';
  switch (runStatus) {
    case 'running':
    case 'paused':
      return 'running';
    case 'waiting':
    case 'failed':
      return 'intervene';
    case 'succeeded':
      return 'pr';
    default:
      return 'none';
  }
}

function clone<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T;
}

function normalizeOps(ops: SopNodeOp[]): SopNodeOp[] {
  return Array.isArray(ops) ? ops : [];
}

// 后端定义里的边是 source/target，画布操作里用 from/to；两套形状都要能匹配。
type AnyEdge = { id?: string; source?: string; target?: string; from?: string; to?: string; when?: string };

function edgeSource(edge: AnyEdge): string | undefined {
  return edge?.source ?? edge?.from;
}

function edgeTarget(edge: AnyEdge): string | undefined {
  return edge?.target ?? edge?.to;
}

/**
 * 把服务端校验过的节点操作确定性应用到定义副本上（T25 ops 的客户端等价实现）。
 *
 * remove_node 必须同步清理其余节点的 task_refs / reject_to 与全部关联边，
 * 否则画布上会留下指向不存在节点的悬空引用。
 */
export function applyNodeOps(def: SopDefinition, ops: SopNodeOp[]): SopDefinition {
  const out = clone(def || { nodes: [], edges: [], input_schema: [] });
  out.nodes = Array.isArray(out.nodes) ? out.nodes : [];
  out.edges = Array.isArray(out.edges) ? out.edges : [];

  for (const op of normalizeOps(ops)) {
    switch (op.op) {
      case 'set': {
        const node = out.nodes.find((n) => n.id === op.node_id);
        if (node && op.fields) Object.assign(node, op.fields);
        break;
      }
      case 'add_node': {
        if (op.node && !out.nodes.some((n) => n.id === op.node!.id)) {
          out.nodes.push(clone(op.node) as SopNode);
        }
        break;
      }
      case 'remove_node': {
        const id = op.node_id;
        out.nodes = out.nodes.filter((n) => n.id !== id);
        for (const node of out.nodes) {
          if (Array.isArray(node.task_refs)) {
            node.task_refs = node.task_refs.filter((ref) => ref !== id);
          }
          if (node.reject_to === id) node.reject_to = '';
        }
        out.edges = out.edges.filter((e) => edgeSource(e) !== id && edgeTarget(e) !== id);
        break;
      }
      case 'add_edge': {
        if (!op.edge) break;
        const source = edgeSource(op.edge);
        const target = edgeTarget(op.edge);
        if (!source || !target || source === target) break;
        if (!out.nodes.some((n) => n.id === source) || !out.nodes.some((n) => n.id === target)) break;
        if (out.edges.some((e) => edgeSource(e) === source && edgeTarget(e) === target)) break;
        out.edges.push(clone(op.edge) as unknown as SopEdge);
        break;
      }
      case 'remove_edge': {
        if (!op.edge) break;
        const source = edgeSource(op.edge);
        const target = edgeTarget(op.edge);
        out.edges = out.edges.filter((e) => !(edgeSource(e) === source && edgeTarget(e) === target));
        break;
      }
      default:
        break;
    }
  }
  return out;
}

export function gateCheckSummary(check: SopArtifactCheck | undefined | null): string {
  switch (check?.type) {
    case 'contains':
      return `必须包含关键词：${check.keyword || ''}`;
    case 'command':
      return '自定义命令';
    case 'exists':
    default:
      return '文件交了就行';
  }
}

const INTERVAL_LABELS: Record<string, string> = {
  '15m': '每 15 分钟',
  '30m': '每 30 分钟',
  hourly: '每小时',
  daily9: '每天 09:00',
  weekly1: '每周一 09:00',
};

export function intervalSummary(cfg: Pick<SopTriggerConfig, 'mode' | 'interval_key' | 'cron' | 'timezone'>): string {
  if (cfg?.mode === 'cron') {
    return cfg.timezone ? `Cron：${cfg.cron}（${cfg.timezone}）` : `Cron：${cfg.cron}`;
  }
  return INTERVAL_LABELS[cfg?.interval_key] || '未设置';
}

/** 项目编号前缀：2–10 位字母数字且字母开头（与后端 types.ValidateSopProjectCode 一致）。 */
export function validateProjectCode(code: string): boolean {
  return /^[A-Z][A-Z0-9]{1,9}$/.test(String(code || '').trim().toUpperCase());
}

/** 仓库地址：只接受 HTTPS 或 SSH（与后端 types.ValidateSopRepoURL 一致）。 */
export function validateRepoURL(url: string): boolean {
  const value = String(url || '').trim();
  if (value.startsWith('https://') && value.length > 'https://'.length) return true;
  if (value.startsWith('ssh://') && value.length > 'ssh://'.length) return true;
  return /^[\w.-]+@[\w.-]+:[\w./-]+$/.test(value);
}

export function progressText(done: number, total: number): string {
  if (!total || total <= 0) return '—';
  return `${Math.max(0, done)}/${total}`;
}

export function shortSha(sha: string): string {
  return String(sha || '').slice(0, 8);
}

export function formatBytes(n: number): string {
  const size = Number(n) || 0;
  if (size < 1024) return `${size} B`;
  const units = ['KB', 'MB', 'GB', 'TB'];
  let value = size / 1024;
  let idx = 0;
  while (value >= 1024 && idx < units.length - 1) {
    value /= 1024;
    idx += 1;
  }
  return `${value.toFixed(1)} ${units[idx]}`;
}

/** 仓库显示名：URL basename 去掉 .git（后端未给名字时的展示兜底）。 */
export function repoDisplayName(url: string): string {
  const value = String(url || '').replace(/\/+$/, '');
  const base = value.slice(Math.max(value.lastIndexOf('/'), value.lastIndexOf(':')) + 1);
  return base.replace(/\.git$/, '') || value;
}

/** 事项类型中文（与原型 KINDS 一致；未知类型原样返回）。 */
export const KIND_LABELS: Record<string, string> = {
  requirement: '需求',
  bug: '缺陷',
  task: '任务',
  incident: '故障',
  research: '调研',
  custom: '自定义',
  other: '其他',
};

/** 事项状态主题色（列表状态点与看板列头共用，与原型 STATUSES 对齐）。 */
export const ITEM_STATUS_COLORS: Record<ItemStatus, string> = {
  backlog: '#9aa1a9',
  todo: '#0052d9',
  in_progress: '#366ef4',
  in_review: '#e37318',
  blocked: '#d54941',
  done: '#2ba471',
  closed: '#8b939e',
  cancelled: '#c3c8cf',
};

export interface ExecOption {
  value: ExecState | '';
  label: string;
  count: number;
}

/**
 * 执行情况下拉的可搜索过滤：只匹配选项名（计数不参与匹配），空关键词返回全部。
 * 否则输入「3」会把带计数 3 的选项全筛出来。
 */
export function matchExecOptions(
  options: ExecOption[],
  keyword: string,
): ExecOption[] {
  const q = String(keyword || '').trim().toLowerCase();
  if (!q) return options;
  return options.filter((option) => option.label.toLowerCase().includes(q));
}

/** 看板列：7 个可停留状态（cancelled 不进看板，与原型一致）。 */
export const KANBAN_COLUMNS: ItemStatus[] = [
  'backlog',
  'todo',
  'in_progress',
  'in_review',
  'blocked',
  'done',
  'closed',
];

// 事项状态机：与后端 types.sopItemTransitions 逐条对齐（唯一权威在后端）。
const ITEM_TRANSITIONS: Record<string, string[]> = {
  backlog: ['todo', 'cancelled'],
  todo: ['in_progress', 'backlog', 'cancelled'],
  in_progress: ['in_review', 'blocked', 'done', 'cancelled'],
  in_review: ['in_progress', 'done', 'cancelled'],
  blocked: ['in_progress', 'todo', 'cancelled'],
  done: ['closed'],
  closed: ['todo'],
  cancelled: ['todo'],
};

/** 迁移是否合法（本地预判，最终以后端 CanTransitSopItem 为准；同状态放行）。 */
export function canTransitItem(from: string, to: string): boolean {
  if (!from || !to) return false;
  if (from === to) return true;
  return (ITEM_TRANSITIONS[from] || []).includes(to);
}

/** 活动条目（business/audit 两源合并视图共用；at 兼容后端 at/created_at）。 */
export interface MergeableActivity {
  at?: string;
  created_at?: string;
  kind?: string;
  text?: string;
  action?: string;
  detail?: string;
  actor?: string;
  [key: string]: unknown;
}

function activityTime(entry: MergeableActivity): string {
  return String(entry.at || entry.created_at || '');
}

/** 业务活动与审计日志按时间降序合并（同刻保持 business 在前）。 */
export function mergeActivities(
  business: MergeableActivity[],
  audit: MergeableActivity[],
): MergeableActivity[] {
  return [...(business || []), ...(audit || [])].sort((a, b) => {
    const ta = activityTime(a);
    const tb = activityTime(b);
    if (ta === tb) return 0;
    return ta < tb ? 1 : -1;
  });
}

const OUTCOME_LABELS: Record<string, string> = {
  achieved: '已达成',
  partial: '部分达成',
  not_achieved: '未达成',
};

/**
 * 产物结论的白话摘要：优先用 summary，其次按 value_achieved 三档，
 * 都没有时给「尚未回填」（验收 tab 与概览共用）。
 */
export function outcomeSummary(outcome: Record<string, unknown> | undefined | null): string {
  if (!outcome) return '尚未回填';
  const summary = typeof outcome.summary === 'string' ? outcome.summary.trim() : '';
  if (summary) return summary;
  const value = typeof outcome.value_achieved === 'string' ? outcome.value_achieved : '';
  if (value && OUTCOME_LABELS[value]) return OUTCOME_LABELS[value];
  return '尚未回填';
}

/** AI 拆解块在描述里的标记：重写时整块替换，用户原文始终保持不动。 */
export const BREAKDOWN_MARKER = '<!-- sop-ai-breakdown -->';

/**
 * 把 AI 拆解结果写进描述：重复写入是**覆盖**而不是追加，
 * 用户自己写的内容（标记块之外）永远保留。
 */
/** 拆解块的结束标记：与起始标记不同，替换范围才唯一。 */
export const BREAKDOWN_END_MARKER = '<!-- /sop-ai-breakdown -->';

export function upsertBreakdownBlock(description: string, block: string): string {
  const base = String(description || '');
  const payload = [
    BREAKDOWN_MARKER,
    String(block || '').trim(),
    BREAKDOWN_END_MARKER,
  ].join('\n');
  const pattern = new RegExp(`${BREAKDOWN_MARKER}[\\s\\S]*?${BREAKDOWN_END_MARKER}`);
  if (pattern.test(base)) return base.replace(pattern, payload);
  return base.trim() ? `${base.trimEnd()}\n\n${payload}` : payload;
}

const FIELD_LABELS: Record<string, string> = {
  name: '步骤名称',
  task_prompt: '这一步做什么',
  extra_rules: '补充规则',
  executor: '执行者',
  on_failure: '失败策略',
  reject_to: '打回目标',
  mcp: 'MCP 服务',
  skills: '技能',
  artifact_outputs: '交付物',
  max_attempts: '重试次数',
  max_reworks: '返工上限',
  timeout_minutes: '超时（分钟）',
  carry_over: '保留上一轮产物',
  auto_ingest: '通过后自动入库',
};

const FAILURE_LABELS: Record<string, string> = {
  loop: '原地重试',
  reject: '打回上游',
  fail: '直接失败',
  human: '转人工',
};

const EXECUTOR_KIND_LABELS: Record<string, string> = {
  platform_agent: '平台智能体',
  cli: '本机 CLI',
  script: '脚本',
};

export function briefValue(field: string, value: unknown): string {
  if (value === undefined || value === null || value === '') return '未配置';
  if (field === 'on_failure') return FAILURE_LABELS[String(value)] || String(value);
  if (field === 'executor' && value && typeof value === 'object') {
    const executor = value as Record<string, unknown>;
    const kind = EXECUTOR_KIND_LABELS[String(executor.kind)] || String(executor.kind || '未配置');
    const target = executor.agent_id || executor.cli_kind || '';
    return target ? `${kind} · ${target}` : kind;
  }
  if (Array.isArray(value)) return value.length ? value.join('、') : '未配置';
  if (typeof value === 'object') return JSON.stringify(value);
  if (typeof value === 'boolean') return value ? '开启' : '关闭';
  return String(value);
}

/**
 * 把 AI 编排的 ops 转成字段级差异卡（原值 → 新值 + 理由）。
 * 只处理 `set`（其它 op 属结构变更，画布上直接可见，不需要逐字段解释）。
 */
export function formatDiffCards(
  def: SopDefinition,
  ops: { op: string; node_id?: string; fields?: Record<string, unknown>; reason?: string }[],
): DiffCard[] {
  const cards: DiffCard[] = [];
  for (const op of ops || []) {
    if (op.op !== 'set' || !op.node_id || !op.fields) continue;
    const node = (def.nodes || []).find((item) => item.id === op.node_id);
    if (!node) continue;
    for (const [field, value] of Object.entries(op.fields)) {
      const before = (node as unknown as Record<string, unknown>)[field];
      cards.push({
        nodeId: node.id,
        nodeName: node.name || node.id,
        field: FIELD_LABELS[field] || field,
        from: briefValue(field, before),
        to: briefValue(field, value),
        reason: op.reason || '',
        fieldKey: field,
      });
    }
  }
  return cards;
}

/**
 * 两份定义的字段级差异（用于 AI 只返回合并结果的场景）。
 *
 * 后端 node 模式返回 applied/skipped + 合并后的定义，不回传 ops；
 * 这里按「逐节点逐字段」比对还原差异卡，口径与 formatDiffCards 一致。
 */
export function diffDefinitions(before: SopDefinition, after: SopDefinition): DiffCard[] {
  const cards: DiffCard[] = [];
  const beforeNodes = new Map((before.nodes || []).map((node) => [node.id, node]));
  for (const node of after.nodes || []) {
    const prev = beforeNodes.get(node.id);
    if (!prev) {
      cards.push({
        nodeId: node.id,
        nodeName: node.name || node.id,
        field: '新节点',
        from: '—',
        to: node.name || node.id,
        reason: '',
        fieldKey: '',
      });
      continue;
    }
    for (const key of Object.keys(FIELD_LABELS)) {
      const fromValue = JSON.stringify((prev as unknown as Record<string, unknown>)[key] ?? null);
      const toValue = JSON.stringify((node as unknown as Record<string, unknown>)[key] ?? null);
      if (fromValue === toValue) continue;
      cards.push({
        nodeId: node.id,
        nodeName: node.name || node.id,
        field: FIELD_LABELS[key],
        from: briefValue(key, (prev as unknown as Record<string, unknown>)[key]),
        to: briefValue(key, (node as unknown as Record<string, unknown>)[key]),
        reason: '',
        fieldKey: key,
      });
    }
  }
  for (const prev of before.nodes || []) {
    if ((after.nodes || []).some((node) => node.id === prev.id)) continue;
    cards.push({
      nodeId: prev.id,
      nodeName: prev.name || prev.id,
      field: '删除节点',
      from: prev.name || prev.id,
      to: '—',
      reason: '',
      fieldKey: '',
    });
  }
  return cards;
}

const TRIGGER_SOURCE_LABELS: Record<string, string> = {
  manual: '手动',
  schedule: 'SOP 任务',
  api: 'API',
  child_run: '子流程',
};

/** 运行触发来源中文（未知来源原样返回）。 */
export function triggerSourceLabel(kind: string): string {
  return TRIGGER_SOURCE_LABELS[kind] || kind || '—';
}

/**
 * 成功率文案：优先用后端 `success_rate`（0–1 小数），
 * 否则用 status_counts.succeeded / finished_count。分母为 0 时给「—」。
 */
export function successRate(stats: {
  success_rate?: number;
  finished_count?: number;
  status_counts?: Record<string, number>;
  succeeded?: number;
  finished?: number;
}): string {
  if (typeof stats?.success_rate === 'number' && stats.finished_count) {
    return `${Math.round(stats.success_rate * 100)}%`;
  }
  const finished = stats?.finished ?? stats?.finished_count ?? 0;
  if (!finished) return '—';
  const succeeded = stats?.succeeded ?? stats?.status_counts?.succeeded ?? 0;
  return `${Math.round((succeeded / finished) * 100)}%`;
}

export type PreviewKind = 'markdown' | 'text' | 'diff' | 'json' | 'html' | 'binary';

const TEXT_EXTS = new Set([
  'txt', 'log', 'csv', 'tsv', 'yaml', 'yml', 'toml', 'ini', 'xml', 'sh', 'py', 'go', 'ts', 'js', 'sql', 'mdx',
]);

/** 预览器分派：按扩展名决定用哪种视图（未知类型按二进制处理）。 */
export function previewKind(path: string): PreviewKind {
  const clean = String(path || '').split('?')[0];
  const ext = clean.slice(clean.lastIndexOf('.') + 1).toLowerCase();
  if (ext === 'md' || ext === 'markdown') return 'markdown';
  if (ext === 'diff' || ext === 'patch') return 'diff';
  if (ext === 'json') return 'json';
  if (ext === 'html' || ext === 'htm') return 'html';
  if (TEXT_EXTS.has(ext)) return 'text';
  return 'binary';
}

/** 去掉 YAML frontmatter（只处理文件开头的 `---` 块），正文无变化时原样返回。 */
export function stripFrontmatter(markdown: string): string {
  const text = String(markdown || '');
  if (!text.startsWith('---')) return text;
  const end = text.indexOf('\n---', 3);
  if (end < 0) return text;
  const rest = text.slice(end + 4);
  // 吃掉 frontmatter 后面紧跟的空行（正文自身的首行结构保留）
  return rest.replace(/^(\r?\n)+/, '');
}

/** diff 行着色类名：+ 增 / − 删 / @@ hunk 头 / 其它 context。 */
export function diffLineClass(line: string): string {
  if (line.startsWith('@@')) return 'diff-hunk';
  if (line.startsWith('+') && !line.startsWith('+++')) return 'diff-add';
  if (line.startsWith('-') && !line.startsWith('---')) return 'diff-del';
  return '';
}

// ---- 项目共享上下文与大需求拆分（T65） ----

/** 父项执行情况：与后端 types.DeriveParentExecState 同口径（intervene>running>pr>全 closed；空=none）。 */
export function deriveParentExecState(states: string[]): ExecState {
  if (!states.length) return 'none';
  const seen = new Set(states);
  if (seen.has('intervene')) return 'intervene';
  if (seen.has('running')) return 'running';
  if (seen.has('pr')) return 'pr';
  if (seen.has('closed') && seen.size === 1) return 'closed';
  return 'none';
}

/** 子项进度文案：`3/7 子项已完成`；没有子项给破折号。 */
export function childProgressText(done: number, total: number): string {
  if (!total || total <= 0) return '—';
  return `${done}/${total} 子项已完成`;
}

const NOTE_KIND_LABELS: Record<string, string> = {
  decision: '决策',
  run_summary: '运行结论',
  risk: '风险',
  release: '发布',
  note: '随手记',
};

/** 黑板条目类型中文名（未知类型按「随手记」）。 */
export function noteKindLabel(kind: string): string {
  return NOTE_KIND_LABELS[kind] || NOTE_KIND_LABELS.note;
}

const NOTE_SOURCE_LABELS: Record<string, string> = {
  human: '人工',
  engine: '引擎',
  ai: 'AI',
};

/** 黑板条目来源中文名（未知来源按「人工」）。 */
export function noteSourceLabel(source: string): string {
  return NOTE_SOURCE_LABELS[source] || NOTE_SOURCE_LABELS.human;
}

/** 简报是否落后于黑板：渲染水位小于黑板最大 seq 即有未同步动态。 */
export function briefSeqStale(renderedSeq: number, notesMaxSeq: number): boolean {
  return notesMaxSeq > renderedSeq;
}
