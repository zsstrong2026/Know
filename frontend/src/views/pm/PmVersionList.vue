<template>
  <div class="pm-versions">
    <!-- 工具栏：新建版本 / 右侧筛选（对齐原型） -->
    <header class="vs-toolbar">
      <t-button theme="primary" :disabled="loading" @click="openCreate">
        ＋ {{ $t('pm.version.create') }}
      </t-button>

      <span class="vs-spacer" />

      <div class="vs-filters">
        <t-input
          v-model="filter.keyword"
          class="vs-filter vs-filter--search"
          :placeholder="$t('pm.version.filters.keyword')"
          clearable
        >
          <template #prefix-icon><t-icon name="search" /></template>
        </t-input>
        <t-select
          v-model="filter.status"
          class="vs-filter"
          :placeholder="$t('pm.version.filters.status')"
          :options="statusOptions"
          clearable
        />
        <t-select
          v-model="filter.created"
          class="vs-filter"
          :placeholder="$t('pm.version.filters.created')"
          :options="createdOptions"
          clearable
        />
        <t-button variant="text" size="small" @click="resetFilter">
          {{ $t('pm.version.filters.reset') }}
        </t-button>
      </div>
    </header>

    <div v-if="loading && !versions.length" class="vs-state">
      <t-loading />
    </div>

    <!-- 空态：区分「还没有版本」与「筛选无结果」 -->
    <div v-else-if="!versions.length" class="vs-state vs-empty">
      <t-icon name="layer" size="40px" />
      <div class="vs-empty-title">{{ $t('pm.version.empty.title') }}</div>
      <div class="vs-empty-hint">{{ $t('pm.version.empty.hint') }}</div>
      <t-button theme="primary" variant="outline" @click="openCreate">
        {{ $t('pm.version.create') }}
      </t-button>
    </div>
    <div v-else-if="!filteredList.length" class="vs-state vs-empty">
      <t-icon name="search" size="40px" />
      <div class="vs-empty-title">{{ $t('pm.version.empty.filteredTitle') }}</div>
      <div class="vs-empty-hint">{{ $t('pm.version.empty.filteredHint') }}</div>
      <t-button variant="outline" @click="resetFilter">
        {{ $t('pm.version.filters.reset') }}
      </t-button>
    </div>

    <div v-else class="vs-body">
      <t-table
        row-key="id"
        :data="filteredList"
        :columns="columns"
        :loading="loading"
        size="medium"
        hover
      >
        <!-- 未定稿版本没有版本号，展示占位并可进入详情 -->
        <template #versionNo="{ row }">
          <t-button variant="text" size="small" @click="openDetail(row)">
            {{ versionDisplayNo(row) || $t('pm.version.unfinalized') }}
          </t-button>
        </template>
        <template #name="{ row }">
          <span class="vs-name" :title="row.name">{{ row.name }}</span>
        </template>
        <template #status="{ row }">
          <t-tag :theme="statusMeta(row.status).theme" variant="light" size="small">
            {{ $t(statusMeta(row.status).labelKey) }}
          </t-tag>
        </template>
        <template #requirementCount="{ row }">{{ row.requirement_count ?? 0 }}</template>
        <template #planDate="{ row }">{{ formatPmDate(row.plan_date) }}</template>
        <template #planLaunchDate="{ row }">{{ formatPmDate(row.plan_launch_date) }}</template>
        <template #finalizeDate="{ row }">{{ formatPmDate(row.finalize_date) }}</template>
        <template #launchDate="{ row }">{{ formatPmDate(row.launch_date) }}</template>
        <template #actions="{ row }">
          <div class="vs-ops">
            <t-button variant="text" size="small" @click="openDetail(row)">
              {{ $t('pm.version.actions.detail') }}
            </t-button>
            <t-button v-if="showGenerate(row)" variant="text" size="small" @click="openDialog('generate', row)">
              {{ $t('pm.version.actions.generate') }}
            </t-button>
            <t-button v-if="canReview(row)" variant="text" size="small" @click="openDialog('review', row)">
              {{ $t('pm.version.actions.review') }}
            </t-button>
            <t-button v-if="canRegenerate(row)" variant="text" size="small" @click="openDialog('regenerate', row)">
              {{ $t('pm.version.actions.regenerate') }}
            </t-button>
            <t-button v-if="canFinalize(row)" variant="text" size="small" @click="openDialog('finalize', row)">
              {{ $t('pm.version.actions.finalize') }}
            </t-button>
            <t-button v-if="canDevelop(row)" variant="text" size="small" @click="changeStatus(row, 'developing')">
              {{ $t('pm.version.actions.develop') }}
            </t-button>
            <t-button v-if="canLaunch(row)" variant="text" size="small" @click="changeStatus(row, 'launched')">
              {{ $t('pm.version.actions.launch') }}
            </t-button>
            <t-button v-if="canShelve(row)" variant="text" size="small" @click="openDialog('shelve', row)">
              {{ $t('pm.version.actions.shelve') }}
            </t-button>
            <t-button v-if="canRestore(row)" variant="text" size="small" @click="changeStatus(row, 'planning')">
              {{ $t('pm.version.actions.restore') }}
            </t-button>
          </div>
        </template>
      </t-table>
    </div>

    <!-- 新建版本 -->
    <t-dialog
      :visible="createVisible"
      :header="$t('pm.version.form.createTitle')"
      width="620px"
      placement="center"
      :footer="false"
      @close="closeCreate"
    >
      <t-form label-align="top">
        <t-form-item :label="$t('pm.version.form.name')" required-mark>
          <t-input
            v-model="draft.name"
            :maxlength="50"
            :placeholder="$t('pm.version.form.namePlaceholder')"
          />
        </t-form-item>
        <t-form-item :label="$t('pm.version.form.planDate')" required-mark>
          <t-date-picker
            v-model="draft.plan_date"
            class="vs-date"
            value-type="YYYY-MM-DD"
            format="YYYY-MM-DD"
            :placeholder="$t('pm.version.form.planDate')"
          />
        </t-form-item>
        <t-form-item :label="$t('pm.version.form.planLaunchDate')" required-mark>
          <t-date-picker
            v-model="draft.plan_launch_date"
            class="vs-date"
            value-type="YYYY-MM-DD"
            format="YYYY-MM-DD"
            :placeholder="$t('pm.version.form.planLaunchDate')"
          />
          <template #help>
            <span class="vs-form-hint">{{ $t('pm.version.form.planLaunchDateHint') }}</span>
          </template>
        </t-form-item>
        <t-form-item :label="$t('pm.version.form.releaseNote')">
          <t-textarea
            v-model="draft.release_note"
            :autosize="{ minRows: 4, maxRows: 4 }"
            :maxlength="500"
            :placeholder="$t('pm.version.form.releaseNotePlaceholder')"
          />
        </t-form-item>
        <t-form-item :label="$t('pm.version.form.requirements')">
          <!-- 需求归纳：仅可选择「已确认」需求，按优先级排序 -->
          <div class="vs-picker">
            <div v-if="loadingRequirements" class="vs-picker__state"><t-loading size="small" /></div>
            <p v-else-if="!confirmedRequirements.length" class="vs-picker__state">
              {{ $t('pm.version.form.requirementsEmpty') }}
            </p>
            <t-checkbox-group v-else v-model="draft.requirement_ids" class="vs-picker__list">
              <t-checkbox v-for="item in confirmedRequirements" :key="item.id" :value="item.id">
                {{ item.title }}（{{ item.priority }}·{{ $t('pm.requirement.status.confirmed') }}）
              </t-checkbox>
            </t-checkbox-group>
          </div>
          <template #help>
            <span class="vs-form-hint">{{ $t('pm.version.form.requirementsHint') }}</span>
          </template>
        </t-form-item>
      </t-form>
      <div class="vs-foot">
        <t-button variant="outline" @click="closeCreate">{{ $t('pm.version.dialogs.cancel') }}</t-button>
        <t-button theme="primary" :loading="creating" @click="submitCreate">
          {{ $t('pm.version.form.submit') }}
        </t-button>
      </div>
    </t-dialog>

    <!-- 版本级操作弹窗：记录意见 / 定稿 / 生成 / 重新生成 / 搁置 -->
    <PmVersionDialogs ref="dialogsRef" @changed="reload" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute, useRouter } from 'vue-router';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  createPmVersion,
  listPmRequirements,
  listPmVersions,
  updatePmVersion,
  type PmRequirement,
  type PmVersion,
  type PmVersionStatus,
} from '@/api/pm';
import { REQUIREMENT_PRIORITIES } from './pmRequirementState';
import { VERSION_STATUS_MAP } from './pmStatus';
import {
  VERSION_STATUSES,
  canDevelop,
  canFinalize,
  canGenerate,
  canLaunch,
  canRegenerate,
  canRestore,
  canReview,
  canShelve,
  emptyVersionDraft,
  emptyVersionFilter,
  filterVersions,
  formatPmDate,
  sortVersions,
  validateVersionDraft,
  versionDisplayNo,
  type VersionDraft,
  type VersionFilterState,
} from './pmVersionState';
import PmVersionDialogs from './PmVersionDialogs.vue';

