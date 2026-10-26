<template>
  <div class="pm-requirements">
    <!-- 工具栏：新建需求 / 视图切换 / 右侧筛选（对齐原型） -->
    <header class="rq-toolbar">
      <t-button theme="primary" :disabled="loading" @click="openCreate">
        ＋ {{ $t('pm.requirements.create') }}
      </t-button>
      <span class="rq-divider" />
      <button
        type="button"
        class="rq-view-btn"
        :class="{ 'is-on': view === 'list' }"
        @click="view = 'list'"
      >
        {{ $t('pm.requirements.view.list') }}
      </button>
      <button
        type="button"
        class="rq-view-btn"
        :class="{ 'is-on': view === 'kanban' }"
        @click="view = 'kanban'"
      >
        {{ $t('pm.requirements.view.kanban') }}
      </button>

      <span class="rq-spacer" />

      <div class="rq-filters">
        <t-input
          v-model="filter.keyword"
          class="rq-filter rq-filter--search"
          :placeholder="$t('pm.requirements.filters.keyword')"
          clearable
        >
          <template #prefix-icon><t-icon name="search" /></template>
        </t-input>
        <t-select
          v-model="filter.priority"
          class="rq-filter"
          :placeholder="$t('pm.requirements.filters.priority')"
          :options="priorityOptions"
          clearable
        />
        <t-select
          v-model="filter.status"
          class="rq-filter"
          :placeholder="$t('pm.requirements.filters.status')"
          :options="statusOptions"
          clearable
        />
        <t-select
          v-model="filter.source"
          class="rq-filter"
          :placeholder="$t('pm.requirements.filters.source')"
          :options="sourceOptions"
          clearable
        />
        <t-select
          v-model="filter.valueAchieved"
          class="rq-filter"
          :placeholder="$t('pm.requirements.filters.value')"
          :options="valueOptions"
          clearable
        />
        <t-select
          v-model="filter.sort"
          class="rq-filter rq-filter--sort"
          :options="sortOptions"
        />
        <t-button variant="text" size="small" @click="resetFilter">
          {{ $t('pm.requirements.filters.reset') }}
        </t-button>
      </div>
    </header>

    <!-- 加载中 -->
    <div v-if="loading && !requirements.length" class="rq-state">
      <t-loading />
    </div>

    <!-- 空态：区分「还没有需求」与「筛选无结果」 -->
    <div v-else-if="!requirements.length" class="rq-state rq-empty">
      <t-icon name="task" size="40px" />
      <div class="rq-empty-title">{{ $t('pm.requirements.empty.title') }}</div>
      <div class="rq-empty-hint">{{ $t('pm.requirements.empty.hint') }}</div>
      <t-button theme="primary" variant="outline" @click="openCreate">
        {{ $t('pm.requirements.create') }}
      </t-button>
    </div>
    <div v-else-if="!filteredList.length" class="rq-state rq-empty">
      <t-icon name="search" size="40px" />
      <div class="rq-empty-title">{{ $t('pm.requirements.emptyFiltered.title') }}</div>
      <div class="rq-empty-hint">{{ $t('pm.requirements.emptyFiltered.hint') }}</div>
      <t-button variant="outline" @click="resetFilter">
        {{ $t('pm.requirements.filters.reset') }}
      </t-button>
    </div>

    <!-- 列表视图 -->
    <div v-else-if="view === 'list'" class="rq-body">
      <t-table
        row-key="id"
        :data="filteredList"
        :columns="columns"
        :loading="loading"
        size="medium"
        hover
      >
        <template #title="{ row }">
          <span class="rq-title" :title="row.title">{{ row.title }}</span>
        </template>
        <template #priority="{ row }">
          <t-tag :theme="priorityTheme(row.priority)" variant="light" size="small">
            {{ row.priority }}
          </t-tag>
        </template>
        <template #status="{ row }">
          <t-tag :theme="statusMeta(row.status).theme" variant="light" size="small">
            {{ $t(statusMeta(row.status).labelKey) }}
          </t-tag>
        </template>
        <template #source="{ row }">{{ $t(sourceLabelKey(row.source)) }}</template>
        <template #proposer="{ row }">{{ row.proposer || '—' }}</template>
        <template #description="{ row }">
          <span class="rq-desc" :title="row.description">{{ row.description || '—' }}</span>
        </template>
        <template #version>
          <span class="rq-muted">{{ $t('pm.requirements.versionUnassigned') }}</span>
        </template>
        <template #valueAchieved="{ row }">
          <t-tag v-if="row.value_achieved" :theme="valueTheme(row.value_achieved)" variant="light" size="small">
            {{ $t(valueLabelKey(row.value_achieved)) }}
          </t-tag>
          <span v-else class="rq-muted">—</span>
        </template>
        <template #updatedAt="{ row }">{{ formatPmTime(row.updated_at) }}</template>
        <template #actions="{ row }">
          <div class="rq-ops">
            <t-button variant="text" size="small" @click="openDetail(row)">
              {{ $t('pm.requirements.actions.detail') }}
            </t-button>
            <t-button v-if="canEdit(row)" variant="text" size="small" @click="openEdit(row)">
              {{ $t('pm.requirements.actions.edit') }}
            </t-button>
            <t-button v-if="canVerify(row.status)" variant="text" size="small" @click="openDetail(row)">
              {{ $t('pm.requirements.actions.verify') }}
            </t-button>
            <t-button v-if="canReactivate(row.status)" variant="text" size="small" @click="reactivate(row)">
              {{ $t('pm.requirements.actions.reactivate') }}
            </t-button>
            <t-button theme="danger" variant="text" size="small" @click="remove(row)">
              {{ $t('pm.requirements.actions.delete') }}
            </t-button>
          </div>
        </template>
      </t-table>
    </div>

    <!-- 看板视图：按状态分列展示，状态由需求进度（确认 / 版本流转）自动变更 -->
    <div v-else class="rq-body">
      <div class="rq-kanban">
        <section v-for="column in boardColumns" :key="column.status" class="rq-col">
          <div class="rq-col-head">
            <span class="rq-col-name">{{ $t(statusMeta(column.status).labelKey) }}</span>
            <span class="rq-col-count">{{ column.items.length }}</span>
          </div>
          <div
            v-for="item in column.items"
            :key="item.id"
            class="rq-card"
            @click="openDetail(item)"
          >
            <span class="rq-card-title" :title="item.title">{{ item.title }}</span>
            <!-- 卡片右上角删除入口：阻止冒泡，避免同时打开详情 -->
            <button
              type="button"
              class="rq-card-del"
              :title="$t('pm.requirements.actions.delete')"
              :aria-label="$t('pm.requirements.actions.delete')"
              @click.stop="remove(item)"
            >
              <t-icon name="delete" />
            </button>
            <!-- 次行与参考图一致：状态胶囊 + 图标 + 元信息；已上线优先展示价值结论 -->
            <div class="rq-card-meta">
              <span class="rq-pill" :class="`rq-pill--${cardPillTheme(item)}`">{{ cardBadge(item) }}</span>
              <span class="rq-card-source">
                <t-icon name="link" />
                {{ $t('pm.requirements.columns.source') }}：{{ $t(sourceLabelKey(item.source)) }}
              </span>
            </div>
          </div>
        </section>
      </div>
    </div>

    <!-- 新建 / 编辑 -->
    <PmRequirementForm
      v-model:visible="formVisible"
      :project-id="projectId"
      :requirement="editing"
      @saved="reload"
    />

    <!-- 详情与上线验证 -->
    <PmRequirementDetail
      v-model:visible="detailVisible"
      :requirement="detailRequirement"
      @changed="reload"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute } from 'vue-router';
