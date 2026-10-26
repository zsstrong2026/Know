<template>
  <div class="pm-material-preview">
    <header class="mp-head">
      <t-button variant="text" size="small" @click="goBack">
        ← {{ $t('pm.materials.preview.back') }}
      </t-button>
    </header>

    <div v-if="loading" class="mp-state"><t-loading /></div>

    <div v-else-if="!material" class="mp-state mp-empty">
      <t-icon name="file" size="40px" />
      <div class="mp-empty-title">{{ $t('pm.materials.preview.notFound') }}</div>
      <t-button variant="outline" @click="goBack">{{ $t('pm.materials.preview.back') }}</t-button>
    </div>

    <div v-else class="mp-body">
      <!-- 资料概要：类型、对应版本、状态与更新时间 -->
      <section class="mp-card">
        <div class="mp-title-row">
          <h2 class="mp-title">{{ material.title }}</h2>
          <t-tag :theme="typeMeta.theme" variant="light" size="small">{{ $t(typeMeta.labelKey) }}</t-tag>
          <t-tag :theme="statusMeta.theme" variant="light" size="small">{{ $t(statusMeta.labelKey) }}</t-tag>
          <t-tag v-if="material.needs_update" theme="danger" variant="light" size="small">
            {{ $t('pm.materials.actions.needsUpdate') }}
          </t-tag>
        </div>

        <div class="mp-info">
          <div class="mp-info__item">
            <span class="mp-info__label">{{ $t('pm.materials.preview.version') }}</span>
            <span class="mp-info__value">{{ material.version_no || '—' }}</span>
          </div>
          <div class="mp-info__item">
            <span class="mp-info__label">{{ $t('pm.materials.preview.latestVersion') }}</span>
            <span class="mp-info__value">{{ material.latest_version_no || '—' }}</span>
          </div>
          <div class="mp-info__item">
            <span class="mp-info__label">{{ $t('pm.materials.preview.updatedAt') }}</span>
            <span class="mp-info__value">{{ formatPmDateTime(material.updated_at) }}</span>
          </div>
        </div>

        <div class="mp-actions">
          <t-button v-if="canDownload(material)" variant="outline" size="small" @click="runDownload">
            {{ $t('pm.materials.actions.download') }}
          </t-button>
          <t-button v-if="canGenerate(material)" variant="outline" size="small" @click="runGenerate">
            {{ $t('pm.materials.actions.generate') }}
          </t-button>
          <t-button v-if="canPublish(material)" theme="primary" size="small" @click="confirmPublish">
            {{ $t('pm.materials.actions.publish') }}
          </t-button>
          <t-button
            v-if="canUpdateToLatest(material)"
            theme="primary"
            variant="outline"
            size="small"
            @click="runUpdateLatest"
          >
            {{ $t('pm.materials.actions.updateLatest') }}
          </t-button>
          <!-- 删除后当前详情页失效，确认成功后返回列表 -->
          <t-button v-if="canDelete(material)" theme="danger" variant="outline" size="small" @click="confirmDelete">
            {{ $t('pm.materials.actions.delete') }}
          </t-button>
        </div>

        <!-- 归档记录只读提示 -->
        <p v-if="material.archived" class="mp-note">
          {{ $t('pm.materials.preview.archivedHint') }}
        </p>
      </section>

      <!-- 资料原文：Markdown 正文，与导出 docx/pptx 内容一致 -->
      <section class="mp-card">
        <h3 class="mp-section-title">{{ $t('pm.materials.preview.contentTitle') }}</h3>
        <PmMarkdown
          v-if="material.content?.trim()"
          class="mp-content"
          :content="material.content"
        />
        <p v-else class="mp-note mp-note--empty">{{ $t('pm.materials.preview.contentEmpty') }}</p>
      </section>

      <!-- 同类型历史归档：更新到最新版本后旧发布版保留在这里 -->
      <section class="mp-card">
        <h3 class="mp-section-title">{{ $t('pm.materials.preview.historyTitle') }}</h3>
        <p v-if="!archivedList.length" class="mp-note">{{ $t('pm.materials.preview.historyEmpty') }}</p>
        <t-table
          v-else
          row-key="id"
          :data="archivedList"
          :columns="historyColumns"
          size="small"
          hover
        >
          <template #title="{ row }">
            <t-button variant="text" size="small" class="mp-link" @click="openSibling(row)">
              {{ row.title }}
            </t-button>
          </template>
          <template #versionNo="{ row }">{{ row.version_no || '—' }}</template>
          <template #updatedAt="{ row }">{{ formatPmDateTime(row.updated_at) }}</template>
        </t-table>
      </section>
    </div>

    <!-- 生成进度：与列表页共用任务轮询组件 -->
    <t-dialog
      :visible="taskVisible"
      :header="$t('pm.materials.taskTitle')"
      width="520px"
      placement="center"
      :footer="false"
      @close="taskVisible = false"
    >
      <PmTaskProgress :task-id="activeTaskId" @done="onTaskDone" @failed="onTaskFailed" />
      <div class="mp-foot">
        <t-button variant="outline" @click="taskVisible = false">{{ $t('pm.materials.close') }}</t-button>
      </div>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute, useRouter } from 'vue-router';
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next';
import {
  deletePmMaterial,
  downloadPmMaterial,
  generatePmMaterial,
  getPmMaterial,
  listPmMaterials,
  publishPmMaterial,
  updatePmMaterialToLatest,
  type PmMaterial,
} from '@/api/pm';
import { downloadBlob } from '@/utils/downloadFile';
import PmTaskProgress from './components/PmTaskProgress.vue';
import PmMarkdown from './components/PmMarkdown.vue';
import {
  MATERIAL_ARCHIVED_META,
  MATERIAL_STATUS_MAP,
  MATERIAL_TYPE_MAP,
  canDelete,
  canDownload,
  canGenerate,
  canPublish,
  canUpdateToLatest,
  formatPmDateTime,
  materialFileName,
  materialHistory,
  sortMaterials,
} from './pmMaterialState';

