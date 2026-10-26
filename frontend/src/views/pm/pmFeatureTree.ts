// 功能清单纯函数层：三级树构建、筛选、统计、迭代历史排序。
// 不依赖浏览器 API，便于 Node 单测；组件只负责渲染与网络请求。
import type {
  PmFeatureChangeType,
  PmFeatureSource,
  PmFeatureStatus,
  PmVersion,
} from '@/api/pm';
import type { PmStatusTheme } from './pmStatus';

// ===== 枚举映射 =====

// 来源：路线图同步 / PRD 同步 / 人工补充
export const FEATURE_SOURCE_MAP: Record<PmFeatureSource, { labelKey: string; theme: PmStatusTheme }> = {
  roadmap: { labelKey: 'pm.features.source.roadmap', theme: 'primary' },
  prd: { labelKey: 'pm.features.source.prd', theme: 'success' },
  manual: { labelKey: 'pm.features.source.manual', theme: 'default' },
};

// 状态：仅规划中 / 已上线两态，与后端 types.FeatureStatus* 一致
export const FEATURE_STATUS_MAP: Record<PmFeatureStatus, { labelKey: string; theme: PmStatusTheme }> = {
  planned: { labelKey: 'pm.features.status.planned', theme: 'warning' },
  launched: { labelKey: 'pm.features.status.launched', theme: 'success' },
};

// 迭代变更类型：新增 / 变更 / 移除
export const FEATURE_CHANGE_MAP: Record<PmFeatureChangeType, { labelKey: string; theme: PmStatusTheme }> = {
  add: { labelKey: 'pm.features.change.add', theme: 'success' },
  change: { labelKey: 'pm.features.change.change', theme: 'warning' },
  remove: { labelKey: 'pm.features.change.remove', theme: 'danger' },
};

export const FEATURE_SOURCES: PmFeatureSource[] = ['roadmap', 'prd', 'manual'];
export const FEATURE_STATUSES: PmFeatureStatus[] = ['planned', 'launched'];

// 空层级兜底名称（模块 / 功能点 / 操作项缺失时使用）
export const UNCLASSIFIED = '未分类';

// ===== 三级树 =====

export type FeatureLevel = 'module' | 'feature' | 'operation';

// 输入只需覆盖建模用到的字段，便于测试直接构造
export interface FeatureTreeInput {
  id?: string;
  module_name?: string;
  feature_name?: string;
  operation_name?: string;
  requirement_id?: string;
  source?: PmFeatureSource;
  status?: PmFeatureStatus;
  latest_version_no?: string;
  latest_version_status?: PmVersion['status'];
}

export interface FeatureTreeNode {
  key: string;
  level: FeatureLevel;
  name: string;
  module_name: string;
  feature_name: string;
  operation_name: string;
  requirement_id: string;
  source: string;
  status: PmFeatureStatus;
  latest_version_no: string;
  latest_version_status?: PmVersion['status'];
  // 叶子对应的功能点记录 ID，用于查询迭代历史；模块/功能节点为空
  feature_id: string;
  children: FeatureTreeNode[];
  // 汇总：叶子数（无操作项时功能点自身算 1 个叶子）与已上线叶子数
  leafCount: number;
  launchedCount: number;
}

// 构建模块 → 功能 → 操作三级树；同名节点合并，空层级补「未分类」
export function buildFeatureTree(nodes: FeatureTreeInput[]): FeatureTreeNode[] {
  const modules = new Map<string, FeatureTreeNode>();
  const features = new Map<string, FeatureTreeNode>();

  for (const row of nodes) {
    const moduleName = normalize(row.module_name);
    const featureName = normalize(row.feature_name);
    // 是否真的存在操作项要看原始值，不能用兜底后的「未分类」判断
    const hasOperation = !!(row.operation_name ?? '').trim();
    const operationName = normalize(row.operation_name);
    const moduleKey = `m:${moduleName}`;
    const featureKey = `${moduleKey}|f:${featureName}`;

    let moduleNode = modules.get(moduleKey);
    if (!moduleNode) {
      moduleNode = createNode('module', moduleName, moduleName, '', moduleKey);
      modules.set(moduleKey, moduleNode);
    }
    let featureNode = features.get(featureKey);
    if (!featureNode) {
      featureNode = createNode('feature', featureName, moduleName, featureName, featureKey);
      features.set(featureKey, featureNode);
      moduleNode.children.push(featureNode);
    }

    // 只有真实操作项才建第三级；否则功能点本身作为叶子承载来源/状态
    const leaf = buildLeaf(row, moduleName, featureName, operationName, featureKey);
    if (hasOperation) {
      featureNode.children.push(leaf);
    } else {
      featureNode.requirement_id = leaf.requirement_id;
      featureNode.source = leaf.source;
      featureNode.status = leaf.status;
      featureNode.latest_version_no = leaf.latest_version_no;
      featureNode.latest_version_status = leaf.latest_version_status;
      featureNode.feature_id = leaf.feature_id;
    }
    accumulate([moduleNode, featureNode, leaf]);
  }

  return Array.from(modules.values());
}