import { MessagePlugin, DialogPlugin } from 'tdesign-vue-next';
import {
  deletePmRequirement,
  listPmRequirements,
  reactivatePmRequirement,
  type PmRequirement,
} from '@/api/pm';
import { REQUIREMENT_STATUS_MAP } from './pmStatus';
import {
  REQUIREMENT_PRIORITIES,
  REQUIREMENT_SORTS,
  REQUIREMENT_SOURCES,
  REQUIREMENT_STATUSES,
  REQUIREMENT_VALUES,
  canReactivate,
  canVerify,
  emptyRequirementFilter,
  filterRequirements,
  formatPmTime,
  groupByStatus,
  priorityTheme,
  sortRequirements,
  sourceLabelKey,
  valueLabelKey,
  valueTheme,
  type RequirementFilterState,
  type RequirementSort,
} from './pmRequirementState';
import PmRequirementForm from './PmRequirementForm.vue';
import PmRequirementDetail from './PmRequirementDetail.vue';

const route = useRoute();
const { t } = useI18n();

const projectId = computed(() => String(route.params.projectId || ''));

const loading = ref(false);
const requirements = ref<PmRequirement[]>([]);
const view = ref<'list' | 'kanban'>('list');
const filter = reactive<RequirementFilterState>(emptyRequirementFilter());