const route = useRoute();
const router = useRouter();
const { t } = useI18n();

const projectId = computed(() => String(route.params.projectId || ''));

const loading = ref(false);
const versions = ref<PmVersion[]>([]);
const filter = reactive<VersionFilterState>(emptyVersionFilter());

const dialogsRef = ref<InstanceType<typeof PmVersionDialogs> | null>(null);

const createVisible = ref(false);
const creating = ref(false);
const loadingRequirements = ref(false);
const confirmedRequirements = ref<PmRequirement[]>([]);
const draft = reactive<VersionDraft>(emptyVersionDraft());

const statusOptions = computed(() => VERSION_STATUSES.map((value) => ({
  label: t(VERSION_STATUS_MAP[value].labelKey),
  value,
})));
const createdOptions = computed(() => (['7d', '30d', '90d'] as const).map((value) => ({
  label: t(`pm.version.filters.createdRange.${value}`),
  value,
})));

const columns = computed(() => [
  { colKey: 'versionNo', title: t('pm.version.columns.versionNo'), width: 112 },
  { colKey: 'name', title: t('pm.version.columns.name'), minWidth: 140, ellipsis: true },
  { colKey: 'status', title: t('pm.version.columns.status'), width: 104 },
  { colKey: 'requirementCount', title: t('pm.version.columns.requirementCount'), width: 88, align: 'center' },
  { colKey: 'planDate', title: t('pm.version.columns.planDate'), width: 112 },
  { colKey: 'planLaunchDate', title: t('pm.version.columns.planLaunchDate'), width: 124 },
  { colKey: 'finalizeDate', title: t('pm.version.columns.finalizeDate'), width: 112 },
  { colKey: 'launchDate', title: t('pm.version.columns.launchDate'), width: 112 },
  { colKey: 'actions', title: t('pm.version.columns.actions'), width: 320, fixed: 'right' },
]);

