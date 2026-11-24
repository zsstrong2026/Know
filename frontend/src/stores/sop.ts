// SOP 工作台的前端状态：当前项目（localStorage 持久）+ 各 Tab 数据 + 轮询助手。
//
// 项目 id 不走路由参数（与 PM 的 :projectId 不同）：SOP 子页 URL 保持稳定，
// 切项目只重载数据、不改地址，用户刷新后回到同一个上次项目。
import { computed, ref } from 'vue';
import { defineStore } from 'pinia';
import {
  cliStatus,
  createProject as apiCreateProject,
  getTriggerConfig,
  listApprovals,
  listCredentials,
  listItems,
  listProjects,
  listRuns,
  listTemplates,
  type ApprovalEntry,
  type CLICredentialMasked,
  type CLIStatus,
  type ExecState,
  type SopItemFilter,
  type SopItemWithDerived,
  type SopProject,
  type SopProjectInput,
  type SopRun,
  type SopRunFilter,
  type SopTemplate,
  type SopTriggerConfig,
} from '@/api/sop';

export const SOP_CURRENT_PROJECT_KEY = 'sop_current_project';

export interface SopItemStats {
  total: number;
  none: number;
  running: number;
  intervene: number;
  pr: number;
  closed: number;
}

const emptyStats = (): SopItemStats => ({ total: 0, none: 0, running: 0, intervene: 0, pr: 0, closed: 0 });

const loadStoredProject = (): string => {
  try {
    return localStorage.getItem(SOP_CURRENT_PROJECT_KEY) || '';
  } catch {
    return '';
  }
};

const storeProject = (id: string) => {
  try {
    if (id) localStorage.setItem(SOP_CURRENT_PROJECT_KEY, id);
    else localStorage.removeItem(SOP_CURRENT_PROJECT_KEY);
  } catch {
    // 隐私模式写入失败不影响功能，只丢失「记住上次项目」
  }
};

/** 五态汇总：口径与后端 DeriveExecState 一致（这里只做计数）。 */
export function summarizeItems(items: SopItemWithDerived[]): SopItemStats {
  const stats = emptyStats();
  stats.total = items.length;
  for (const item of items) {
    const state = item.exec_state as ExecState;
    if (state === 'none' || state === 'running' || state === 'intervene' || state === 'pr' || state === 'closed') {
      stats[state] += 1;
    }
  }
  return stats;
}

export const useSopStore = defineStore('sop', () => {
  const projects = ref<SopProject[]>([]);
  const currentProjectId = ref<string>(loadStoredProject());
  const loadingProjects = ref(false);

  const items = ref<SopItemWithDerived[]>([]);
  const itemStats = ref<SopItemStats>(emptyStats());
  const itemFilter = ref<SopItemFilter>({});
  const loadingItems = ref(false);

  const templates = ref<SopTemplate[]>([]);
  const runs = ref<SopRun[]>([]);
  const runFilter = ref<SopRunFilter>({});
  const loadingRuns = ref(false);

  const approvals = ref<ApprovalEntry[]>([]);
  const triggerConfig = ref<SopTriggerConfig | null>(null);
  const cliStatuses = ref<CLIStatus[]>([]);
  const credentials = ref<CLICredentialMasked[]>([]);

  const currentProject = computed<SopProject | null>(
    () => projects.value.find((p) => p.id === currentProjectId.value) || null,
  );
  const approvalCount = computed(() => approvals.value.length);

  async function loadProjects(): Promise<void> {
    loadingProjects.value = true;
    try {
      projects.value = await listProjects();
    } finally {
      loadingProjects.value = false;
    }
    const ids = projects.value.map((p) => p.id);
    if (!ids.includes(currentProjectId.value)) {
      const next = ids[0] || '';
      currentProjectId.value = next;
      storeProject(next);
    }
  }

  async function loadItems(): Promise<void> {
    const projectId = currentProjectId.value;
    if (!projectId) {
      items.value = [];
      itemStats.value = emptyStats();
      return;
    }
    loadingItems.value = true;
    try {
      const list = await listItems(projectId, itemFilter.value);
      items.value = list;
      itemStats.value = summarizeItems(list);
    } finally {
      loadingItems.value = false;
    }
  }

  // 统计带需要「不受筛选影响」的全量五态计数：单独拉一次无 exec/条件过滤的列表。
  async function loadStats(): Promise<void> {
    const projectId = currentProjectId.value;
    if (!projectId) {
      itemStats.value = emptyStats();
      return;
    }
    const all = await listItems(projectId, {});
    itemStats.value = summarizeItems(all);
  }

  async function loadTemplates(): Promise<void> {
    templates.value = await listTemplates(currentProjectId.value || undefined);
  }

  async function loadRuns(): Promise<void> {
    const projectId = currentProjectId.value;
    if (!projectId) {
      runs.value = [];
      return;
    }
    loadingRuns.value = true;
    try {
      runs.value = await listRuns({ ...runFilter.value, project_id: projectId });
    } finally {
      loadingRuns.value = false;
    }
  }

  async function loadApprovals(): Promise<void> {
    approvals.value = await listApprovals();
  }

  async function loadTriggerConfig(): Promise<void> {
    const projectId = currentProjectId.value;
    triggerConfig.value = projectId ? await getTriggerConfig(projectId) : null;
  }

  async function loadCliStatus(): Promise<void> {
    cliStatuses.value = await cliStatus();
  }

  async function loadCredentials(): Promise<void> {
    credentials.value = await listCredentials();
  }

  /** 切项目：只改前端状态并把该项目的四类数据并行重载（URL 不变）。 */
  async function switchProject(id: string): Promise<void> {
    if (id === currentProjectId.value) return;
    currentProjectId.value = id;
    storeProject(id);
    await Promise.all([
      loadItems(),
      loadStats(),
      loadRuns(),
      loadApprovals(),
      loadTriggerConfig(),
    ]);
  }

  /** 新建项目：创建即切换（空项目态），随后刷新项目列表拿到服务端字段。 */
  async function createProject(input: SopProjectInput): Promise<SopProject> {
    const project = await apiCreateProject(input);
    projects.value = [...projects.value, project];
    await switchProject(project.id);
    await loadProjects();
    return project;
  }

  /** 轮询助手：返回 stop 函数，组件 onUnmounted 必须调用。 */
  function startPolling(fn: () => void | Promise<void>, ms: number): () => void {
    const timer = setInterval(() => {
      void fn();
    }, ms);
    return () => clearInterval(timer);
  }

  return {
    projects,
    currentProjectId,
    loadingProjects,
    items,
    itemStats,
    itemFilter,
    loadingItems,
    templates,
    runs,
    runFilter,
    loadingRuns,
    approvals,
    triggerConfig,
    cliStatuses,
    credentials,
    currentProject,
    approvalCount,
    loadProjects,
    loadItems,
    loadStats,
    loadTemplates,
    loadRuns,
    loadApprovals,
    loadTriggerConfig,
    loadCliStatus,
    loadCredentials,
    switchProject,
    createProject,
    startPolling,
  };
});