const formVisible = ref(false);
const editing = ref<PmRequirement | null>(null);
const detailVisible = ref(false);
const detailRequirement = ref<PmRequirement | null>(null);

const priorityOptions = REQUIREMENT_PRIORITIES.map((value) => ({ label: value, value }));
const statusOptions = computed(() => REQUIREMENT_STATUSES.map((value) => ({
  label: t(REQUIREMENT_STATUS_MAP[value].labelKey),
  value,
})));
const sourceOptions = REQUIREMENT_SOURCES.map((value) => ({
  label: t(`pm.requirements.source.${value}`),
  value,
}));
const valueOptions = REQUIREMENT_VALUES.map((value) => ({
  label: t(`pm.requirements.value.${value}`),
  value,
}));
const sortOptions = REQUIREMENT_SORTS.map((value: RequirementSort) => ({
  label: t(`pm.requirements.sort.${value}`),
  value,
}));

const columns = computed(() => [
  { colKey: 'title', title: t('pm.requirements.columns.title'), width: 220, ellipsis: true },
  { colKey: 'priority', title: t('pm.requirements.columns.priority'), width: 88 },
  { colKey: 'status', title: t('pm.requirements.columns.status'), width: 112 },
  { colKey: 'source', title: t('pm.requirements.columns.source'), width: 110 },
  { colKey: 'proposer', title: t('pm.requirements.columns.proposer'), width: 96, ellipsis: true },
  { colKey: 'description', title: t('pm.requirements.columns.description'), minWidth: 200, ellipsis: true },
  { colKey: 'version', title: t('pm.requirements.columns.version'), width: 96 },
  { colKey: 'valueAchieved', title: t('pm.requirements.columns.value'), width: 100 },
  { colKey: 'updatedAt', title: t('pm.requirements.columns.updatedAt'), width: 152 },
  { colKey: 'actions', title: t('pm.requirements.columns.actions'), width: 280, fixed: 'right' },
]);

// 筛选 + 排序全部在客户端完成，避免每次输入都请求接口
const filteredList = computed(() => (
  sortRequirements(filterRequirements(requirements.value, filter), filter.sort)
));

// 看板列与需求状态一一对应，顺序固定
const boardColumns = computed(() => groupByStatus(filteredList.value, REQUIREMENT_STATUSES));

// 卡片胶囊：已上线且有验证结论时展示价值，否则展示优先级
function cardBadge(item: PmRequirement): string {
  if (item.status === 'launched' && item.value_achieved) {
    return t(valueLabelKey(item.value_achieved));
  }
  return item.priority;
}

// 胶囊配色与详情页保持一致：价值结论优先，其次按优先级着色
function cardPillTheme(item: PmRequirement): string {
  if (item.status === 'launched' && item.value_achieved) {
    return valueTheme(item.value_achieved);
  }
  return priorityTheme(item.priority);
}

