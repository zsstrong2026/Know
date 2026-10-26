<template>
  <div class="pm-materials">
    <!-- 工具栏：新建资料 / 右侧筛选（对齐原型） -->
    <header class="mt-toolbar">
      <t-button theme="primary" :disabled="loading" @click="openCreate">
        ＋ {{ $t('pm.materials.create') }}
      </t-button>

      <span class="mt-spacer" />

      <div class="mt-filters">
        <t-input
          v-model="filter.keyword"
          class="mt-filter mt-filter--search"
          :placeholder="$t('pm.materials.filters.keyword')"
          clearable
        >
          <template #prefix-icon><t-icon name="search" /></template>
        </t-input>
        <t-select
          v-model="filter.type"
          class="mt-filter"
          :placeholder="$t('pm.materials.filters.type')"
          :options="typeOptions"
          clearable
        />
        <t-select
          v-model="filter.versionId"
          class="mt-filter"
          :placeholder="$t('pm.materials.filters.version')"
          :options="versionOptions"
          clearable
        />
        <t-button variant="text" size="small" @click="resetFilter">
          {{ $t('pm.materials.filters.reset') }}
        </t-button>
      </div>
    </header>

    <div v-if="loading && !materials.length" class="mt-state">
      <t-loading />
    </div>

    <!-- 空态：区分「还没有资料」与「筛选无结果」 -->
    <div v-else-if="!materials.length" class="mt-state mt-empty">
      <t-icon name="file" size="40px" />
      <div class="mt-empty-title">{{ $t('pm.materials.empty.title') }}</div>
      <div class="mt-empty-hint">{{ $t('pm.materials.empty.hint') }}</div>
      <t-button theme="primary" variant="outline" @click="openCreate">
        {{ $t('pm.materials.create') }}
      </t-button>
    </div>
    <div v-else-if="!filteredList.length" class="mt-state mt-empty">
      <t-icon name="search" size="40px" />
      <div class="mt-empty-title">{{ $t('pm.materials.empty.filteredTitle') }}</div>
      <div class="mt-empty-hint">{{ $t('pm.materials.empty.filteredHint') }}</div>
      <t-button variant="outline" @click="resetFilter">
        {{ $t('pm.materials.filters.reset') }}
      </t-button>
    </div>

    <div v-else class="mt-body">
      <t-table row-key="id" :data="filteredList" :columns="columns" :loading="loading" size="medium" hover>
        <template #title="{ row }">
          <t-button variant="text" size="small" class="mt-name" @click="openPreview(row)">
            {{ row.title }}
          </t-button>
        </template>
        <template #materialType="{ row }">
          <t-tag :theme="typeMeta(row.material_type).theme" variant="light" size="small">
            {{ $t(typeMeta(row.material_type).labelKey) }}
          </t-tag>
        </template>
        <template #versionNo="{ row }">{{ row.version_no || '—' }}</template>
        <template #status="{ row }">
          <t-tag :theme="statusMeta(row).theme" variant="light" size="small">
            {{ $t(statusMeta(row).labelKey) }}
          </t-tag>
        </template>
        <template #updatedAt="{ row }">{{ formatPmDateTime(row.updated_at) }}</template>
        <template #actions="{ row }">
          <div class="mt-ops">
            <t-button variant="text" size="small" @click="openPreview(row)">
              {{ $t('pm.materials.actions.preview') }}
            </t-button>
            <t-button v-if="hasHistory(row)" variant="text" size="small" @click="openHistory(row)">
              {{ $t('pm.materials.actions.history') }}
            </t-button>
            <t-button v-if="canDownload(row)" variant="text" size="small" @click="runDownload(row)">
              {{ $t('pm.materials.actions.download') }}
            </t-button>
            <t-button v-if="canGenerate(row)" variant="text" size="small" @click="runGenerate(row)">
              {{ $t('pm.materials.actions.generate') }}
            </t-button>
            <t-button v-if="canPublish(row)" variant="text" size="small" @click="confirmPublish(row)">
              {{ $t('pm.materials.actions.publish') }}
            </t-button>
            <!-- 仅草稿可删除；已发布/归档需先归档处理 -->
            <t-button v-if="canDelete(row)" theme="danger" variant="text" size="small" @click="confirmDelete(row)">
              {{ $t('pm.materials.actions.delete') }}
            </t-button>
            <!-- 版本上线后提示「有新版可用」，与原型一致 -->
            <t-tag v-if="canUpdateToLatest(row)" theme="danger" variant="light" size="small">
              {{ $t('pm.materials.actions.needsUpdate') }}
            </t-tag>
            <t-button v-if="canUpdateToLatest(row)" variant="text" size="small" @click="runUpdateLatest(row)">
              {{ $t('pm.materials.actions.updateLatest') }}
            </t-button>
          </div>
        </template>
      </t-table>
      <p class="mt-note">{{ $t('pm.materials.note') }}</p>
    </div>

    <!-- 新建资料：创建后为草稿 -->
    <t-dialog
      :visible="createVisible"
      :header="$t('pm.materials.form.createTitle')"
      width="560px"
      placement="center"
      :footer="false"
      @close="closeCreate"
    >
      <t-form label-align="top">
        <t-form-item :label="$t('pm.materials.form.title')" required-mark>
          <t-input
            v-model="draft.title"
            :maxlength="100"
            :placeholder="$t('pm.materials.form.titlePlaceholder')"
          />
        </t-form-item>
        <t-form-item :label="$t('pm.materials.form.type')" required-mark>
          <t-select v-model="draft.material_type" :options="typeOptions" />
        </t-form-item>
        <t-form-item :label="$t('pm.materials.form.version')" required-mark>
          <t-select
            v-model="draft.version_id"
            :options="usableVersionOptions"
            :placeholder="$t('pm.materials.form.versionPlaceholder')"
          />
          <template #help>
            <span class="mt-form-hint">{{ $t('pm.materials.form.versionHint') }}</span>
          </template>
        </t-form-item>
      </t-form>
      <div class="mt-foot">
        <t-button variant="outline" @click="closeCreate">{{ $t('pm.materials.cancel') }}</t-button>
        <t-button theme="primary" :loading="creating" @click="submitCreate">
          {{ $t('pm.materials.form.submit') }}
        </t-button>
      </div>
    </t-dialog>

    <!-- 生成进度：复用统一任务轮询组件 -->
    <t-dialog
      :visible="taskVisible"
      :header="$t('pm.materials.taskTitle')"
      width="520px"
      placement="center"
      :footer="false"
      @close="taskVisible = false"
    >
      <PmTaskProgress :task-id="activeTaskId" @done="onTaskDone" @failed="onTaskFailed" />
      <div class="mt-foot">
        <t-button variant="outline" @click="taskVisible = false">
          {{ $t('pm.materials.close') }}
        </t-button>
      </div>
    </t-dialog>

    <!-- 历史版本：同类型归档记录，可预览原文或下载文件 -->
    <t-dialog
      :visible="historyVisible"
      :header="$t('pm.materials.history.title')"
      width="660px"
      placement="center"
      :footer="false"
      @close="closeHistory"
    >
      <p class="mt-history-hint">
        {{ $t('pm.materials.history.hint', { title: historyMaterial?.title || '' }) }}
      </p>
      <p v-if="!historyList.length" class="mt-history-empty">
        {{ $t('pm.materials.history.empty') }}
      </p>
      <t-table
        v-else
        row-key="id"
        :data="historyList"
        :columns="historyColumns"
        size="small"
        hover
      >
        <template #title="{ row }">
          <t-button variant="text" size="small" class="mt-name" @click="previewHistory(row)">
            {{ row.title }}
          </t-button>
        </template>
        <template #versionNo="{ row }">{{ row.version_no || '—' }}</template>
        <template #updatedAt="{ row }">{{ formatPmDateTime(row.updated_at) }}</template>
        <template #actions="{ row }">
          <div class="mt-ops">
            <t-button variant="text" size="small" @click="previewHistory(row)">
              {{ $t('pm.materials.actions.preview') }}
            </t-button>
            <t-button v-if="canDownload(row)" variant="text" size="small" @click="runDownload(row)">
              {{ $t('pm.materials.actions.download') }}
            </t-button>
          </div>
        </template>
      </t-table>
      <div class="mt-foot">
        <t-button variant="outline" @click="closeHistory">{{ $t('pm.materials.close') }}</t-button>
      </div>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute, useRouter } from 'vue-router';
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next';
import {
  createPmMaterial,
  deletePmMaterial,
  downloadPmMaterial,
  generatePmMaterial,
  listPmMaterials,
  listPmVersions,
  publishPmMaterial,
  updatePmMaterialToLatest,
  type PmMaterial,
  type PmMaterialType,
  type PmVersion,
} from '@/api/pm';
import { downloadBlob } from '@/utils/downloadFile';
import PmTaskProgress from './components/PmTaskProgress.vue';
import {
  MATERIAL_ARCHIVED_META,
  MATERIAL_STATUS_MAP,
  MATERIAL_TYPE_MAP,
  MATERIAL_TYPES,
  canDelete,
  canDownload,
  canGenerate,
  canPublish,
  canUpdateToLatest,
  emptyMaterialDraft,
  emptyMaterialFilter,
  filterMaterials,
  formatPmDateTime,
  materialFileName,
  materialHistory,
  sortMaterials,
  validateMaterialDraft,
  type MaterialDraft,
  type MaterialFilterState,
} from './pmMaterialState';

