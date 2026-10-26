<template>
  <!-- 新增功能点：模块可新建或选已有，操作项按逗号拆成多条记录 -->
  <t-dialog
    :visible="createVisible"
    :header="$t('pm.features.createForm.title')"
    width="640px"
    placement="center"
    :footer="false"
    @close="createVisible = false"
  >
    <t-form label-align="top">
      <t-form-item :label="$t('pm.features.createForm.module')" required-mark>
        <t-select
          v-model="draft.module_name"
          :options="moduleOptions"
          :placeholder="$t('pm.features.createForm.modulePlaceholder')"
          filterable
          creatable
        />
      </t-form-item>
      <t-form-item :label="$t('pm.features.createForm.feature')" required-mark>
        <t-input
          v-model="draft.feature_name"
          :maxlength="100"
          :placeholder="$t('pm.features.createForm.featurePlaceholder')"
        />
      </t-form-item>
      <t-form-item :label="$t('pm.features.createForm.operations')">
        <t-input
          v-model="draft.operations"
          :placeholder="$t('pm.features.createForm.operationsPlaceholder')"
        />
        <template #help>
          <span class="fd-hint">{{ $t('pm.features.createForm.operationsHint') }}</span>
        </template>
      </t-form-item>
      <t-form-item :label="$t('pm.features.createForm.requirement')">
        <t-select
          v-model="draft.requirement_id"
          :options="requirementOptions"
          :placeholder="$t('pm.features.createForm.requirementPlaceholder')"
          clearable
          filterable
        />
      </t-form-item>
      <t-form-item :label="$t('pm.features.createForm.version')">
        <t-select
          v-model="draft.version_id"
          :options="versionOptions"
          :placeholder="$t('pm.features.createForm.versionPlaceholder')"
          clearable
        />
        <template #help>
          <span class="fd-hint">{{ $t('pm.features.createForm.versionHint') }}</span>
        </template>
      </t-form-item>
    </t-form>
    <div class="fd-foot">
      <t-button variant="outline" @click="createVisible = false">
        {{ $t('pm.features.createForm.cancel') }}
      </t-button>
      <t-button theme="primary" :loading="saving" @click="submitCreate">
        {{ $t('pm.features.createForm.submit') }}
      </t-button>
    </div>
  </t-dialog>

  <!-- 迭代历史：按版本计划上线日升序 -->
  <t-drawer
    :visible="historyVisible"
    :header="historyHeader"
    size="560px"
    :footer="false"
    @close="historyVisible = false"
  >
    <div v-if="loadingHistory" class="fd-state"><t-loading /></div>
    <div v-else-if="!historyRows.length" class="fd-state">
      <t-icon name="history" size="32px" />
      <p>{{ $t('pm.features.history.empty') }}</p>
    </div>
    <t-table
      v-else
      row-key="version_id"
      size="small"
      :data="historyRows"
      :columns="historyColumns"
      :bordered="false"
    >
      <template #version="{ row }">
        <span class="fd-version">
          {{ featureVersionLabel(row.version_no) || row.version_name || $t('pm.features.unfinalized') }}
        </span>
      </template>
      <template #change="{ row }">
        <t-tag :theme="changeMeta(row.change_type).theme" variant="light" size="small">
          {{ $t(changeMeta(row.change_type).labelKey) }}
        </t-tag>
      </template>
      <template #planLaunchDate="{ row }">{{ formatPmDate(row.plan_launch_date) }}</template>
      <template #changedAt="{ row }">{{ formatPmDate(row.changed_at) }}</template>
      <template #note="{ row }">
        <span class="fd-note">{{ row.change_note || '—' }}</span>
      </template>
    </t-table>
    <p v-if="historyRows.length" class="fd-note-tip">{{ $t('pm.features.history.note') }}</p>
  </t-drawer>
</template>

<script setup lang="ts">
import { computed, reactive, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  createPmFeature,
  listPmFeatureIterations,
  type PmRequirement,
  type PmVersion,
} from '@/api/pm';
import {
  FEATURE_CHANGE_MAP,
  featureVersionLabel,
  sortFeatureIterations,
  type FeatureIterationRow,
  type FeatureTreeNode,
} from './pmFeatureTree';
import { formatPmDate } from './pmVersionState';

const props = defineProps<{
  projectId: string;
  // 已有模块名，供下拉选择
  modules: string[];
  // 来源需求与迭代版本候选由页面统一加载，避免重复请求
  requirements: PmRequirement[];
  versions: PmVersion[];
}>();

const emit = defineEmits<{ changed: [] }>();
const { t } = useI18n();