function statusMeta(status: string) {
  return REQUIREMENT_STATUS_MAP[status] ?? REQUIREMENT_STATUS_MAP.pending_analysis;
}

// 已上线/终止态不允许再改基础信息，避免与流程状态冲突
function canEdit(item: PmRequirement): boolean {
  return item.status !== 'launched' && !canReactivate(item.status);
}

// ===== 数据加载 =====

async function loadRequirements() {
  if (!projectId.value) return;
  loading.value = true;
  try {
    requirements.value = await listPmRequirements(projectId.value);
  } catch {
    requirements.value = [];
    MessagePlugin.error(t('pm.requirements.loadFailed'));
  } finally {
    loading.value = false;
  }
}

// 刷新后同步详情抽屉里的对象，避免验证结果展示陈旧
async function reload() {
  await loadRequirements();
  const current = detailRequirement.value;
  if (!current) return;
  const next = requirements.value.find((item) => item.id === current.id);
  if (next) detailRequirement.value = next;
}

onMounted(() => void loadRequirements());

// 切换项目时组件复用，需要清空旧的筛选与弹窗状态
watch(projectId, () => {
  requirements.value = [];
  Object.assign(filter, emptyRequirementFilter());
  formVisible.value = false;
  detailVisible.value = false;
  void loadRequirements();
});

// ===== 筛选 =====

function resetFilter() {
  Object.assign(filter, emptyRequirementFilter());
}

// ===== 弹窗入口 =====

function openCreate() {
  editing.value = null;
  formVisible.value = true;
}

function openEdit(item: PmRequirement) {
  editing.value = item;
  formVisible.value = true;
}

function openDetail(item: PmRequirement) {
  detailRequirement.value = item;
  detailVisible.value = true;
}

// ===== 操作 =====

async function reactivate(item: PmRequirement) {
  try {
    await reactivatePmRequirement(item.id);
    MessagePlugin.success(t('pm.requirements.reactivateSuccess'));
    await reload();
  } catch {
    MessagePlugin.error(t('pm.requirements.reactivateFailed'));
  }
}

// 删除需求：二次确认后调用接口；若详情抽屉正展示该需求则一并关闭
function remove(item: PmRequirement) {
  const dialog = DialogPlugin.confirm({
    header: t('pm.requirements.deleteConfirmTitle'),
    body: t('pm.requirements.deleteConfirmBody', { title: item.title }),
    confirmBtn: { content: t('pm.requirements.actions.delete'), theme: 'danger' as const },
    cancelBtn: t('pm.requirements.cancel'),
    onConfirm: async () => {
      dialog.hide();
      try {
        await deletePmRequirement(item.id);
        MessagePlugin.success(t('pm.requirements.deleteSuccess'));
        if (detailRequirement.value?.id === item.id) detailVisible.value = false;
        await reload();
      } catch {
        MessagePlugin.error(t('pm.requirements.deleteFailed'));
      }
    },
  });
}
</script>

<style lang="less" scoped>
.pm-requirements {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  padding: var(--app-space-6);
}

/* ---------- 工具栏 ---------- */
.rq-toolbar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--app-space-2);
  margin-bottom: var(--app-space-4);
}

.rq-divider {
  width: 1px;
  height: 20px;
  background: var(--td-component-stroke);
}

.rq-view-btn {
  padding: 5px 14px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  cursor: pointer;
  transition: border-color var(--app-motion-fast), color var(--app-motion-fast);

  &:hover {
    border-color: var(--td-brand-color);
    color: var(--td-brand-color);
  }

  &.is-on {
    border-color: var(--td-brand-color);
    background: color-mix(in srgb, var(--td-brand-color) 8%, transparent);
    color: var(--td-brand-color);
    font-weight: 600;
  }
}

.rq-spacer {
  flex: 1;
}

.rq-filters {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--app-space-2);
}

.rq-filter {
  width: 118px;

  &--search {
    width: 190px;
  }

  &--sort {
    width: 156px;
  }
}

/* ---------- 状态与空态 ---------- */
.rq-state {
  flex: 1;
  min-height: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}