const route = useRoute();
const router = useRouter();
const { t } = useI18n();

const projectId = computed(() => String(route.params.projectId || ''));

const loading = ref(false);
const materials = ref<PmMaterial[]>([]);
const versions = ref<PmVersion[]>([]);
const filter = reactive<MaterialFilterState>(emptyMaterialFilter());

const createVisible = ref(false);
const creating = ref(false);
const draft = reactive<MaterialDraft>(emptyMaterialDraft());

const taskVisible = ref(false);
const activeTaskId = ref('');

// 历史版本弹窗：以当前资料为基准，展示同类型归档记录
const historyVisible = ref(false);
const historyMaterial = ref<PmMaterial | null>(null);
const historyList = computed(() => {
  if (!historyMaterial.value) return [];
  return sortMaterials(materialHistory(materials.value, historyMaterial.value));
});
const historyColumns = computed(() => [
  { colKey: 'title', title: t('pm.materials.columns.title'), minWidth: 200, ellipsis: true },
  { colKey: 'versionNo', title: t('pm.materials.columns.version'), width: 100 },
  { colKey: 'updatedAt', title: t('pm.materials.preview.archivedAt'), width: 150 },
  { colKey: 'actions', title: t('pm.materials.columns.actions'), width: 140 },
]);

const typeOptions = computed(() => MATERIAL_TYPES.map((value) => ({
  label: t(MATERIAL_TYPE_MAP[value].labelKey),
  value,
})));