function normalize(value?: string): string {
  const text = (value ?? '').trim();
  return text || UNCLASSIFIED;
}

function createNode(
  level: FeatureLevel,
  name: string,
  moduleName: string,
  featureName: string,
  key: string,
): FeatureTreeNode {
  return {
    key,
    level,
    name,
    module_name: moduleName,
    feature_name: featureName,
    operation_name: '',
    requirement_id: '',
    source: '',
    status: 'planned',
    latest_version_no: '',
    feature_id: '',
    children: [],
    leafCount: 0,
    launchedCount: 0,
  };
}

// 操作项叶子；无操作项时由功能点节点复用其来源/状态
function buildLeaf(
  row: FeatureTreeInput,
  moduleName: string,
  featureName: string,
  operationName: string,
  parentKey: string,
): FeatureTreeNode {
  const hasOperation = !!(row.operation_name ?? '').trim();
  const leaf = createNode(
    hasOperation ? 'operation' : 'feature',
    hasOperation ? operationName : featureName,
    moduleName,
    featureName,
    hasOperation ? `${parentKey}|o:${operationName}` : parentKey,
  );
  leaf.operation_name = row.operation_name?.trim() ?? '';
  leaf.requirement_id = row.requirement_id?.trim() ?? '';
  leaf.source = row.source ?? '';
  leaf.status = row.status ?? 'planned';
  leaf.latest_version_no = row.latest_version_no?.trim() ?? '';
  leaf.latest_version_status = row.latest_version_status;
  leaf.feature_id = row.id?.trim() ?? '';
  return leaf;
}

// 一个功能点记录对应 1 个叶子，逐级累加计数
function accumulate(nodes: FeatureTreeNode[]) {
  for (const node of nodes) {
    node.leafCount += 1;
    if (node.status === 'launched') node.launchedCount += 1;
  }
}

// ===== 后端树归一 =====

// 后端 ListTree 已按 parent_id 组好三级树，前端只做字段归一与叶子计数
export interface FeatureApiNode extends FeatureTreeInput {
  children?: FeatureApiNode[];
}

export function normalizeFeatureTree(nodes: FeatureApiNode[], depth = 0): FeatureTreeNode[] {
  return nodes.map((node) => {
    const level: FeatureLevel = depth === 0 ? 'module' : depth === 1 ? 'feature' : 'operation';
    const name = (node.operation_name || node.feature_name || node.module_name || '').trim() || UNCLASSIFIED;
    const children = normalizeFeatureTree(node.children ?? [], depth + 1);
    const treeNode = createNode(
      level,
      name,
      node.module_name ?? '',
      node.feature_name ?? '',
      `${level}:${node.id ?? name}`,
    );
    treeNode.operation_name = node.operation_name ?? '';
    treeNode.requirement_id = node.requirement_id ?? '';
    treeNode.source = node.source ?? '';
    treeNode.status = node.status ?? 'planned';
    treeNode.latest_version_no = node.latest_version_no ?? '';
    treeNode.latest_version_status = node.latest_version_status;
    treeNode.feature_id = node.id ?? '';
    treeNode.children = children;
    // 叶子计数：有子节点取子节点之和，否则自身算 1 个叶子
    treeNode.leafCount = children.length
      ? children.reduce((sum, child) => sum + child.leafCount, 0)
      : 1;
    treeNode.launchedCount = children.length
      ? children.reduce((sum, child) => sum + child.launchedCount, 0)
      : treeNode.status === 'launched'
        ? 1
        : 0;
    return treeNode;
  });
}