.rq-empty {
  flex-direction: column;
  gap: var(--app-space-2);
  border: 1px dashed var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-placeholder);
}

.rq-empty-title {
  font-size: var(--app-text-lg);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.rq-empty-hint {
  margin-bottom: var(--app-space-2);
  font-size: var(--app-text-md);
}

/* ---------- 列表 ---------- */
.rq-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
}

.rq-title {
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.rq-desc {
  color: var(--td-text-color-secondary);
}

.rq-muted {
  color: var(--td-text-color-placeholder);
}

.rq-ops {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 2px;
}

/* ---------- 看板（参考图：浅色列 + 白卡 + 圆角阴影） ---------- */
/* 7 个状态列等分容器宽度，列宽随视口收缩，避免横向滚动条 */
.rq-kanban {
  display: grid;
  grid-template-columns: repeat(7, minmax(0, 1fr));
  gap: var(--app-space-3);
  align-items: start;
  padding-bottom: var(--app-space-2);
}

/* 中等屏幕改为 4 列两行，保证卡片内文字可读 */
@media (max-width: 1280px) {
  .rq-kanban {
    grid-template-columns: repeat(4, minmax(0, 1fr));
  }
}

/* 窄屏两列，避免单列过窄 */
@media (max-width: 820px) {
  .rq-kanban {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

.rq-col {
  min-width: 0;
  padding: 4px;
  border-radius: var(--app-radius-md);
}

.rq-col-head {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 2px 6px 10px;
}

.rq-col-name {
  font-size: var(--app-text-md);
  font-weight: 700;
  color: var(--td-text-color-primary);
}

.rq-col-count {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
}

.rq-card {
  position: relative;
  padding: 10px 12px;
  margin-bottom: 8px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  background: var(--td-bg-color-container);
  box-shadow: 0 1px 2px rgb(0 0 0 / 4%);
  cursor: pointer;
  transition: border-color var(--app-motion-fast), box-shadow var(--app-motion-fast);

  &:hover {
    border-color: color-mix(in srgb, var(--td-brand-color) 30%, var(--td-component-stroke));
    box-shadow: 0 2px 8px rgb(0 0 0 / 8%);
  }

  // 删除入口默认隐藏，悬停卡片时浮现，避免干扰阅读
  &:hover .rq-card-del,
  .rq-card-del:focus-visible {
    opacity: 1;
  }
}

.rq-card-del {
  position: absolute;
  top: 6px;
  right: 6px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  padding: 0;
  border: none;
  border-radius: 4px;
  background: transparent;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
  opacity: 0;
  transition: opacity var(--app-motion-fast), color var(--app-motion-fast),
    background var(--app-motion-fast);

  &:hover {
    background: var(--td-error-color-1);
    color: var(--td-error-color);
  }
}

.rq-card-title {
  display: block;
  padding-right: 20px;
  font-size: var(--app-text-md);
  font-weight: 600;
  line-height: 20px;
  color: var(--td-text-color-primary);
  word-break: break-word;
}

.rq-card-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  margin-top: 8px;
}

.rq-pill {
  flex: 0 0 auto;
  padding: 1px 8px;
  border-radius: 4px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-xs);
  line-height: 18px;
  white-space: nowrap;
}

// 优先级 / 价值结论配色，与详情页 t-tag(light) 的主题色保持一致
.rq-pill--danger {
  background: var(--td-error-color-1);
  color: var(--td-error-color);
}

.rq-pill--warning {
  background: var(--td-warning-color-1);
  color: var(--td-warning-color);
}

.rq-pill--primary {
  background: var(--td-brand-color-1);
  color: var(--td-brand-color);
}

.rq-pill--success {
  background: var(--td-success-color-1);
  color: var(--td-success-color);
}

.rq-card-source {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  overflow: hidden;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-xs);
  text-overflow: ellipsis;
  white-space: nowrap;

  .t-icon {
    flex: 0 0 auto;
    color: var(--td-text-color-placeholder);
  }
}
</style>
