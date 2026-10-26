<template>
  <div class="pm-roadmap">
    <!-- 顶部工具栏：新增规划 / 重新规划 / 规划历史 -->
    <header class="rm-toolbar">
      <div class="rm-heading">
        <h2 class="rm-title">{{ $t('pm.roadmap.title') }}</h2>
        <p class="rm-subtitle">{{ $t('pm.roadmap.subtitle') }}</p>
      </div>
      <div class="rm-ops">
        <t-button theme="primary" :disabled="loading" @click="openCreate()">
          {{ activeBatch ? $t('pm.roadmap.actions.add') : $t('pm.roadmap.actions.start') }}
        </t-button>
        <!-- 重新规划必须基于现有生效批次 -->
        <t-tooltip v-if="!activeBatch" :content="$t('pm.roadmap.actions.replanDisabled')">
          <span>
            <t-button variant="outline" disabled>{{ $t('pm.roadmap.actions.replan') }}</t-button>
          </span>
        </t-tooltip>
        <t-button v-else variant="outline" @click="openCreate('replan')">
          {{ $t('pm.roadmap.actions.replan') }}
        </t-button>
        <t-button variant="outline" @click="openHistory()">
          {{ $t('pm.roadmap.actions.history') }}
        </t-button>
      </div>
    </header>

    <!-- 批次条：横向平铺，当前批次高亮 -->
    <div v-if="batches.length" class="rm-batch-bar">
      <span class="rm-batch-label">{{ $t('pm.roadmap.batch.label') }}</span>
      <button
        v-for="batch in batches"
        :key="batch.id"
        type="button"
        class="rm-batch"
        :class="{ 'is-on': batch.id === selectedId }"
        @click="selectedId = batch.id"
      >
        {{ batch.batch_name }}
      </button>
      <span class="rm-batch-summary">{{ batchSummary }}</span>
      <span class="rm-batch-spacer" />
      <t-tag v-if="selectedBatch" size="small" variant="light" :theme="statusTheme(selectedBatch.status)">
        {{ statusLabel(selectedBatch.status) }}
      </t-tag>
      <button v-if="selectedBatch?.status === 'draft'" type="button" class="rm-batch-link is-strong" @click="resumeGuide">
        {{ $t('pm.roadmap.batch.resume') }}
      </button>
      <button
        v-if="selectedBatch?.status === 'draft'"
        type="button"
        class="rm-batch-link is-danger"
        @click="confirmDeleteBatch"
      >
        {{ $t('pm.roadmap.batch.delete') }}
      </button>
      <button type="button" class="rm-batch-link" @click="openHistory(selectedBatch?.generated_at ? selectedBatch.id : '')">
        {{ $t('pm.roadmap.batch.snapshot') }}
      </button>
    </div>

    <div v-if="loading && !batches.length" class="rm-state">
      <t-loading />
    </div>

    <!-- 空态：尚未规划过 -->
    <div v-else-if="!batches.length" class="rm-state rm-empty">
      <t-icon name="map" size="40px" />
      <div class="rm-empty-title">{{ $t('pm.roadmap.empty.title') }}</div>
      <div class="rm-empty-hint">{{ $t('pm.roadmap.empty.hint') }}</div>
      <t-button theme="primary" variant="outline" @click="openCreate('initial')">
        {{ $t('pm.roadmap.actions.start') }}
      </t-button>
    </div>

    <!-- 主体：Markdown 路线图 -->
    <div v-else-if="selectedBatch" class="rm-body">
      <section class="rm-doc">
        <div class="rm-doc-head">
          <span class="rm-doc-title">{{ $t('pm.roadmap.doc.title') }}</span>
          <span class="rm-doc-meta">{{ sideMeta }}</span>
        </div>
        <div class="rm-doc-body">
          <!-- 未生成的批次没有路线图正文，引导用户继续完成 -->
          <div v-if="!selectedBatch.generated_at" class="rm-pending">
            <t-icon name="time" size="32px" />
            <div class="rm-pending-title">{{ $t('pm.roadmap.doc.draftTitle') }}</div>
            <p class="rm-pending-hint">{{ $t('pm.roadmap.doc.draftHint') }}</p>
            <t-button theme="primary" @click="resumeGuide">{{ $t('pm.roadmap.batch.resume') }}</t-button>
          </div>
          <PmMarkdown v-else :content="roadmapMarkdown" />
        </div>
      </section>
    </div>

    <!-- 新建规划：先收集批次信息，再进入 5 步引导 -->
    <t-dialog
      v-model:visible="createVisible"
      width="520px"
      placement="center"
      :header="$t('pm.roadmap.create.title')"
      :confirm-btn="{ content: $t('pm.roadmap.create.submit'), loading: submitting }"
      :cancel-btn="$t('common.cancel')"
      :on-confirm="submitCreate"
    >
      <t-form label-align="top" :data="form">
        <t-form-item :label="$t('pm.roadmap.create.name')" required-mark>
          <t-input
            v-model="form.name"
            :placeholder="$t('pm.roadmap.create.namePlaceholder')"
            :status="nameError ? 'error' : 'default'"
            :maxlength="80"
            @enter="submitCreate"
          />
          <template #help>
            <span v-if="nameError" class="rm-form-error">{{ nameError }}</span>
            <span v-else class="rm-form-hint">{{ $t('pm.roadmap.create.typeHint') }}</span>
          </template>
        </t-form-item>
        <t-form-item :label="$t('pm.roadmap.create.type')">
          <t-radio-group v-model="form.planType">
            <t-radio-button v-for="type in PLAN_TYPES" :key="type" :value="type">
              {{ $t(`pm.roadmap.planType.${type}`) }}
            </t-radio-button>
          </t-radio-group>
          <template #help>
            <span class="rm-form-hint">{{ $t(`pm.roadmap.planTypeDesc.${form.planType}`) }}</span>
          </template>
        </t-form-item>
      </t-form>
    </t-dialog>

    <!-- 引导为覆盖层，保留批次列表作为背景 -->
    <PmRoadmapGuide
      v-if="guideBatch"
      :project-id="projectId"
      :project-name="projectName"
      :batch="guideBatch"
      :seed-draft="guideSeedDraft"
      :active-batch-name="activeBatch?.batch_name"
      @close="onGuideClose"
      @completed="onGuideCompleted"
    />

    <PmPlanHistoryDialog
      v-model:visible="historyVisible"
      :project-id="projectId"
      :project-name="projectName"
      :initial-snapshot-id="historySnapshotId"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute } from 'vue-router';
