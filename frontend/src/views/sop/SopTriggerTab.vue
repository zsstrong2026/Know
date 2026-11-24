<template>
  <div class="sop-trigger">
    <header class="sop-trigger-head">
      <div>
        <h2 class="sop-trigger-title">{{ $t('sop.trig.title') }}</h2>
        <p class="sop-trigger-desc">{{ $t('sop.trig.desc') }}</p>
      </div>
      <div class="sop-trigger-actions">
        <t-button size="small" variant="outline" @click="openConfig">{{ $t('sop.trig.openConfig') }}</t-button>
        <t-button size="small" theme="primary" :loading="running" :disabled="!config?.enabled && !config" @click="runNowClick">
          {{ $t('sop.trig.runNow') }}
        </t-button>
      </div>
    </header>

    <div v-if="!config" class="sop-empty">{{ $t('sop.trig.noConfig') }}</div>

    <template v-else>
      <div class="sop-summary-card">
        <div class="sop-summary-kv"><span class="k">{{ $t('sop.trig.schedule') }}</span><span class="v">{{ intervalSummary(config) }}</span></div>
        <div class="sop-summary-kv"><span class="k">{{ $t('sop.trig.defaultTemplate') }}</span><span class="v">{{ defaultTemplateName }}</span></div>
        <div class="sop-summary-kv"><span class="k">{{ $t('sop.trig.sources') }}</span><span class="v">{{ sourceLabel(config.sources) }}</span></div>
        <div class="sop-summary-kv"><span class="k">{{ $t('sop.trig.statuses') }}</span><span class="v">{{ statusLabels }}</span></div>
        <div class="sop-summary-kv"><span class="k">{{ $t('sop.trig.perRunLimit') }}</span><span class="v">{{ config.per_run_limit }}</span></div>
        <div class="sop-summary-kv"><span class="k">{{ $t('sop.trig.enabled') }}</span><span class="v">{{ config.enabled ? $t('sop.trig.enabledYes') : $t('sop.trig.enabledNo') }}</span></div>
        <div class="sop-summary-kv"><span class="k">{{ $t('sop.trig.lastRun') }}</span><span class="v">{{ formatTime(config.last_run_at) }}</span></div>
        <div class="sop-summary-kv"><span class="k">{{ $t('sop.trig.nextRun') }}</span><span class="v">{{ formatTime(config.next_run_at) }}</span></div>
      </div>

      <section class="sop-logs">
        <header class="sop-logs-head">
          <b>{{ $t('sop.trig.logsTitle') }}</b>
          <t-button size="small" variant="text" :loading="logsLoading" @click="loadLogs">⟳</t-button>
        </header>
        <table class="sop-table">
          <thead>
            <tr>
              <th>{{ $t('sop.trig.colTime') }}</th>
              <th>{{ $t('sop.trig.colKind') }}</th>
              <th>{{ $t('sop.trig.colScanned') }}</th>
              <th>{{ $t('sop.trig.colFired') }}</th>
              <th>{{ $t('sop.trig.colSkipped') }}</th>
              <th>{{ $t('sop.trig.colNote') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="log in logs" :key="log.id">
              <td>{{ formatTime(log.created_at) }}</td>
              <td>{{ log.kind === 'manual' ? $t('sop.trig.kindManual') : $t('sop.trig.kindAuto') }}</td>
              <td>{{ log.scanned }}</td>
              <td>{{ log.fired }}</td>
              <td>{{ log.skipped }}</td>
              <td class="sop-note">{{ log.note || '—' }}</td>
            </tr>
            <tr v-if="!logs.length">
              <td colspan="6" class="sop-empty-row">{{ $t('sop.trig.noLogs') }}</td>
            </tr>
          </tbody>
        </table>
      </section>
    </template>

    <SopTriggerConfigModal
      v-model:visible="configVisible"
      :project-id="store.currentProjectId"
      :config="config"
      @saved="onSaved"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { storeToRefs } from 'pinia';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  getTriggerConfig,
  listTriggerLogs,
  runNow,
  type SopTriggerConfig,
  type SopTriggerLog,
} from '@/api/sop';
import { ITEM_STATUS_LABELS, SOURCE_LABELS, intervalSummary } from '@/api/sop/helpers';
import { useSopStore } from '@/stores/sop';
import SopTriggerConfigModal from './components/SopTriggerConfigModal.vue';

const { t } = useI18n();
const store = useSopStore();
const { templates } = storeToRefs(store);

const config = ref<SopTriggerConfig | null>(null);
const logs = ref<SopTriggerLog[]>([]);
const configVisible = ref(false);
const running = ref(false);
const logsLoading = ref(false);
let stopPolling: (() => void) | null = null;

const defaultTemplateName = computed(() => {
  const id = config.value?.default_template_id;
  if (!id) return t('sop.trig.defaultTemplateEmpty');
  return (templates.value || []).find((tpl) => tpl.id === id)?.name || id;
});

const statusLabels = computed(() =>
  (config.value?.statuses || [])
    .map((status) => (ITEM_STATUS_LABELS as Record<string, string>)[status] || status)
    .join('、') || '—',
);

function sourceLabel(sources: string): string {
  if (!sources || sources === 'all') return t('sop.trig.sourceAll');
  return sources
    .split(',')
    .map((key) => SOURCE_LABELS[key.trim()] || key)
    .join('、');
}

function formatTime(value?: string | null): string {
  if (!value) return '—';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

async function loadConfig() {
  const projectId = store.currentProjectId;
  if (!projectId) {
    config.value = null;
    return;
  }
  try {
    config.value = await getTriggerConfig(projectId);
  } catch {
    config.value = null;
  }
}

async function loadLogs() {
  const projectId = store.currentProjectId;
  if (!projectId) return;
  logsLoading.value = true;
  try {
    logs.value = await listTriggerLogs(projectId);
  } catch {
    logs.value = [];
  } finally {
    logsLoading.value = false;
  }
}

function openConfig() {
  configVisible.value = true;
}

async function onSaved(saved: SopTriggerConfig) {
  config.value = saved;
  await loadLogs();
}

/**
 * 立即运行：服务端后台扫描（202），关闭页面不影响。
 * 轮询历史表直到出现 manual 行，给用户一个确定的结果。
 */
async function runNowClick() {
  const projectId = store.currentProjectId;
  if (!projectId) return;
  running.value = true;
  try {
    await runNow(projectId);
    MessagePlugin.success(t('sop.trig.runNowStarted'));
    await waitForManualLog();
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.trig.runNowFailed'));
  } finally {
    running.value = false;
  }
}

async function waitForManualLog() {
  for (let i = 0; i < 10; i++) {
    await new Promise((resolve) => setTimeout(resolve, 1500));
    await loadLogs();
    if (logs.value.some((log) => log.kind === 'manual')) break;
  }
}

watch(
  () => store.currentProjectId,
  async () => {
    await loadConfig();
    await loadLogs();
  },
);

onMounted(async () => {
  if (!templates.value.length) await store.loadTemplates().catch(() => undefined);
  await loadConfig();
  await loadLogs();
  stopPolling = store.startPolling(() => void loadConfig(), 30_000);
});

onUnmounted(() => stopPolling?.());
</script>

<style lang="less" scoped src="./sopTriggerTab.less"></style>