// 版本下拉：优先展示版本号，未定稿版本回退到名称
const versionOptions = computed(() => versions.value.map((v) => ({
  label: v.version_no || v.name,
  value: v.id,
})));

// 新建资料只允许关联已定稿及以上版本，与后端校验保持一致
const USABLE_VERSION_STATUSES = ['finalized', 'developing', 'launched'];
const usableVersionOptions = computed(() => versions.value
  .filter((v) => USABLE_VERSION_STATUSES.includes(v.status))
  .map((v) => ({ label: v.version_no || v.name, value: v.id })));

// 列宽分配：标题列自适应占满剩余空间，操作列固定在最右侧
const columns = computed(() => [
  { colKey: 'title', title: t('pm.materials.columns.title'), minWidth: 220, ellipsis: true },
  { colKey: 'materialType', title: t('pm.materials.columns.type'), width: 104 },
  { colKey: 'versionNo', title: t('pm.materials.columns.version'), width: 104 },
  { colKey: 'status', title: t('pm.materials.columns.status'), width: 96 },
  { colKey: 'updatedAt', title: t('pm.materials.columns.updatedAt'), width: 156 },
  { colKey: 'actions', title: t('pm.materials.columns.actions'), width: 400, fixed: 'right' },
]);

// 列表只展示当前资料，归档记录在预览页的「历史归档」中查看
const currentMaterials = computed(() => materials.value.filter((m) => !m.archived));
const filteredList = computed(() => sortMaterials(filterMaterials(currentMaterials.value, filter)));

