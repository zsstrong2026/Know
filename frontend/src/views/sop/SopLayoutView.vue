<template>
  <!-- SOP 工作台壳：顶部五 Tab + 右侧项目切换/更多菜单，内容区为子路由出口 -->
  <div class="sop-shell">
    <header class="sop-topbar">
      <nav class="sop-tabs">
        <RouterLink
          v-for="tab in tabs"
          :key="tab.name"
          class="sop-tab"
          :class="{ 'is-active': activeTab === tab.key }"
          :to="{ name: tab.name }"
        >
          <t-icon :name="tab.icon" size="16px" />
          <span>{{ t(tab.labelKey) }}</span>
          <span v-if="tab.key === 'approvals' && approvalCount > 0" class="sop-tab-badge">
            {{ approvalCount }}
          </span>
        </RouterLink>
      </nav>

      <div class="sop-actions">
        <ProjectSwitcher @create="createVisible = true" />
        <t-dropdown
          :options="moreOptions"
          trigger="click"
          placement="bottom-right"
          attach="body"
          @click="onMoreAction"
        >
          <t-button variant="text" shape="square" :aria-label="t('sop.shell.more')">
            <template #icon><t-icon name="more" /></template>
          </t-button>
        </t-dropdown>
      </div>
    </header>

    <section class="sop-main">
      <div v-if="!loadingProjects && !projects.length" class="sop-empty">
        <div class="sop-empty-title">{{ t('sop.shell.noProject') }}</div>
        <div class="sop-empty-hint">{{ t('sop.shell.noProjectHint') }}</div>
        <t-button theme="primary" @click="createVisible = true">
          {{ t('sop.project.create') }}
        </t-button>
      </div>
      <RouterView v-else />
    </section>

    <ProjectCreateModal v-model:visible="createVisible" @created="onProjectCreated" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute, useRouter } from 'vue-router';
import { storeToRefs } from 'pinia';
import { MessagePlugin } from 'tdesign-vue-next';
import { useSopStore } from '@/stores/sop';
import ProjectSwitcher from './components/ProjectSwitcher.vue';
import ProjectCreateModal from './components/ProjectCreateModal.vue';

const tabs = [
  { key: 'items', name: 'sopItems', labelKey: 'sop.shell.tabItems', icon: 'task' },
  { key: 'templates', name: 'sopTemplates', labelKey: 'sop.shell.tabTemplates', icon: 'layers' },
  { key: 'runs', name: 'sopRuns', labelKey: 'sop.shell.tabRuns', icon: 'play-circle' },
  { key: 'approvals', name: 'sopApprovals', labelKey: 'sop.shell.tabApprovals', icon: 'check-circle' },
  { key: 'tasks', name: 'sopTasks', labelKey: 'sop.shell.tabTasks', icon: 'time' },
] as const;

const router = useRouter();
const route = useRoute();
const { t } = useI18n();
const store = useSopStore();
const { projects, loadingProjects, approvalCount } = storeToRefs(store);

const createVisible = ref(false);
let addedWhileMounted = true;
let stopApprovals: (() => void) | null = null;

const activeTab = computed(() => (route.meta.sopTab as string) || 'items');

const moreOptions = computed(() => [
  { content: t('sop.settings.projectTitle'), value: 'project' },
  { content: t('sop.shell.executorSettings'), value: 'settings' },
  { content: t('sop.shell.pmArchive'), value: 'archive' },
]);

onMounted(async () => {
  addedWhileMounted = true;
  try {
    await store.loadProjects();
  } catch {
    if (addedWhileMounted) MessagePlugin.error(t('sop.shell.loadProjectsFailed'));
    return;
  }
  if (!store.currentProjectId || !addedWhileMounted) return;
  await Promise.all([
    // 统计带与审批角标各自兜底：任一失败都不该让工作台停在「加载失败」
    store.loadStats().catch(() => undefined),
    store.loadApprovals().catch(() => undefined),
  ]);
  // 组件可能在请求期间已卸载：此时绝不能再起 interval（否则永远清不掉）
  if (!addedWhileMounted) return;
  stopApprovals = store.startPolling(() => store.loadApprovals(), 60_000);
});

onUnmounted(() => {
  addedWhileMounted = false;
  stopApprovals?.();
});

// 切换项目时由 store.switchProject 负责重载审批，这里不再重复请求

function onMoreAction(data: { value?: string | number }) {
  if (data.value === 'project') {
    void router.push({ name: 'sopProjectSettings' });
    return;
  }
  if (data.value === 'settings') {
    void router.push({ name: 'sopSettings' });
    return;
  }
  if (data.value === 'archive') {
    void router.push({ name: 'sopArchive' });
  }
}

function onProjectCreated() {
  const name = store.currentProject?.name || '';
  MessagePlugin.success(t('sop.project.createSuccess', { name }));
  void router.push({ name: 'sopItems' });
}
</script>

<style lang="less" scoped>
.sop-shell {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  background: var(--td-bg-color-container);
}

.sop-topbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--app-space-4);
  padding: var(--app-space-2) var(--app-space-5);
  border-bottom: 1px solid var(--td-component-stroke);
}

.sop-tabs {
  flex: 0 1 auto;
  min-width: 0;
  display: inline-flex;
  align-items: center;
  gap: 2px;
  padding: 3px;
  border-radius: var(--app-radius-lg);
  background: var(--td-bg-color-secondarycontainer);
  overflow-x: auto;
  overflow-y: hidden;
  scrollbar-width: none;

  &::-webkit-scrollbar {
    display: none;
  }
}

.sop-tab {
  position: relative;
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 6px 14px;
  border-radius: var(--app-radius-md);
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
}

.sop-tab-badge {
  min-width: 18px;
  height: 18px;
  padding: 0 5px;
  border-radius: var(--app-radius-pill);
  background: var(--td-error-color);
  color: #fff;
  font-size: var(--app-text-xs);
  font-weight: 600;
  line-height: 18px;
  text-align: center;
}

.sop-actions {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
}

.sop-main {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: auto;
}

.sop-empty {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--app-space-2);
  padding: 48px 24px;
}

.sop-empty-title {
  font-size: var(--app-text-lg);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.sop-empty-hint {
  margin-bottom: var(--app-space-3);
  color: var(--td-text-color-secondary);
}
</style>