// 筛选与排序在客户端完成，避免每次输入都请求接口
const filteredList = computed(() => sortVersions(filterVersions(versions.value, filter)));

function statusMeta(status: string) {
  return VERSION_STATUS_MAP[status] ?? VERSION_STATUS_MAP.planning;
}

// 首次生成：可生成但还没有任何产物轮次
function showGenerate(item: PmVersion): boolean {
  return canGenerate(item) && !canRegenerate(item);
}

// ===== 数据加载 =====

async function loadVersions() {
  if (!projectId.value) return;
  loading.value = true;
  try {
    versions.value = await listPmVersions(projectId.value);
  } catch {
    versions.value = [];
    MessagePlugin.error(t('pm.version.loadFailed'));
  } finally {
    loading.value = false;
  }
}

async function reload() {
  await loadVersions();
}

onMounted(() => void loadVersions());

// 切换项目时组件复用，需清空旧筛选与弹窗状态
watch(projectId, () => {
  versions.value = [];
  Object.assign(filter, emptyVersionFilter());
  createVisible.value = false;
  void loadVersions();
});

// ===== 筛选 =====

function resetFilter() {
  Object.assign(filter, emptyVersionFilter());
}

// ===== 导航与操作 =====

function openDetail(item: PmVersion) {
  void router.push({ name: 'pmVersionDetail', params: { projectId: projectId.value, versionId: item.id } });
}

