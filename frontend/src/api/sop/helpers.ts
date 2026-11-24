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
