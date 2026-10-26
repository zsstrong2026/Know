<template>
  <div class="pm-features">
    <!-- 工具栏：新增功能点 / 搜索 / 迭代版本 / 来源 / 仅看已上线（对齐原型） -->
    <header class="ft-toolbar">
      <t-button theme="primary" :disabled="loading" @click="dialogsRef?.openCreate()">
        ＋ {{ $t('pm.features.create') }}
      </t-button>

      <span class="ft-summary">
        {{ $t('pm.features.summary', summary) }}
      </span>

      <span class="ft-spacer" />

      <div class="ft-filters">
        <t-input
          v-model="filter.keyword"
          class="ft-filter ft-filter--search"
          :placeholder="$t('pm.features.filters.keyword')"
          clearable
        >
          <template #prefix-icon><t-icon name="search" /></template>
        </t-input>
        <t-select
          v-model="filter.versionNo"
          class="ft-filter"
          :placeholder="$t('pm.features.filters.version')"
          :options="versionFilterOptions"
          clearable
        />
        <t-select
          v-model="filter.source"
          class="ft-filter"
          :placeholder="$t('pm.features.filters.source')"
          :options="sourceOptions"
          clearable
        />
        <t-checkbox v-model="filter.onlyLaunched">
          {{ $t('pm.features.filters.onlyLaunched') }}
        </t-checkbox>
        <t-button variant="text" size="small" @click="resetFilter">
          {{ $t('pm.features.filters.reset') }}
        </t-button>
      </div>
    </header>

    <div v-if="loading && !tree.length" class="ft-state"><t-loading /></div>

    <!-- 空态：区分「还没有功能点」与「筛选无结果」 -->
    <div v-else-if="!tree.length" class="ft-state ft-empty">
      <t-icon name="view-list" size="40px" />
      <div class="ft-empty-title">{{ $t('pm.features.empty.title') }}</div>
      <div class="ft-empty-hint">{{ $t('pm.features.empty.hint') }}</div>
      <t-button theme="primary" variant="outline" @click="dialogsRef?.openCreate()">
        {{ $t('pm.features.create') }}
      </t-button>
    </div>
    <div v-else-if="!tableRows.length" class="ft-state ft-empty">
      <t-icon name="search" size="40px" />
      <div class="ft-empty-title">{{ $t('pm.features.empty.filteredTitle') }}</div>
      <div class="ft-empty-hint">{{ $t('pm.features.empty.filteredHint') }}</div>
      <t-button variant="outline" @click="resetFilter">
        {{ $t('pm.features.filters.reset') }}
      </t-button>
    </div>

    <div v-else class="ft-body">
      <t-enhanced-table
        row-key="key"
        size="medium"
        hover
        :data="tableRows"
        :columns="columns"
        :tree="treeConfig"
        :loading="loading"
      >
        <!-- 树列：模块 / 功能点 / 操作项三级名称 -->
        <template #name="{ row }">
          <span :class="['ft-name', `ft-name--${row.level}`]" :title="row.name">{{ row.name }}</span>
        </template>
        <template #requirement="{ row }">
          <span class="ft-muted">{{ requirementLabel(row) }}</span>
        </template>
        <!-- 叶子展示最新迭代版本，模块/功能节点不重复展示 -->
        <template #version="{ row }">
          <template v-if="row.level !== 'module' && row.children?.length === undefined">
            <span class="ft-version">{{ featureVersionLabel(row.latest_version_no) || '—' }}</span>
            <t-tag
              v-if="row.latest_version_status && versionMeta(row.latest_version_status)"
              class="ft-version-tag"
              :theme="versionMeta(row.latest_version_status)!.theme"
              variant="light"
              size="small"
            >
              {{ $t(versionMeta(row.latest_version_status)!.labelKey) }}
            </t-tag>
          </template>
          <span v-else class="ft-muted">—</span>
        </template>
        <template #source="{ row }">
          <t-tag
            v-if="row.level !== 'module' && sourceMeta(row.source)"
            :theme="sourceMeta(row.source)!.theme"
            variant="light"
            size="small"
          >
            {{ $t(sourceMeta(row.source)!.labelKey) }}
          </t-tag>
          <span v-else class="ft-muted">—</span>
        </template>
        <template #status="{ row }">
          <t-tag
            v-if="row.level !== 'module'"
            :theme="statusMeta(row.status).theme"
            variant="light"
            size="small"
          >
            {{ $t(statusMeta(row.status).labelKey) }}
          </t-tag>
          <span v-else class="ft-muted">—</span>
        </template>
        <template #actions="{ row }">
          <t-button
            v-if="row.feature_id && !row.children?.length"
            variant="text"
            size="small"
            @click="dialogsRef?.openHistory(row)"
          >
            {{ $t('pm.features.actions.history') }}
          </t-button>
        </template>
      </t-enhanced-table>

      <p class="ft-note">{{ $t('pm.features.note') }}</p>
    </div>

    <PmFeatureDialogs
      ref="dialogsRef"
      :project-id="projectId"
      :modules="modules"
      :requirements="requirements"
      :versions="versions"
      @changed="reload"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute } from 'vue-router';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  listPmFeatures,
  listPmRequirements,
  listPmVersions,
  type PmFeatureNode,
  type PmRequirement,
  type PmVersion,
} from '@/api/pm';
import { VERSION_STATUS_MAP } from './pmStatus';
import {
  FEATURE_SOURCES,
  FEATURE_SOURCE_MAP,
  FEATURE_STATUS_MAP,
  UNCLASSIFIED,
  emptyFeatureFilter,
  featureVersionLabel,
  filterFeatureTree,
  normalizeFeatureTree,
  summarizeFeatureTree,
  type FeatureFilterState,
  type FeatureTreeNode,
} from './pmFeatureTree';
import PmFeatureDialogs from './PmFeatureDialogs.vue';