const route = useRoute();
const router = useRouter();
const { t } = useI18n();

const projectId = computed(() => String(route.params.projectId || ''));
const materialId = computed(() => String(route.params.materialId || ''));

const loading = ref(false);
const material = ref<PmMaterial | null>(null);
const siblings = ref<PmMaterial[]>([]);

const taskVisible = ref(false);
const activeTaskId = ref('');

const typeMeta = computed(() => MATERIAL_TYPE_MAP[material.value?.material_type ?? 'manual']);
const statusMeta = computed(() => {
  if (material.value?.archived) return MATERIAL_ARCHIVED_META;
  return MATERIAL_STATUS_MAP[material.value?.status ?? 'draft'] ?? MATERIAL_STATUS_MAP.draft;
});

const historyColumns = computed(() => [
  { colKey: 'title', title: t('pm.materials.columns.title'), minWidth: 200, ellipsis: true },
  { colKey: 'versionNo', title: t('pm.materials.columns.version'), width: 120 },
  { colKey: 'updatedAt', title: t('pm.materials.preview.archivedAt'), width: 170 },
]);

// 历史归档：同项目同类型的归档记录，排除当前资料自身
const archivedList = computed(() => {
  if (!material.value) return [];
  return sortMaterials(materialHistory(siblings.value, material.value));
});

// ===== 数据加载 =====

async function loadDetail() {
  if (!materialId.value || !projectId.value) return;
  loading.value = true;
  try {
    material.value = await getPmMaterial(materialId.value);
    siblings.value = await listPmMaterials(projectId.value);
  } catch {
    material.value = null;
    MessagePlugin.error(t('pm.materials.preview.loadFailed'));
  } finally {
    loading.value = false;
  }
}

onMounted(() => void loadDetail());

watch(materialId, () => {
  material.value = null;
  siblings.value = [];
  taskVisible.value = false;
  void loadDetail();
});

// ===== 导航与操作 =====

function goBack() {
  void router.push({ name: 'pmMaterials', params: { projectId: projectId.value } });
}

// 归档记录之间可互相跳转查看
function openSibling(row: PmMaterial) {
  void router.push({
    name: 'pmMaterialPreview',
    params: { projectId: projectId.value, materialId: row.id },
  });
}

function startTask(taskId: string) {
  activeTaskId.value = taskId;
  taskVisible.value = true;
}

async function runGenerate() {
  if (!material.value) return;
  try {
    const task = await generatePmMaterial(material.value.id);
    startTask(task.id);
  } catch {
    MessagePlugin.error(t('pm.materials.actionFailed'));
  }
}

