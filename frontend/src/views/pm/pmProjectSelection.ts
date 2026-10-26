// ===== PM 工作站项目选择与 Tab 归属的纯逻辑（组件层不做单测，抽到此处）=====

const LAST_PROJECT_KEY = 'pm:lastProjectId';

// 顶部 Tab 对应的路由名，顺序与展示顺序一致
export const PM_TAB_ROUTES = [
  'pmRoadmap',
  'pmRequirements',
  'pmVersions',
  'pmFeatures',
  'pmMaterials',
] as const;

export type PmTabRoute = (typeof PM_TAB_ROUTES)[number];

// 读取上次切换的项目；隐私模式等取不到存储时返回空串
export function readLastProjectId(): string {
  try {
    return window.localStorage.getItem(LAST_PROJECT_KEY) || '';
  } catch {
    return '';
  }
}

// 记录当前项目；空值表示清空（例如项目被删除后无剩余项目）
export function saveLastProjectId(projectId: string): void {
  try {
    if (projectId) window.localStorage.setItem(LAST_PROJECT_KEY, projectId);
    else window.localStorage.removeItem(LAST_PROJECT_KEY);
  } catch {
    // 持久化失败不影响当前会话内的切换
  }
}

// 优先沿用上次项目，其次回退到列表首个；列表为空时返回空串
export function resolveInitialProjectId(lastProjectId: string, projectIds: string[]): string {
  if (lastProjectId && projectIds.includes(lastProjectId)) return lastProjectId;
  return projectIds[0] || '';
}

// 详情类子页归一到所属 Tab，未知路由默认功能规划，切换项目时保持当前 Tab
export function resolveTabRoute(routeName: string): PmTabRoute {
  if (routeName === 'pmVersionDetail') return 'pmVersions';
  if (routeName === 'pmMaterialPreview') return 'pmMaterials';
  return (PM_TAB_ROUTES as readonly string[]).includes(routeName)
    ? (routeName as PmTabRoute)
    : 'pmRoadmap';
}