// 表格行：叶子去掉空 children，避免出现无意义的展开箭头
interface FeatureTableRow extends Omit<FeatureTreeNode, 'children'> {
  children?: FeatureTableRow[];
}

const route = useRoute();
const { t } = useI18n();

const projectId = computed(() => String(route.params.projectId || ''));

const loading = ref(false);
const tree = ref<FeatureTreeNode[]>([]);
const versions = ref<PmVersion[]>([]);
const requirements = ref<PmRequirement[]>([]);
const filter = reactive<FeatureFilterState>(emptyFeatureFilter());

const dialogsRef = ref<InstanceType<typeof PmFeatureDialogs> | null>(null);

const treeConfig = {
  childrenKey: 'children',
  treeNodeColumnIndex: 0,
  defaultExpandAll: true,
  indent: 20,
};

const columns = computed(() => [
  { colKey: 'name', title: t('pm.features.columns.name'), minWidth: 260 },
  { colKey: 'requirement', title: t('pm.features.columns.requirement'), width: 150, ellipsis: true },
  { colKey: 'version', title: t('pm.features.columns.version'), width: 190 },
  { colKey: 'source', title: t('pm.features.columns.source'), width: 110 },
  { colKey: 'status', title: t('pm.features.columns.status'), width: 100 },
  { colKey: 'actions', title: t('pm.features.columns.actions'), width: 110, fixed: 'right' },
]);

const sourceOptions = computed(() =>
  FEATURE_SOURCES.map((value) => ({
    label: t(FEATURE_SOURCE_MAP[value].labelKey),
    value,
  })),
);

// 只有已分配版本号的历史版本才可筛选
const versionFilterOptions = computed(() =>
  versions.value
    .filter((item) => item.version_no)
    .map((item) => ({
      label: featureVersionLabel(item.version_no),
      value: item.version_no,
    })),
);

// 顶层模块名去重，供新增弹窗下拉选择
const modules = computed(() =>
  Array.from(new Set(tree.value.map((node) => node.name).filter((name) => name !== UNCLASSIFIED))),
);

const summary = computed(() => {
  const result = summarizeFeatureTree(tree.value);
  return {
    modules: result.moduleCount,
    features: result.featureCount,
    leaves: result.leafCount,
    launched: result.launchedCount,
  };
});