// 更新到最新：以最新版本重生成草稿，不覆盖已发布版本
async function runUpdateLatest() {
  if (!material.value) return;
  try {
    const task = await updatePmMaterialToLatest(material.value.id);
    startTask(task.id);
  } catch {
    MessagePlugin.error(t('pm.materials.actionFailed'));
  }
}

// 发布前二次确认：同类型旧发布版会被归档
function confirmPublish() {
  if (!material.value) return;
  const current = material.value;
  const dialog = DialogPlugin.confirm({
    header: t('pm.materials.publishConfirmTitle'),
    body: t('pm.materials.publishConfirmBody', { title: current.title }),
    confirmBtn: t('pm.materials.actions.publish'),
    cancelBtn: t('pm.materials.cancel'),
    onConfirm: async () => {
      dialog.hide();
      try {
        material.value = await publishPmMaterial(current.id);
        MessagePlugin.success(t('pm.materials.publishSuccess'));
        await loadDetail();
      } catch {
        MessagePlugin.error(t('pm.materials.actionFailed'));
      }
    },
  });
}

// 下载导出文件：后端按资料类型返回 docx/pptx
async function runDownload() {
  if (!material.value) return;
  const current = material.value;
  try {
    const blob = await downloadPmMaterial(current.id);
    downloadBlob(blob, materialFileName(current));
  } catch {
    MessagePlugin.error(t('pm.materials.downloadFailed'));
  }
}

// 删除仅对草稿开放；成功后当前详情已不存在，需返回列表
function confirmDelete() {
  if (!material.value) return;
  const current = material.value;
  const dialog = DialogPlugin.confirm({
    header: t('pm.materials.deleteConfirmTitle'),
    body: t('pm.materials.deleteConfirmBody', { title: current.title }),
    confirmBtn: { content: t('pm.materials.actions.delete'), theme: 'danger' as const },
    cancelBtn: t('pm.materials.cancel'),
    onConfirm: async () => {
      dialog.hide();
      try {
        await deletePmMaterial(current.id);
        MessagePlugin.success(t('pm.materials.deleteSuccess'));
        goBack();
      } catch {
        MessagePlugin.error(t('pm.materials.deleteFailed'));
      }
    },
  });
}

function onTaskDone() {
  MessagePlugin.success(t('pm.materials.taskSuccess'));
  void loadDetail();
}

function onTaskFailed() {
  MessagePlugin.error(t('pm.materials.taskFailed'));
  void loadDetail();
}
</script>

<style lang="less" scoped>
.pm-material-preview {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  padding: var(--app-space-6);
}

.mp-head {
  margin-bottom: var(--app-space-3);
}

.mp-state {
  flex: 1;
  min-height: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}

.mp-empty {
  flex-direction: column;
  gap: var(--app-space-2);
  border: 1px dashed var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
  color: var(--td-text-color-placeholder);
}

.mp-empty-title {
  font-size: var(--app-text-lg);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.mp-body {
  flex: 1;
  min-height: 0;
  overflow: auto;
  display: flex;
  flex-direction: column;
  gap: var(--app-space-4);
}

.mp-card {
  padding: var(--app-space-4);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
  background: var(--td-bg-color-container);
}

.mp-title-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--app-space-2);
}

.mp-title {
  margin: 0;
  font-size: var(--app-text-lg);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.mp-info {
  display: flex;
  flex-wrap: wrap;
  gap: var(--app-space-6);
  margin-top: var(--app-space-4);
}

.mp-info__item {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.mp-info__label {
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}

.mp-info__value {
  font-size: var(--app-text-md);
  color: var(--td-text-color-primary);
}

.mp-actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--app-space-2);
  margin-top: var(--app-space-4);
}

.mp-note {
  margin: var(--app-space-3) 0 0;
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}

.mp-note--empty {
  margin-top: 0;
  font-size: var(--app-text-md);
}

/* 资料原文正文：保持可读行高，长文在卡片内自然撑开 */
.mp-content {
  font-size: var(--app-text-md);
  line-height: 1.75;
  color: var(--td-text-color-primary);
}

.mp-section-title {
  margin: 0 0 var(--app-space-3);
  font-size: var(--app-text-md);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.mp-link {
  padding-left: 0;
  font-weight: 600;
}

.mp-foot {
  display: flex;
  justify-content: flex-end;
  margin-top: var(--app-space-4);
}
</style>