import { MessagePlugin, DialogPlugin } from 'tdesign-vue-next';
import {
  createPlanBatch,
  deletePlanBatch,
  getPmProject,
  listPlanBatches,
  type PmPlanBatch,
  type PmPlanBatchStatus,
  type PmPlanType,
  type PmRoadmapDraft,
} from '@/api/pm';
import { usePmProjectsStore } from '@/stores/pmProjects';
import {
  PLAN_TYPES,
  buildRoadmapMarkdown,
  summarizeDraft,
} from './pmRoadmapState';
import PmMarkdown from './components/PmMarkdown.vue';
import PmRoadmapGuide from './PmRoadmapGuide.vue';
import PmPlanHistoryDialog from './PmPlanHistoryDialog.vue';

const route = useRoute();
const { t } = useI18n();
const projectStore = usePmProjectsStore();

const projectId = computed(() => String(route.params.projectId || ''));
// 项目名称从共享 store 取；未命中时由 ensureProject 拉取后回填
const projectName = computed(() => projectStore.projects.find((item) => item.id === projectId.value)?.name || '');

const loading = ref(false);
const batches = ref<PmPlanBatch[]>([]);
const selectedId = ref('');

const createVisible = ref(false);
const submitting = ref(false);
const nameError = ref('');
const form = reactive<{ name: string; planType: PmPlanType }>({ name: '', planType: 'initial' });

const guideBatch = ref<PmPlanBatch | null>(null);
const guideSeedDraft = ref<PmRoadmapDraft | null>(null);

const historyVisible = ref(false);
const historySnapshotId = ref('');

const selectedBatch = computed(() => batches.value.find((item) => item.id === selectedId.value) ?? null);
const activeBatch = computed(() => batches.value.find((item) => item.status === 'active') ?? null);
const stats = computed(() => summarizeDraft(selectedBatch.value?.draft));

const roadmapMarkdown = computed(() => (
  selectedBatch.value ? buildRoadmapMarkdown(projectName.value || selectedBatch.value.batch_name, selectedBatch.value.draft) : ''
));