function typeMeta(type: PmMaterialType) {
  return MATERIAL_TYPE_MAP[type] ?? MATERIAL_TYPE_MAP.manual;
}

function statusMeta(material: PmMaterial) {
  if (material.archived) return MATERIAL_ARCHIVED_META;
  return MATERIAL_STATUS_MAP[material.status] ?? MATERIAL_STATUS_MAP.draft;
}

// ===== 数据加载 =====

async function loadMaterials() {
  if (!projectId.value) return;
  loading.value = true;
  try {
    materials.value = await listPmMaterials(projectId.value);
  } catch {
    materials.value = [];
    MessagePlugin.error(t('pm.materials.loadFailed'));
  } finally {
    loading.value = false;
  }
}

// 版本列表同时服务于筛选与新建弹窗
async function loadVersions() {
  if (!projectId.value) return;
  try {
    versions.value = await listPmVersions(projectId.value);
  } catch {
    versions.value = [];
  }
}

async function reload() {
  await Promise.all([loadMaterials(), loadVersions()]);
}

onMounted(() => void reload());

// 切换项目时组件复用，需清空旧筛选与弹窗状态
watch(projectId, () => {
  materials.value = [];
  versions.value = [];
  Object.assign(filter, emptyMaterialFilter());
  createVisible.value = false;
  taskVisible.value = false;
  closeHistory();
  void reload();
});

function resetFilter() {
  Object.assign(filter, emptyMaterialFilter());
}

// ===== 操作 =====

function openPreview(material: PmMaterial) {
  void router.push({
    name: 'pmMaterialPreview',
    params: { projectId: projectId.value, materialId: material.id },
  });
}

// ===== 历史版本 =====

// 存在同类型归档记录时才展示入口，避免空弹窗
function hasHistory(material: PmMaterial): boolean {
  return materialHistory(materials.value, material).length > 0;
}

function openHistory(material: PmMaterial) {
  historyMaterial.value = material;
  historyVisible.value = true;
}

function closeHistory() {
  historyVisible.value = false;
  historyMaterial.value = null;
}

// 弹窗内预览：关闭弹窗后进入资料原文页
function previewHistory(material: PmMaterial) {
  closeHistory();
  openPreview(material);
}

// 生成/更新均返回任务，统一用进度弹窗展示
function startTask(taskId: string) {
  activeTaskId.value = taskId;
  taskVisible.value = true;
}

async function runGenerate(material: PmMaterial) {
  try {
    const task = await generatePmMaterial(material.id);
    startTask(task.id);
  } catch {
    MessagePlugin.error(t('pm.materials.actionFailed'));
  }
}

async function runUpdateLatest(material: PmMaterial) {
  try {
    const task = await updatePmMaterialToLatest(material.id);
    startTask(task.id);
  } catch {
    MessagePlugin.error(t('pm.materials.actionFailed'));
  }
}

// 发布需二次确认：旧发布版会被归档
function confirmPublish(material: PmMaterial) {
  const dialog = DialogPlugin.confirm({
    header: t('pm.materials.publishConfirmTitle'),
    body: t('pm.materials.publishConfirmBody', { title: material.title }),
    confirmBtn: t('pm.materials.actions.publish'),
    cancelBtn: t('pm.materials.cancel'),
    onConfirm: async () => {
      dialog.hide();
      try {
        await publishPmMaterial(material.id);
        MessagePlugin.success(t('pm.materials.publishSuccess'));
        await reload();
      } catch {
        MessagePlugin.error(t('pm.materials.actionFailed'));
      }
    },
  });
}

// 下载由后端按资料类型导出 docx/pptx，前端只负责落盘
async function runDownload(material: PmMaterial) {
  try {
    const blob = await downloadPmMaterial(material.id);
    downloadBlob(blob, materialFileName(material));
  } catch {
    MessagePlugin.error(t('pm.materials.downloadFailed'));
  }
}

