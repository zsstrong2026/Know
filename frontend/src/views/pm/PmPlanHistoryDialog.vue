<template>
  <!-- 规划历史：批次列表 + 快照只读查看（快照正文由批次草稿重建） -->
  <t-dialog
    :visible="visible"
    width="980px"
    placement="center"
    :header="dialogTitle"
    :confirm-btn="{ content: $t('pm.roadmap.snapshot.close') }"
    :cancel-btn="null"
    :on-confirm="close"
    :on-close="close"
    @close="close"
  >
    <div v-if="snapshotBatch" class="ph-snapshot">
      <div class="ph-snapshot__head">
        <t-button variant="text" size="small" @click="snapshotBatch = null">
          <template #icon><t-icon name="chevron-left" /></template>
          {{ $t('pm.roadmap.snapshot.back') }}
        </t-button>
        <span class="ph-snapshot__note">{{ $t('pm.roadmap.snapshot.note') }}</span>
      </div>
      <div class="ph-snapshot__body">
        <PmMarkdown :content="snapshotMarkdown" />
      </div>
    </div>

    <div v-else class="ph-table">
      <t-table
        row-key="id"
        :data="batches"
        :columns="columns"
        :loading="loading"
        size="medium"
        hover
      >
        <template #plan_type="{ row }">{{ planTypeLabel(row.plan_type) }}</template>
        <template #version="{ row }">{{ versionLabel(row.version_id) }}</template>
        <template #change_summary="{ row }">{{ row.change_summary || '—' }}</template>
        <template #operator="{ row }">{{ row.operator_id || row.created_by || '—' }}</template>
        <template #created_at="{ row }">{{ formatArtifactDateTime(row.created_at) }}</template>
        <template #status="{ row }">
          <t-tag size="small" variant="light" :theme="statusTheme(row.status)">
            {{ statusLabel(row.status) }}
          </t-tag>
        </template>
        <template #actions="{ row }">
          <!-- 未生成的批次没有快照，按钮禁用并给出原因 -->
          <t-tooltip v-if="!row.generated_at" :content="$t('pm.roadmap.snapshot.notGenerated')">
            <span>
              <t-button variant="text" size="small" disabled>
                {{ $t('pm.roadmap.snapshot.view') }}
              </t-button>
            </span>
          </t-tooltip>
          <t-button v-else variant="text" size="small" @click="openSnapshot(row)">
            {{ $t('pm.roadmap.snapshot.view') }}
          </t-button>
        </template>
      </t-table>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  listPlanBatches,
  listPmVersions,
  type PmPlanBatch,
  type PmPlanBatchStatus,
  type PmPlanType,
  type PmVersion,
} from '@/api/pm';
import { formatArtifactDateTime } from '@/utils/sessionArtifacts';
import { buildRoadmapMarkdown } from './pmRoadmapState';
import PmMarkdown from './components/PmMarkdown.vue';

const props = defineProps<{
  visible: boolean;
  projectId: string;
  projectName: string;
  // 指定批次时，打开弹窗直接展示该批次快照
  initialSnapshotId?: string;
}>();
const emit = defineEmits<{ 'update:visible': [value: boolean] }>();

const { t } = useI18n();

const loading = ref(false);
const batches = ref<PmPlanBatch[]>([]);
const versions = ref<PmVersion[]>([]);
const snapshotBatch = ref<PmPlanBatch | null>(null);

// 快照正文未由接口返回（仅返回知识库指针），按批次草稿只读重建
const snapshotMarkdown = computed(() =>
  snapshotBatch.value ? buildRoadmapMarkdown(props.projectName, snapshotBatch.value.draft) : '',
);

const dialogTitle = computed(() => (
  snapshotBatch.value
    ? t('pm.roadmap.snapshot.titleWith', { name: snapshotBatch.value.batch_name })
    : t('pm.roadmap.history.title')
));

const versionMap = computed(() => {
  const map = new Map<string, string>();
  for (const version of versions.value) {
    map.set(version.id, [version.version_no, version.name].filter(Boolean).join(' '));
  }
  return map;
});

const columns = computed(() => [
  { colKey: 'batch_name', title: t('pm.roadmap.history.columns.name'), minWidth: 170, ellipsis: true },
  { colKey: 'plan_type', title: t('pm.roadmap.history.columns.type'), width: 110 },
  { colKey: 'version', title: t('pm.roadmap.history.columns.version'), width: 130, ellipsis: true },
  { colKey: 'change_summary', title: t('pm.roadmap.history.columns.summary'), minWidth: 180, ellipsis: true },
  { colKey: 'operator', title: t('pm.roadmap.history.columns.operator'), width: 150, ellipsis: true },
  { colKey: 'created_at', title: t('pm.roadmap.history.columns.time'), width: 150 },
  { colKey: 'status', title: t('pm.roadmap.history.columns.status'), width: 100 },
  { colKey: 'actions', title: t('pm.roadmap.history.columns.actions'), width: 110 },
]);

// 打开时并行拉取批次与版本（版本仅用于回显关联版本名称）
// initialSnapshotId：拉取完成后若命中已生成批次，直接进入快照视图
async function load(initialSnapshotId = '') {
  if (!props.projectId) return;
  loading.value = true;
  const [batchResult, versionResult] = await Promise.allSettled([
    listPlanBatches(props.projectId),
    listPmVersions(props.projectId),
  ]);
  loading.value = false;
  if (batchResult.status === 'fulfilled') {
    batches.value = batchResult.value;
  } else {
    batches.value = [];
    MessagePlugin.error(t('pm.roadmap.snapshot.loadFailed'));
  }
  versions.value = versionResult.status === 'fulfilled' ? versionResult.value : [];

  if (initialSnapshotId) {
    const target = batches.value.find((item) => item.id === initialSnapshotId && item.generated_at);
    if (target) snapshotBatch.value = target;
  }
}

watch(
  () => props.visible,
  (visible) => {
    if (visible) {
      snapshotBatch.value = null;
      void load(props.initialSnapshotId || '');
    }
  },
);

function close() {
  emit('update:visible', false);
}

function openSnapshot(batch: PmPlanBatch) {
  snapshotBatch.value = batch;
}

function planTypeLabel(type: PmPlanType): string {
  return t(`pm.roadmap.planType.${type}`);
}

function versionLabel(versionId: string): string {
  if (!versionId) return '—';
  return versionMap.value.get(versionId) || versionId;
}

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
.ph-table {
  min-height: 200px;
}

.ph-snapshot__head {
  display: flex;
  align-items: center;
  gap: var(--app-space-3);
  padding-bottom: var(--app-space-2);
  border-bottom: 1px solid var(--td-component-stroke);
}

.ph-snapshot__note {
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}

.ph-snapshot__body {
  margin-top: var(--app-space-3);
  max-height: 60vh;
  overflow-y: auto;
  padding-right: var(--app-space-2);
}
</style>