const sideMeta = computed(() => t('pm.roadmap.side.meta', {
  modules: stats.value.modules,
  features: stats.value.features,
  operations: stats.value.operations,
  scope: stats.value.scope,
}));

const batchSummary = computed(() => (
  selectedBatch.value?.change_summary || t('pm.roadmap.batch.noSummary')
));

// ===== 数据加载 =====

function today(): string {
  const now = new Date();
  const pad = (value: number) => String(value).padStart(2, '0');
  return `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`;
}

function defaultBatchName(type: PmPlanType): string {
  return `${t(`pm.roadmap.planType.${type}`)} · ${today()}`;
}

async function ensureProject() {
  if (!projectId.value || projectName.value) return;
  try {
    const project = await getPmProject(projectId.value);
    projectStore.upsert(project);
  } catch {
    // 名称仅用于标题展示，失败时回退为批次名
  }
}

// preferId：新建/完成引导后优先保持选中的批次
async function loadBatches(preferId = '') {
  if (!projectId.value) return;
  loading.value = true;
  try {
    const list = await listPlanBatches(projectId.value);
    batches.value = [...list].sort((a, b) => (a.created_at < b.created_at ? 1 : -1));
    const target = preferId || selectedId.value;
    const exists = batches.value.some((item) => item.id === target);
    selectedId.value = exists
      ? target
      : batches.value.find((item) => item.status === 'active')?.id || batches.value[0]?.id || '';
  } catch {
    batches.value = [];
    selectedId.value = '';
    MessagePlugin.error(t('pm.roadmap.loadFailed'));
  } finally {
    loading.value = false;
  }
}

onMounted(() => {
  void ensureProject();
  void loadBatches();
});

// 切换项目时同一组件被复用，需要重置引导与历史状态并重拉
watch(projectId, () => {
  guideBatch.value = null;
  historyVisible.value = false;
  historySnapshotId.value = '';
  selectedId.value = '';
  void ensureProject();
  void loadBatches();
});

// ===== 新建规划 =====

function openCreate(mode?: PmPlanType) {
  const type: PmPlanType = mode ?? (activeBatch.value ? 'iteration' : 'initial');
  form.planType = type;
  form.name = defaultBatchName(type);
  nameError.value = '';
  createVisible.value = true;
}

// 名称仍是上一类型的默认值时跟随类型更新，用户已改名则保留
watch(() => form.planType, (next, prev) => {
  if (form.name === defaultBatchName(prev)) form.name = defaultBatchName(next);
});

async function submitCreate() {
  if (submitting.value) return;
  const name = form.name.trim();
  if (!name) {
    nameError.value = t('pm.roadmap.create.nameEmpty');
    return;
  }
  nameError.value = '';
  submitting.value = true;
  try {
    const batch = await createPlanBatch(projectId.value, { batch_name: name, plan_type: form.planType });
    // 增量/重新规划继承当前生效批次草稿作为基线；首期从空白开始
    const seed = form.planType === 'initial' ? null : activeBatch.value?.draft ?? null;
    createVisible.value = false;
    await loadBatches(batch.id);
    guideSeedDraft.value = seed;
    guideBatch.value = batch;
  } catch {
    MessagePlugin.error(t('pm.roadmap.create.failed'));
  } finally {
    submitting.value = false;
  }
}

// ===== 引导续接与关闭 =====

function resumeGuide() {
  if (!selectedBatch.value) return;
  guideSeedDraft.value = selectedBatch.value.draft;
  guideBatch.value = selectedBatch.value;
}

function onGuideClose() {
  guideBatch.value = null;
  void loadBatches();
}

async function onGuideCompleted() {
  guideBatch.value = null;
  await loadBatches();
  const active = batches.value.find((item) => item.status === 'active');
  if (active) selectedId.value = active.id;
}

// ===== 删除草稿批次 =====