// 需求 ID → 标题，避免每行再查一次
const requirementMap = computed(
  () => new Map(requirements.value.map((item) => [item.id, item.title])),
);

// 筛选后转表格行，移除空 children
const tableRows = computed<FeatureTableRow[]>(() => {
  const rows = filterFeatureTree(tree.value, filter);
  const strip = (list: FeatureTreeNode[]): FeatureTableRow[] =>
    list.map(({ children, ...rest }) => ({
      ...rest,
      children: children.length ? strip(children) : undefined,
    }));
  return strip(rows);
});

function resetFilter() {
  Object.assign(filter, emptyFeatureFilter());
}

function sourceMeta(source?: string) {
  return FEATURE_SOURCE_MAP[source as keyof typeof FEATURE_SOURCE_MAP];
}

function statusMeta(status?: string) {
  return FEATURE_STATUS_MAP[status as keyof typeof FEATURE_STATUS_MAP] ?? FEATURE_STATUS_MAP.planned;
}

function versionMeta(status?: string) {
  return VERSION_STATUS_MAP[status ?? ''];
}

function requirementLabel(row: FeatureTableRow): string {
  // 模块/功能节点没有直接来源需求，仅在叶子展示
  if (row.level === 'module' || row.children?.length) return '—';
  return requirementMap.value.get(row.requirement_id) ?? '—';
}

// ===== 数据加载 =====

async function loadFeatures() {
  if (!projectId.value) return;
  loading.value = true;
  try {
    const nodes = await listPmFeatures(projectId.value);
    // 后端已按 parent_id 组好三级树，这里只做字段归一
    tree.value = normalizeFeatureTree(nodes as PmFeatureNode[]);
  } catch {
    tree.value = [];
    MessagePlugin.error(t('pm.features.loadFailed'));
  } finally {
    loading.value = false;
  }
}

async function loadLookups() {
  if (!projectId.value) return;
  try {
    const [versionList, requirementList] = await Promise.all([
      listPmVersions(projectId.value),
      listPmRequirements(projectId.value),
    ]);
    versions.value = versionList;
    requirements.value = requirementList;
  } catch {
    // 候选数据加载失败不阻塞树展示，仅影响需求名与版本下拉
    versions.value = [];
    requirements.value = [];
  }
}

async function reload() {
  await loadFeatures();
}

onMounted(() => {
  void loadFeatures();
  void loadLookups();
});

// 切换项目时组件复用，清空旧数据与筛选
watch(projectId, () => {
  tree.value = [];
  versions.value = [];
  requirements.value = [];
  Object.assign(filter, emptyFeatureFilter());
  void loadFeatures();
  void loadLookups();
});
</script>

<style lang="less" scoped>
.pm-features {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  padding: var(--app-space-6);
}

/* ---------- 工具栏 ---------- */
.ft-toolbar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--app-space-2);
  margin-bottom: var(--app-space-4);
}

.ft-summary {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
}

.ft-spacer {
  flex: 1;
}

.ft-filters {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--app-space-2);
}

.ft-filter {
  width: 132px;

  &--search {
    width: 200px;
  }
}

/* ---------- 状态与空态 ---------- */
.ft-state {
  flex: 1;
  min-height: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}

.ft-empty {
  flex-direction: column;
  gap: var(--app-space-2);
  border: 1px dashed var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-placeholder);
}

.ft-empty-title {
  font-size: var(--app-text-lg);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.ft-empty-hint {
  margin-bottom: var(--app-space-2);
  font-size: var(--app-text-md);
}

/* ---------- 表格 ---------- */
.ft-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
}

.ft-name {
  color: var(--td-text-color-primary);

  &--module {
    font-weight: 600;
  }

  &--feature {
    font-weight: 500;
  }

  &--operation {
    color: var(--td-text-color-secondary);
  }
}

.ft-version {
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.ft-version-tag {
  margin-left: var(--app-space-2);
}

.ft-muted {
  color: var(--td-text-color-placeholder);
}

.ft-note {
  margin: var(--app-space-3) 0 0;
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}
</style>
