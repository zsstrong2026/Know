// 功能规划（路线图）纯函数：5 步引导状态机、草稿合并、清单聚合与 Markdown 渲染。
// 不依赖浏览器 API，便于 Node 单测；组件只负责绑定与网络请求。
import type {
  PmFeature,
  PmPlanType,
  PmRoadmapCognition,
  PmRoadmapDraft,
  PmRoadmapFlow,
} from '@/api/pm';

export const ROADMAP_STEP_COUNT = 5;
export const PLAN_TYPES: PmPlanType[] = ['initial', 'iteration', 'replan'];

// 草稿的宽松形态：真实结构为 PmRoadmapDraft，project_brief 为步骤 1 摘要兼容字段；
// scope 允许单个字符串，便于步骤校验在未初始化数组时也能工作。
export interface RoadmapStateDraft {
  project_brief?: string;
  cognition?: Partial<PmRoadmapCognition>;
  features?: Array<Partial<PmFeature>>;
  flows?: Array<Partial<PmRoadmapFlow>>;
  scope?: string | string[];
  batch_name?: string;
  plan_type?: PmPlanType | '';
}

function isFilled(value: unknown): boolean {
  return typeof value === 'string' && value.trim().length > 0;
}

// 规则：project_brief 存在时以其为准（计划单测口径）；否则要求项目认知三项齐全
function hasCognition(draft: RoadmapStateDraft): boolean {
  if (typeof draft.project_brief === 'string') return isFilled(draft.project_brief);
  const cognition = draft.cognition;
  return !!cognition
    && isFilled(cognition.target_users)
    && isFilled(cognition.core_value)
    && isFilled(cognition.scenarios);
}

// 规则：至少 1 项，且每项的模块/功能点/操作项都不能为空
export function hasValidFeatures(features: Array<Partial<PmFeature>> = []): boolean {
  return features.length > 0
    && features.every((f) => isFilled(f.module_name) && isFilled(f.feature_name) && isFilled(f.operation_name));
}