// 仅草稿批次可删；删除后清空选中，让 loadBatches 重新回落到生效批次
function confirmDeleteBatch() {
  const batch = selectedBatch.value;
  if (!batch || batch.status !== 'draft') return;
  const dialog = DialogPlugin.confirm({
    header: t('pm.roadmap.batch.deleteConfirmTitle'),
    body: t('pm.roadmap.batch.deleteConfirmBody', { name: batch.batch_name }),
    confirmBtn: { content: t('pm.roadmap.batch.delete'), theme: 'danger' as const },
    cancelBtn: t('common.cancel'),
    onConfirm: async () => {
      dialog.hide();
      try {
        await deletePlanBatch(batch.id);
        MessagePlugin.success(t('pm.roadmap.batch.deleteSuccess'));
        // 同步收起与该批次相关的引导与历史弹窗，避免指向已删数据
        if (guideBatch.value?.id === batch.id) guideBatch.value = null;
        if (historySnapshotId.value === batch.id) historyVisible.value = false;
        selectedId.value = '';
        await loadBatches();
      } catch {
        MessagePlugin.error(t('pm.roadmap.batch.deleteFailed'));
      }
    },
  });
}

// ===== 规划历史 =====

// 传入批次 ID 时直接打开该批次快照，否则展示批次列表
function openHistory(snapshotId = '') {
  historySnapshotId.value = snapshotId;
  historyVisible.value = true;
}

// ===== 展示辅助 =====

function statusLabel(status: PmPlanBatchStatus): string {
  return t(`pm.roadmap.batchStatus.${status}`);
}

function statusTheme(status: PmPlanBatchStatus): 'success' | 'warning' | 'default' {
  if (status === 'active') return 'success';
  if (status === 'draft') return 'warning';
  return 'default';
}
</script>

<style lang="less" scoped>
.pm-roadmap {
  position: relative;
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  padding: var(--app-space-6);
}

/* ---------- 工具栏 ---------- */
.rm-toolbar {
  display: flex;
  align-items: flex-start;
  gap: var(--app-space-4);
  margin-bottom: var(--app-space-4);
}

.rm-heading {
  min-width: 0;
}

.rm-title {
  margin: 0;
  font-size: var(--app-text-2xl);
  font-weight: 700;
  color: var(--td-text-color-primary);
}

.rm-subtitle {
  margin: var(--app-space-1) 0 0;
  font-size: var(--app-text-md);
  color: var(--td-text-color-secondary);
}

.rm-ops {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  flex-shrink: 0;
}

/* ---------- 批次条 ---------- */
.rm-batch-bar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--app-space-2);
  padding: 10px var(--app-space-3);
  margin-bottom: var(--app-space-3);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-secondarycontainer);
}

.rm-batch-label {
  font-size: var(--app-text-sm);
  font-weight: 700;
  color: var(--td-text-color-secondary);
}

.rm-batch {
  max-width: 220px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  padding: 3px 12px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-pill);
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

.rm-batch-summary {
  min-width: 0;
  margin-left: var(--app-space-2);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
}

.rm-batch-spacer {
  flex: 1;
}

.rm-batch-link {
  padding: 0;
  border: 0;
  background: none;
  color: var(--td-brand-color);
  font-size: var(--app-text-sm);
  cursor: pointer;

  &.is-strong {
    font-weight: 600;
  }

  &.is-danger {
    color: var(--td-error-color);
  }
}

/* ---------- 状态与空态 ---------- */
.rm-state {
  flex: 1;
  min-height: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}

.rm-empty {
  flex-direction: column;
  gap: var(--app-space-2);
  border: 1px dashed var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-placeholder);
}

.rm-empty-title {
  font-size: var(--app-text-lg);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.rm-empty-hint {
  margin-bottom: var(--app-space-2);
  font-size: var(--app-text-md);
}

/* ---------- 主体 ---------- */
.rm-body {
  flex: 1;
  min-height: 0;
  display: grid;
  grid-template-columns: minmax(0, 1fr);
}

.rm-doc {
  min-width: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
  background: var(--td-bg-color-container);
}

.rm-doc-head {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  padding: 10px var(--app-space-3);
  border-bottom: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-secondarycontainer);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.rm-doc-meta {
  margin-left: auto;
  font-size: var(--app-text-sm);
  font-weight: 400;
  color: var(--td-text-color-placeholder);
}

.rm-doc-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: var(--app-space-4) var(--app-space-6);
}

.rm-pending {
  height: 100%;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--app-space-2);
  color: var(--td-text-color-placeholder);
}

.rm-pending-title {
  font-size: var(--app-text-lg);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.rm-pending-hint {
  margin: 0 0 var(--app-space-2);
  font-size: var(--app-text-md);
}

/* ---------- 表单辅助 ---------- */
.rm-form-error {
  color: var(--td-error-color);
}

.rm-form-hint {
  color: var(--td-text-color-placeholder);
}
</style>