const createVisible = ref(false);
const saving = ref(false);
const draft = reactive({
  module_name: '',
  feature_name: '',
  operations: '',
  requirement_id: '',
  version_id: '',
});

const moduleOptions = computed(() =>
  props.modules.map((name) => ({ label: name, value: name })),
);
const requirementOptions = computed(() =>
  props.requirements
    .filter((item) => item.status === 'confirmed' || item.status === 'scheduled')
    .map((item) => ({
      label: `${item.title}（${t(`pm.requirement.status.${item.status}`)}）`,
      value: item.id,
    })),
);
const versionOptions = computed(() =>
  props.versions.map((item) => ({
    label: featureVersionLabel(item.version_no) || item.name || t('pm.features.unfinalized'),
    value: item.id,
  })),
);

// ===== 新增功能点 =====

function openCreate() {
  Object.assign(draft, {
    module_name: '',
    feature_name: '',
    operations: '',
    requirement_id: '',
    version_id: '',
  });
  createVisible.value = true;
}

// 逗号（中英文）/ 顿号 / 换行均可分隔操作项，去空并去重
function splitOperations(text: string): string[] {
  const parts = text
    .split(/[,，、\n]/)
    .map((item) => item.trim())
    .filter(Boolean);
  return Array.from(new Set(parts));
}

async function submitCreate() {
  if (saving.value) return;
  const moduleName = draft.module_name.trim();
  const featureName = draft.feature_name.trim();
  if (!moduleName) {
    MessagePlugin.warning(t('pm.features.createForm.moduleRequired'));
    return;
  }
  if (!featureName) {
    MessagePlugin.warning(t('pm.features.createForm.featureRequired'));
    return;
  }

  saving.value = true;
  try {
    // 后端一次只能建一条记录，操作项需要逐条提交；不填操作项时功能点自身即为叶子
    const operations = splitOperations(draft.operations);
    const base = {
      project_id: props.projectId,
      module_name: moduleName,
      feature_name: featureName,
      requirement_id: draft.requirement_id,
      version_id: draft.version_id,
    };
    if (operations.length) {
      for (const operation of operations) {
        await createPmFeature({ ...base, operation_name: operation });
      }
    } else {
      await createPmFeature(base);
    }
    MessagePlugin.success(t('pm.features.createForm.success'));
    createVisible.value = false;
    emit('changed');
  } catch {
    MessagePlugin.error(t('pm.features.createForm.failed'));
  } finally {
    saving.value = false;
  }
}

// ===== 迭代历史 =====

const historyVisible = ref(false);
const loadingHistory = ref(false);
const historyRows = ref<FeatureIterationRow[]>([]);
const historyFeature = ref<FeatureTreeNode | null>(null);

const historyHeader = computed(() => {
  const name = historyFeature.value?.name ?? '';
  return name ? t('pm.features.history.titleWith', { name }) : t('pm.features.history.title');
});

const historyColumns = computed(() => [
  { colKey: 'version', title: t('pm.features.history.columns.version'), width: 140 },
  { colKey: 'change', title: t('pm.features.history.columns.change'), width: 88 },
  { colKey: 'planLaunchDate', title: t('pm.features.history.columns.planLaunchDate'), width: 116 },
  { colKey: 'changedAt', title: t('pm.features.history.columns.changedAt'), width: 116 },
  { colKey: 'note', title: t('pm.features.history.columns.note'), minWidth: 160, ellipsis: true },
]);

function changeMeta(type: string) {
  return FEATURE_CHANGE_MAP[type as keyof typeof FEATURE_CHANGE_MAP] ?? FEATURE_CHANGE_MAP.add;
}

async function openHistory(row: FeatureTreeNode) {
  if (!row.feature_id) return;
  historyFeature.value = row;
  historyRows.value = [];
  historyVisible.value = true;
  loadingHistory.value = true;
  try {
    const list = await listPmFeatureIterations(row.feature_id);
    // 接口不返回版本号，需用版本列表补全后按计划上线日排序
    historyRows.value = sortFeatureIterations(list, props.versions);
  } catch {
    MessagePlugin.error(t('pm.features.history.loadFailed'));
  } finally {
    loadingHistory.value = false;
  }
}

defineExpose({ openCreate, openHistory });
</script>

<style lang="less" scoped>
.fd-hint {
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}

.fd-foot {
  display: flex;
  justify-content: flex-end;
  gap: var(--app-space-2);
  margin-top: var(--app-space-4);
}

.fd-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--app-space-2);
  min-height: 160px;
  color: var(--td-text-color-placeholder);
}

.fd-version {
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.fd-note {
  color: var(--td-text-color-secondary);
}

.fd-note-tip {
  margin-top: var(--app-space-3);
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}
</style>