// 统计 Mermaid 流程节点数：仅识别带形状（[] () {} >）的节点声明
export function countMermaidNodes(code: string): number {
  const body = code.replace(/^\s*(?:flowchart|graph)\s+(?:TD|TB|LR|RL|BT)\b/i, '');
  const ids = new Set<string>();
  const re = /([A-Za-z0-9_\u4e00-\u9fa5]+)\s*(?:\[|\(|\{|>)/g;
  let match: RegExpExecArray | null;
  while ((match = re.exec(body)) !== null) ids.add(match[1]);
  return ids.size;
}

// 与后端 sanitizeMermaid 对齐：首行必须是 flowchart TD/LR，且至少 2 个节点
export function isValidMermaidFlow(code: unknown): boolean {
  if (!isFilled(code)) return false;
  const text = (code as string).trim();
  const firstLine = text.split(/\r?\n/, 1)[0]?.trim() ?? '';
  if (!/^flowchart\s+(?:TD|LR)$/i.test(firstLine)) return false;
  return countMermaidNodes(text) >= 2;
}

// 规则：至少 1 条流程，模块名非空且流程图合法
export function hasValidFlows(flows: Array<Partial<PmRoadmapFlow>> = []): boolean {
  return flows.length > 0
    && flows.every((f) => isFilled(f.module) && isValidMermaidFlow(f.mermaid));
}

// scope 兼容 string / string[]，过滤空值
export function normalizeScope(scope: string | string[] | undefined): string[] {
  if (Array.isArray(scope)) return scope.filter((v) => isFilled(v)).map((v) => v.trim());
  return isFilled(scope) ? [(scope as string).trim()] : [];
}

// 步骤推进条件：第 5 步需要批次名称与合法规划类型
export function canAdvanceStep(step: number, draft?: RoadmapStateDraft | null): boolean {
  const d = draft ?? {};
  switch (step) {
    case 1:
      return hasCognition(d);
    case 2:
      return hasValidFeatures(d.features ?? []);
    case 3:
      return hasValidFlows(d.flows ?? []);
    case 4:
      return normalizeScope(d.scope).length > 0;
    case 5:
      return isFilled(d.batch_name) && PLAN_TYPES.includes((d.plan_type ?? '') as PmPlanType);
    default:
      return false;
  }
}

// 草稿合并：仅覆盖 patch 中出现的字段（undefined 跳过），不清空其他步骤内容
export function mergeDraft<T extends object>(base: T, patch: Partial<T>): T {
  const next = { ...base } as Record<string, unknown>;
  for (const [key, value] of Object.entries(patch as Record<string, unknown>)) {
    if (value !== undefined) next[key] = value;
  }
  return next as T;
}

export function clampStep(step: number | null | undefined): number {
  const value = Number(step) || 1;
  return Math.min(Math.max(Math.trunc(value), 1), ROADMAP_STEP_COUNT);
}

// 空草稿：新建批次时的初始结构
export function emptyRoadmapDraft(): PmRoadmapDraft {
  return {
    cognition: { target_users: '', core_value: '', scenarios: '', background: '' },
    features: [],
    flows: [],
    scope: [],
  };
}

let draftFeatureSeq = 0;

// 生成本地草稿功能项：后端只按三级名称做自然键，id 仅用于前端列表渲染
export function makeDraftFeature(
  projectId: string,
  moduleName: string,
  featureName: string,
  operationName: string,
): PmFeature {
  draftFeatureSeq += 1;
  return {
    id: `draft-${Date.now()}-${draftFeatureSeq}`,
    project_id: projectId,
    requirement_id: '',
    parent_id: '',
    module_name: moduleName,
    feature_name: featureName,
    operation_name: operationName,
    source: 'roadmap',
    status: 'planned',
    created_at: '',
    updated_at: '',
  };
}

export function featurePath(feature: Partial<PmFeature>): string {
  return [feature.module_name, feature.feature_name, feature.operation_name]
    .map((v) => (v ?? '').trim())
    .join('/');
}

export interface RoadmapModuleNode {
  module: string;
  features: Array<{ name: string; operations: Array<{ name: string; path: string }> }>;
}

// 三级功能清单聚合：模块 → 功能点 → 操作项（保持出现顺序）
export function buildFeatureTree(features: Array<Partial<PmFeature>> = []): RoadmapModuleNode[] {
  const modules = new Map<string, RoadmapModuleNode>();
  for (const feature of features) {
    const moduleName = (feature.module_name ?? '').trim() || '—';
    const featureName = (feature.feature_name ?? '').trim() || '—';
    const operationName = (feature.operation_name ?? '').trim() || '—';
    let moduleNode = modules.get(moduleName);
    if (!moduleNode) {
      moduleNode = { module: moduleName, features: [] };
      modules.set(moduleName, moduleNode);
    }
    let featureNode = moduleNode.features.find((item) => item.name === featureName);
    if (!featureNode) {
      featureNode = { name: featureName, operations: [] };
      moduleNode.features.push(featureNode);
    }
    featureNode.operations.push({ name: operationName, path: featurePath(feature) });
  }
  return Array.from(modules.values());
}

export interface RoadmapStats {
  modules: number;
  features: number;
  operations: number;
  scope: number;
}

export function summarizeDraft(draft?: PmRoadmapDraft | null): RoadmapStats {
  const tree = buildFeatureTree(draft?.features ?? []);
  return {
    modules: tree.length,
    features: tree.reduce((sum, moduleNode) => sum + moduleNode.features.length, 0),
    operations: tree.reduce(
      (sum, moduleNode) => sum + moduleNode.features.reduce((n, f) => n + f.operations.length, 0),
      0,
    ),
    scope: normalizeScope(draft?.scope).length,
  };
}

const FENCE = '```';

// 本地渲染路线图 Markdown：结构与后端 renderRoadmapMarkdown 保持一致，供预览与快照展示
export function buildRoadmapMarkdown(
  projectName: string,
  draft?: PmRoadmapDraft | null,
): string {
  const data = draft ?? { features: [] };
  const cognition = data.cognition;
  const scope = normalizeScope(data.scope);
  const lines: string[] = [
    `# 产品路线图-${projectName || ''}`,
    '',
    '## 项目认知',
    '',
    `- 目标用户：${cognition?.target_users?.trim() || '—'}`,
    `- 核心价值：${cognition?.core_value?.trim() || '—'}`,
    `- 主要场景：${cognition?.scenarios?.trim() || '—'}`,
    `- 本次规划背景：${cognition?.background?.trim() || '—'}`,
    '',
    '## 首期范围',
    '',
    ...(scope.length ? scope.map((item) => `- ${item}`) : ['- 未指定，默认全部功能']),
    '',
    '## 三级功能清单',
    '',
    '| 模块 | 功能点 | 操作项 |',
    '|---|---|---|',
    ...(data.features ?? []).map(
      (f) => `| ${(f.module_name ?? '').trim()} | ${(f.feature_name ?? '').trim()} | ${(f.operation_name ?? '').trim()} |`,
    ),
    '',
    '## 核心业务流程',
    '',
  ];
  const flows = (data.flows ?? []).filter((f) => (f.mermaid ?? '').trim());
  if (!flows.length) {
    lines.push('暂未提供合法流程图。', '');
  } else {
    for (const flow of flows) {
      lines.push(`### ${(flow.module ?? '').trim() || '—'}`, '', FENCE + 'mermaid', (flow.mermaid ?? '').trim(), FENCE, '');
    }
  }
  return lines.join('\n');
}
