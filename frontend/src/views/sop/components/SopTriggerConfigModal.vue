<template>
  <t-dialog
    v-if="visible"
    :visible="true"
    width="640px"
    placement="center"
    :header="$t('sop.trig.configTitle')"
    :confirm-btn="{ content: $t('sop.trig.save'), loading: saving }"
    :cancel-btn="$t('common.cancel')"
    :on-confirm="submit"
    :on-close="close"
    @close="close"
  >
    <div class="sop-trig-form">
      <div class="sop-field">
        <span>{{ $t('sop.trig.mode') }}</span>
        <t-radio-group v-model="form.mode" variant="default-filled">
          <t-radio-button value="interval">{{ $t('sop.trig.modeInterval') }}</t-radio-button>
          <t-radio-button value="cron">{{ $t('sop.trig.modeCron') }}</t-radio-button>
        </t-radio-group>
      </div>

      <template v-if="form.mode === 'interval'">
        <label class="sop-field">
          <span>{{ $t('sop.trig.interval') }}</span>
          <t-select v-model="form.interval_key" :options="intervalOptions" />
        </label>
      </template>
      <template v-else>
        <div class="sop-cols2">
          <label class="sop-field">
            <span>{{ $t('sop.trig.cron') }}</span>
            <t-input v-model="form.cron" class="mono" placeholder="0 9 * * *" />
          </label>
          <label class="sop-field">
            <span>{{ $t('sop.trig.timezone') }}</span>
            <t-input v-model="form.timezone" class="mono" placeholder="Asia/Shanghai" />
          </label>
        </div>
        <small class="sop-hint">{{ $t('sop.trig.cronHint') }}</small>
      </template>

      <label class="sop-field">
        <span>{{ $t('sop.trig.defaultTemplate') }}</span>
        <t-select v-model="form.default_template_id" :options="templateOptions" clearable
          :placeholder="$t('sop.trig.defaultTemplateEmpty')" />
      </label>

      <div class="sop-switchrow">
        <t-switch v-model="form.prefer_item_template" />
        <span>{{ $t('sop.trig.preferItemTemplate') }}</span>
      </div>

      <label class="sop-field">
        <span>{{ $t('sop.trig.sources') }}</span>
        <t-select v-model="form.sources" :options="sourceOptions" />
      </label>

      <div class="sop-field">
        <span>{{ $t('sop.trig.statuses') }}</span>
        <div class="sop-status-grid">
          <label v-for="status in statusOptions" :key="status.value" class="sop-status-item">
            <input
              type="checkbox"
              :checked="form.statuses.includes(status.value)"
              @change="toggleStatus(status.value, ($event.target as HTMLInputElement).checked)"
            />
            <span>{{ status.label }}</span>
          </label>
        </div>
        <small class="sop-hint">{{ $t('sop.trig.statusesHint') }}</small>
      </div>

      <label class="sop-field">
        <span>{{ $t('sop.trig.perRunLimit') }}</span>
        <t-input-number v-model="form.per_run_limit" :min="1" :max="10" />
        <small class="sop-hint">{{ $t('sop.trig.perRunLimitHint') }}</small>
      </label>

      <div class="sop-switchrow">
        <t-switch v-model="form.enabled" />
        <span>{{ $t('sop.trig.enabled') }}</span>
      </div>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import { storeToRefs } from 'pinia';
import {
  saveTriggerConfig,
  type SopTriggerConfig,
} from '@/api/sop';
import { ITEM_STATUS_LABELS, SOURCE_LABELS } from '@/api/sop/helpers';
import { useSopStore } from '@/stores/sop';

const props = defineProps<{
  visible: boolean;
  projectId: string;
  config: SopTriggerConfig | null;
}>();

const emit = defineEmits<{ 'update:visible': [boolean]; saved: [SopTriggerConfig] }>();

const { t } = useI18n();
const store = useSopStore();
const { templates } = storeToRefs(store);