function openDialog(
  action: 'review' | 'finalize' | 'generate' | 'regenerate' | 'shelve',
  item: PmVersion,
) {
  dialogsRef.value?.open(action, item);
}

// 启动开发 / 上线 / 恢复规划：直接变更状态，成功后刷新
async function changeStatus(item: PmVersion, status: PmVersionStatus) {
  try {
    await updatePmVersion(item.id, { status });
    MessagePlugin.success(t('pm.version.actionSuccess'));
    await reload();
  } catch {
    MessagePlugin.error(t('pm.version.actionFailed'));
  }
}

// ===== 新建版本 =====

function openCreate() {
  Object.assign(draft, emptyVersionDraft());
  createVisible.value = true;
  void loadConfirmed();
}

function closeCreate() {
  createVisible.value = false;
}

async function loadConfirmed() {
  if (!projectId.value) return;
  loadingRequirements.value = true;
  try {
    const list = await listPmRequirements(projectId.value, { status: 'confirmed' });
    // 按优先级 P0→P3 排序，便于快速勾选
    const rank = (priority: string) => {
      const index = REQUIREMENT_PRIORITIES.indexOf(priority as PmRequirement['priority']);
      return index < 0 ? REQUIREMENT_PRIORITIES.length : index;
    };
    confirmedRequirements.value = [...list].sort((a, b) => rank(a.priority) - rank(b.priority));
  } catch {
    confirmedRequirements.value = [];
    MessagePlugin.error(t('pm.requirements.loadFailed'));
  } finally {
    loadingRequirements.value = false;
  }
}

async function submitCreate() {
  if (creating.value) return;
  const errorKey = validateVersionDraft(draft);
  if (errorKey) {
    MessagePlugin.warning(t(errorKey));
    return;
  }
  creating.value = true;
  try {
    await createPmVersion(projectId.value, {
      name: draft.name.trim(),
      plan_date: draft.plan_date,
      plan_launch_date: draft.plan_launch_date,
      release_note: draft.release_note.trim(),
      requirement_ids: draft.requirement_ids,
    });
    MessagePlugin.success(t('pm.version.form.createSuccess'));
    createVisible.value = false;
    await reload();
  } catch {
    MessagePlugin.error(t('pm.version.form.createFailed'));
  } finally {
    creating.value = false;
  }
}
</script>

<style lang="less" scoped>
.pm-versions {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  padding: var(--app-space-6);
}

/* ---------- 工具栏 ---------- */
.vs-toolbar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--app-space-2);
  margin-bottom: var(--app-space-4);
}

.vs-spacer {
  flex: 1;
}

.vs-filters {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--app-space-2);
}

.vs-filter {
  width: 132px;

  &--search {
    width: 200px;
  }
}

/* ---------- 状态与空态 ---------- */
.vs-state {
  flex: 1;
  min-height: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}

.vs-empty {
  flex-direction: column;
  gap: var(--app-space-2);
  border: 1px dashed var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-placeholder);
}

.vs-empty-title {
  font-size: var(--app-text-lg);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.vs-empty-hint {
  margin-bottom: var(--app-space-2);
  font-size: var(--app-text-md);
}

/* ---------- 列表 ---------- */
.vs-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
}

.vs-name {
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.vs-ops {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 2px;
}

/* ---------- 新建弹窗 ---------- */
.vs-date {
  width: 100%;
}

.vs-form-hint {
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}

.vs-picker {
  width: 100%;
  max-height: 140px;
  overflow: auto;
  padding: var(--app-space-2) var(--app-space-3);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-sm);
}

.vs-picker__list {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-1);
  align-items: flex-start;
}

.vs-picker__state {
  margin: 0;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
}

.vs-foot {
  display: flex;
  justify-content: flex-end;
  gap: var(--app-space-2);
  margin-top: var(--app-space-4);
}
</style>