// ===== 筛选 =====

export interface FeatureFilterState {
  keyword: string;
  source: '' | PmFeatureSource;
  // 按「最新迭代版本号」筛选（版本号不带 v 前缀）
  versionNo: string;
  onlyLaunched: boolean;
  status?: '' | PmFeatureStatus;
}

export function emptyFeatureFilter(): FeatureFilterState {
  return { keyword: '', source: '', versionNo: '', onlyLaunched: false, status: '' };
}

// 命中子节点时保留祖先链；模块/功能节点本身不参与匹配
export function filterFeatureTree(
  tree: FeatureTreeNode[],
  filter: FeatureFilterState,
): FeatureTreeNode[] {
  const keyword = filter.keyword.trim().toLowerCase();
  const status = filter.onlyLaunched ? 'launched' : filter.status ?? '';

  const matches = (node: FeatureTreeNode): boolean => {
    if (filter.source && node.source !== filter.source) return false;
    if (status && node.status !== status) return false;
    if (filter.versionNo && node.latest_version_no !== filter.versionNo) return false;
    if (!keyword) return true;
    return [node.name, node.module_name, node.feature_name, node.operation_name]
      .join(' ')
      .toLowerCase()
      .includes(keyword);
  };

  const prune = (list: FeatureTreeNode[]): FeatureTreeNode[] =>
    list.flatMap((node) => {
      if (isLeaf(node)) return matches(node) ? [node] : [];
      const children = prune(node.children);
      return children.length ? [{ ...node, children }] : [];
    });

  return prune(tree);
}

// 操作项一定为叶子；没有操作项时功能点自身即叶子
function isLeaf(node: FeatureTreeNode): boolean {
  return node.level === 'operation' || (node.level === 'feature' && node.children.length === 0);
}

// ===== 统计 =====

export interface FeatureSummary {
  moduleCount: number;
  featureCount: number;
  leafCount: number;
  launchedCount: number;
}

export function summarizeFeatureTree(tree: FeatureTreeNode[]): FeatureSummary {
  const summary: FeatureSummary = { moduleCount: tree.length, featureCount: 0, leafCount: 0, launchedCount: 0 };
  const walk = (nodes: FeatureTreeNode[]) => {
    for (const node of nodes) {
      if (node.level === 'feature') summary.featureCount += 1;
      if (isLeaf(node)) {
        summary.leafCount += 1;
        if (node.status === 'launched') summary.launchedCount += 1;
      }
      walk(node.children);
    }
  };
  walk(tree);
  return summary;
}

// ===== 迭代历史 =====

export interface FeatureIterationInput {
  version_id: string;
  change_type: PmFeatureChangeType;
  change_note?: string;
  changed_at: string;
}

export interface FeatureIterationRow extends FeatureIterationInput {
  version_no: string;
  version_name: string;
  plan_launch_date: string;
}

type FeatureVersionLike = Pick<PmVersion, 'id' | 'version_no' | 'name' | 'plan_launch_date'>;

// 按版本计划上线日升序（无计划日排最后），同日按变更时间升序
export function sortFeatureIterations(
  iterations: FeatureIterationInput[],
  versions: FeatureVersionLike[],
): FeatureIterationRow[] {
  const versionMap = new Map(versions.map((item) => [item.id, item]));
  return iterations
    .map<FeatureIterationRow>((item) => {
      const version = versionMap.get(item.version_id);
      return {
        ...item,
        version_no: version?.version_no ?? '',
        version_name: version?.name ?? '',
        plan_launch_date: version?.plan_launch_date ?? '',
      };
    })
    .sort((a, b) => {
      // 无计划日期的版本统一后置
      if (!a.plan_launch_date !== !b.plan_launch_date) return a.plan_launch_date ? -1 : 1;
      if (a.plan_launch_date !== b.plan_launch_date) {
        return a.plan_launch_date < b.plan_launch_date ? -1 : 1;
      }
      return a.changed_at < b.changed_at ? -1 : a.changed_at > b.changed_at ? 1 : 0;
    });
}

// 版本号展示补 v 前缀；未定稿版本无版本号
export function featureVersionLabel(versionNo?: string): string {
  const text = (versionNo ?? '').trim();
  return text ? `v${text}` : '';
}