const form = reactive({
  mode: 'interval' as 'interval' | 'cron',
  interval_key: 'daily9',
  cron: '',
  timezone: 'Asia/Shanghai',
  default_template_id: '',
  prefer_item_template: true,
  sources: 'all',
  statuses: ['backlog', 'todo'] as string[],
  per_run_limit: 10,
  enabled: false,
});

const submitting = ref(false);
const saving = computed(() => submitting.value);

const intervalOptions = computed(() => [
  { value: '15m', label: t('sop.trig.interval15m') },
  { value: '30m', label: t('sop.trig.interval30m') },
  { value: 'hourly', label: t('sop.trig.intervalHourly') },
  { value: 'daily9', label: t('sop.trig.intervalDaily9') },
  { value: 'weekly1', label: t('sop.trig.intervalWeekly1') },
]);

const templateOptions = computed(() =>
  (templates.value || [])
    .filter((tpl) => tpl.status === 'published')
    .map((tpl) => ({ value: tpl.id, label: tpl.name })),
);

const sourceOptions = computed(() => [
  { value: 'all', label: t('sop.trig.sourceAll') },
  ...Object.entries(SOURCE_LABELS).map(([value, label]) => ({ value, label })),
]);

/**
 * 状态多选：终态（已完成/已关闭）永远不出现——扫描到终态也永远不会派发，
 * 放出来只会让人误配。
 */
const statusOptions = computed(() =>
  (Object.keys(ITEM_STATUS_LABELS) as (keyof typeof ITEM_STATUS_LABELS)[])
    .filter((status) => status !== 'done' && status !== 'closed')
    .map((status) => ({ value: status, label: ITEM_STATUS_LABELS[status] })),
);

watch(
  () => props.visible,
  (open) => {
    if (!open) return;
    const config = props.config;
    if (config) {
      form.mode = config.mode === 'cron' ? 'cron' : 'interval';
      form.interval_key = config.interval_key || 'daily9';
      form.cron = config.cron || '';
      form.timezone = config.timezone || 'Asia/Shanghai';
      form.default_template_id = config.default_template_id || '';
      form.prefer_item_template = !!config.prefer_item_template;
      form.sources = config.sources || 'all';
      form.statuses = [...(config.statuses || ['backlog', 'todo'])];
      form.per_run_limit = config.per_run_limit || 10;
      form.enabled = !!config.enabled;
    }
  },
);

function toggleStatus(value: string, checked: boolean) {
  form.statuses = checked
    ? [...new Set([...form.statuses, value])]
    : form.statuses.filter((status) => status !== value);
}

function close() {
  emit('update:visible', false);
}

async function submit() {
  if (submitting.value) return;
  submitting.value = true;
  try {
    const saved = await saveTriggerConfig(props.projectId, {
      mode: form.mode,
      interval_key: form.mode === 'interval' ? form.interval_key : '',
      cron: form.mode === 'cron' ? form.cron.trim() : '',
      timezone: form.timezone.trim() || 'Asia/Shanghai',
      default_template_id: form.default_template_id || '',
      prefer_item_template: form.prefer_item_template,
      sources: form.sources,
      statuses: form.statuses,
      per_run_limit: form.per_run_limit,
      enabled: form.enabled,
    });
    MessagePlugin.success(t('sop.trig.saved'));
    emit('saved', saved);
    close();
  } catch (error: any) {
    // 后端校验文案是白话（cron 间隔/条数越界），原样透出
    MessagePlugin.error(error?.message || t('sop.trig.saveFailed'));
  } finally {
    submitting.value = false;
  }
}
</script>

<style lang="less" scoped>
.sop-trig-form {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-2);
}

.sop-field {
  display: flex;
  flex-direction: column;
  gap: 6px;

  > span {
    color: var(--td-text-color-secondary);
    font-size: var(--app-text-sm);
  }
}

.sop-cols2 {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--app-space-2);
}

.sop-switchrow {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
}

.sop-hint {
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-xs);
}

.mono :deep(input) {
  font-family: var(--app-font-family-mono);
}

.sop-status-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(120px, 1fr));
  gap: 6px;
}

.sop-status-item {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: var(--app-text-sm);
  cursor: pointer;
}
</style>
