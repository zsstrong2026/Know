<template>
  <!-- PM 工作站：顶部左侧 Tab 导航 + 右侧项目切换，内容区为子路由出口 -->
  <div class="pm-shell">
    <header class="pm-topbar">
      <nav class="pm-tabs">
        <template v-for="item in navItems" :key="item.name">
          <RouterLink
            v-if="selectedProjectId"
            class="pm-tab"
            :class="{ 'is-active': isNavActive(item.name) }"
            :to="{ name: item.name, params: { projectId: selectedProjectId } }"
          >
            <t-icon :name="item.icon" size="17px" />
            <span>{{ $t(item.labelKey) }}</span>
          </RouterLink>
          <span v-else class="pm-tab is-disabled">
            <t-icon :name="item.icon" size="17px" />
            <span>{{ $t(item.labelKey) }}</span>
          </span>
        </template>
      </nav>

      <div class="pm-picker">
        <span class="pm-picker-label">{{ $t('pm.projects.title') }}</span>
        <t-select
          v-model="selectedProjectId"
          class="pm-picker-select"
          :options="projectOptions"
          :placeholder="$t('pm.projects.selectPlaceholder')"
          :loading="loading"
          :disabled="!projectOptions.length"
          @change="onProjectChange"
        />
        <!-- 更多操作：新建 / 删除项目 -->
        <t-dropdown
          :options="moreOptions"
          trigger="click"
          placement="bottom-right"
          attach="body"
          @click="onMoreAction"
        >
          <t-button variant="text" shape="square" :aria-label="$t('pm.projects.more')">
            <template #icon><t-icon name="more" /></template>
          </t-button>
        </t-dropdown>
      </div>
    </header>

    <section class="pm-main">
      <div v-if="!loading && !projects.length" class="pm-empty">
        <div class="pm-empty-title">{{ $t('pm.projects.empty') }}</div>
        <div class="pm-empty-hint">{{ $t('pm.projects.emptyHint') }}</div>
        <t-button theme="primary" @click="createVisible = true">
          {{ $t('pm.projects.create') }}
        </t-button>
      </div>
      <RouterView v-else />
    </section>

    <PmProjectDialog v-model:visible="createVisible" @created="onProjectCreated" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute, useRouter } from 'vue-router';
import { storeToRefs } from 'pinia';
import { MessagePlugin } from 'tdesign-vue-next';
import { deletePmProject, type PmProject } from '@/api/pm';
import { usePmProjectsStore } from '@/stores/pmProjects';
import { useConfirmDelete } from '@/components/settings/useConfirmDelete';
import PmProjectDialog from './PmProjectDialog.vue';
import {
  readLastProjectId,
  resolveInitialProjectId,
  resolveTabRoute,
  saveLastProjectId,
} from './pmProjectSelection';

const router = useRouter();
const route = useRoute();
const { t } = useI18n();
const confirmDanger = useConfirmDelete();

const projectStore = usePmProjectsStore();
const { projects, loading } = storeToRefs(projectStore);

const selectedProjectId = ref('');
const createVisible = ref(false);

// 下拉选项与项目列表同源，新建/删除后自动同步
const projectOptions = computed(() =>
  projects.value.map((project) => ({ label: project.name, value: project.id })),
);

const routeProjectId = computed(() => (route.params.projectId as string) || '');

// 「更多」菜单：新建项目 / 删除项目（无选中项目时删除置灰）
const moreOptions = computed(() => [
  { content: t('pm.projects.create'), value: 'create' },
  { content: t('pm.projects.delete'), value: 'delete', disabled: !selectedProjectId.value },
]);

const navItems = [
  { name: 'pmRoadmap', labelKey: 'pm.nav.roadmap', icon: 'map' },
  { name: 'pmRequirements', labelKey: 'pm.nav.requirements', icon: 'task' },
  { name: 'pmVersions', labelKey: 'pm.nav.versions', icon: 'layers' },
  { name: 'pmFeatures', labelKey: 'pm.nav.features', icon: 'tree-round-dot' },
  { name: 'pmMaterials', labelKey: 'pm.nav.materials', icon: 'folder' },
] as const;

onMounted(async () => {
  try {
    await projectStore.fetchProjects();
  } catch {
    MessagePlugin.error(t('pm.projects.loadFailed'));
    return;
  }
  await syncProjectFromRoute();
});

// 路由带了合法项目则以其为准，否则回退到「上次项目 → 列表首个」
async function syncProjectFromRoute() {
  const ids = projects.value.map((project) => project.id);
  const current = routeProjectId.value;
  if (current && ids.includes(current)) {
    selectedProjectId.value = current;
    saveLastProjectId(current);
    return;
  }

  const initial = resolveInitialProjectId(readLastProjectId(), ids);
  selectedProjectId.value = initial;
  saveLastProjectId(initial);
  if (!initial) {
    // 无项目时回到布局根路径，展示空态
    if (current) await router.replace('/platform/pm');
    return;
  }
  // 首次进入 /platform/pm 或项目已不存在：跳到初始项目的当前 Tab
  await router.replace({
    name: resolveTabRoute(String(route.name || '')),
    params: { projectId: initial },
  });
}