// 删除不可恢复，需二次确认；仅草稿展示入口
function confirmDelete(material: PmMaterial) {
  const dialog = DialogPlugin.confirm({
    header: t('pm.materials.deleteConfirmTitle'),
    body: t('pm.materials.deleteConfirmBody', { title: material.title }),
    confirmBtn: { content: t('pm.materials.actions.delete'), theme: 'danger' as const },
    cancelBtn: t('pm.materials.cancel'),
    onConfirm: async () => {
      dialog.hide();
      try {
        await deletePmMaterial(material.id);
        MessagePlugin.success(t('pm.materials.deleteSuccess'));
        await reload();
      } catch {
        MessagePlugin.error(t('pm.materials.deleteFailed'));
      }
    },
  });
}

function onTaskDone() {
  MessagePlugin.success(t('pm.materials.taskSuccess'));
  void reload();
}

function onTaskFailed() {
  MessagePlugin.error(t('pm.materials.taskFailed'));
  void reload();
}

// ===== 新建资料 =====

function openCreate() {
  Object.assign(draft, emptyMaterialDraft());
  createVisible.value = true;
  if (!versions.value.length) void loadVersions();
}

function closeCreate() {
  createVisible.value = false;
}

async function submitCreate() {
  if (creating.value) return;
  const errorKey = validateMaterialDraft(draft);
  if (errorKey) {
    MessagePlugin.warning(t(errorKey));
    return;
  }
  creating.value = true;
  try {
    await createPmMaterial({
      project_id: projectId.value,
      title: draft.title.trim(),
      material_type: draft.material_type,
      version_id: draft.version_id,
    });
    MessagePlugin.success(t('pm.materials.form.createSuccess'));
    createVisible.value = false;
    await reload();
  } catch {
    MessagePlugin.error(t('pm.materials.form.createFailed'));
  } finally {
    creating.value = false;
  }
}
</script>

<style lang="less" scoped>
.pm-materials {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  padding: var(--app-space-6);
}

/* ---------- 工具栏 ---------- */
.mt-toolbar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--app-space-2);
  margin-bottom: var(--app-space-4);
}

.mt-spacer {
  flex: 1;
}

.mt-filters {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--app-space-2);
}

.mt-filter {
  width: 132px;

  &--search {
    width: 200px;
  }
}

/* ---------- 状态与空态 ---------- */
.mt-state {
  flex: 1;
  min-height: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}

.mt-empty {
  flex-direction: column;
  gap: var(--app-space-2);
  border: 1px dashed var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-placeholder);
}

.mt-empty-title {
  font-size: var(--app-text-lg);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.mt-empty-hint {
  margin-bottom: var(--app-space-2);
  font-size: var(--app-text-md);
}

/* ---------- 列表 ---------- */
.mt-body {
  flex: 1;
  min-height: 0;
  overflow: auto;

  // 操作列表头左对齐，并右移一个按钮内边距，与「预览」文字左边缘对齐
  :deep(.t-table__th-actions) {
    padding-left: calc(var(--td-comp-paddingLR-l) + var(--td-comp-paddingLR-s) - 1px);
    text-align: left;
  }
}

.mt-name {
  font-weight: 600;
  padding-left: 0;
}

.mt-ops {
  display: flex;
  align-items: center;
  justify-content: flex-start; // 操作按钮靠列左侧排列
  flex-wrap: wrap;
  gap: 2px;
}

.mt-note {
  margin: var(--app-space-3) 0 0;
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}

/* ---------- 弹窗 ---------- */
.mt-form-hint {
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}

.mt-foot {
  display: flex;
  justify-content: flex-end;
  gap: var(--app-space-2);
  margin-top: var(--app-space-4);
}

/* ---------- 历史版本弹窗 ---------- */
.mt-history-hint {
  margin: 0 0 var(--app-space-3);
  font-size: var(--app-text-md);
  color: var(--td-text-color-secondary);
}

.mt-history-empty {
  margin: 0;
  padding: var(--app-space-6) 0;
  text-align: center;
  font-size: var(--app-text-md);
  color: var(--td-text-color-placeholder);
}
</style>