// 子页面内跳转（如从项目 A 的版本详情回到列表）时同步下拉选中值
watch(routeProjectId, (projectId) => {
  if (!projectId || projectId === selectedProjectId.value) return;
  selectedProjectId.value = projectId;
  saveLastProjectId(projectId);
});

function onProjectChange(value: unknown) {
  const projectId = String(value || '');
  if (!projectId || projectId === routeProjectId.value) return;
  saveLastProjectId(projectId);
  // 切换项目时保持当前 Tab，详情页归一到所属列表页
  router.push({
    name: resolveTabRoute(String(route.name || '')),
    params: { projectId },
  });
}

function onProjectCreated(project: PmProject) {
  projectStore.upsert(project);
  selectedProjectId.value = project.id;
  saveLastProjectId(project.id);
  router.push({
    name: resolveTabRoute(String(route.name || '')),
    params: { projectId: project.id },
  });
}

function onMoreAction(data: { value?: string | number }) {
  if (data.value === 'create') {
    createVisible.value = true;
    return;
  }
  if (data.value === 'delete') askDelete();
}

function askDelete() {
  const project = projects.value.find((item) => item.id === selectedProjectId.value);
  if (!project) return;
  confirmDanger({
    title: t('pm.projects.deleteConfirmTitle'),
    body: t('pm.projects.deleteConfirmBody', { name: project.name }),
    confirmText: t('pm.projects.delete'),
    onConfirm: () => removeProject(project),
  });
}

async function removeProject(project: PmProject) {
  try {
    await deletePmProject(project.id);
  } catch {
    MessagePlugin.error(t('pm.projects.deleteFailed'));
    return;
  }
  projectStore.remove(project.id);
  MessagePlugin.success(t('pm.projects.deleteSuccess'));
  if (project.id !== selectedProjectId.value) return;

  // 删的是当前项目：顺延到剩余首个项目，没有则回到空态
  const next = projects.value[0];
  selectedProjectId.value = next?.id || '';
  saveLastProjectId(selectedProjectId.value);
  await router.replace(
    next
      ? { name: resolveTabRoute(String(route.name || '')), params: { projectId: next.id } }
      : '/platform/pm',
  );
}

// 版本详情 / 资料预览分别归属「版本管理」「项目资料」
function isNavActive(name: string): boolean {
  return resolveTabRoute(String(route.name || '')) === name;
}
</script>

<style lang="less" scoped>
.pm-shell {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  background: var(--td-bg-color-container);
}

/* ---------- 顶部：左侧 Tab 导航 + 右侧项目下拉与更多操作 ---------- */
.pm-topbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--app-space-4);
  padding: var(--app-space-2) var(--app-space-5);
  border-bottom: 1px solid var(--td-component-stroke);
}

.pm-picker {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  min-width: 0;
}

.pm-picker-label {
  flex: 0 0 auto;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
}

.pm-picker-select {
  width: 240px;
}

/* ---------- Tab 导航：浅灰胶囊容器 + 白色激活态 ---------- */
.pm-tabs {
  flex: 0 1 auto;
  min-width: 0;
  display: inline-flex;
  align-items: center;
  gap: 2px;
  padding: 3px;
  border-radius: 10px;
  background: var(--td-bg-color-secondarycontainer);
  overflow-x: auto;
  overflow-y: hidden;
  scrollbar-width: none;

  &::-webkit-scrollbar {
    display: none;
  }
}

.pm-tab {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 6px 14px;
  border-radius: 8px;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
  text-decoration: none;
  white-space: nowrap;
  transition:
    color var(--app-motion-fast),
    background-color var(--app-motion-fast);

  &:hover:not(.is-active) {
    color: var(--td-text-color-primary);
    background: var(--td-bg-color-container);
  }

  &.is-active {
    color: var(--td-brand-color);
    font-weight: 600;
    background: var(--td-bg-color-container);
    box-shadow: 0 1px 2px rgba(0, 0, 0, 0.06), 0 1px 6px rgba(0, 0, 0, 0.04);
  }

  &.is-disabled {
    color: var(--td-text-color-disabled);
    cursor: not-allowed;
  }
}

/* ---------- 内容区 ---------- */
.pm-main {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.pm-empty {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--app-space-2);
  padding: var(--app-space-8);
  text-align: center;
}

.pm-empty-title {
  font-size: var(--app-text-xl);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.pm-empty-hint {
  margin-bottom: var(--app-space-2);
  font-size: var(--app-text-md);
  color: var(--td-text-color-secondary);
}
</style>
