<template>
  <div class="sop-runs">
    <header class="sop-runs-head">
      <div>
        <h2 class="sop-runs-title">
          {{ $t('sop.runsList.title') }}
          <span class="sop-live" :class="{ on: activeCount > 0 }">
            <i /> {{ activeCount > 0 ? $t('sop.runsList.active', { n: activeCount }) : $t('sop.runsList.idle') }}
          </span>
        </h2>
        <p class="sop-runs-desc">{{ $t('sop.runsList.desc') }}</p>
      </div>
      <div class="sop-runs-stats">
        <div><div class="n">{{ successRate(stats) }}</div><div class="l">{{ $t('sop.runsList.successRate') }}</div></div>
        <div><div class="n">{{ stats.finished_count || 0 }}</div><div class="l">{{ $t('sop.runsList.finished') }}</div></div>
        <div><div class="n">{{ tokenText }}</div><div class="l">{{ $t('sop.runsList.tokens') }}</div></div>
        <div><div class="n">{{ stats.total_count || 0 }}</div><div class="l">{{ $t('sop.runsList.total') }}</div></div>
        <t-button size="small" variant="outline" :loading="refreshing" @click="refreshAll">⟳</t-button>
      </div>
    </header>

    <div class="sop-run-chips">
      <button
        v-for="chip in chips"
        :key="chip.value"
        type="button"
        class="sop-run-chip"
        :class="[chip.class, { on: filter.status === chip.value }]"
        @click="pickStatus(chip.value)"
      >
        {{ chip.label }}
        <span class="cnt">{{ chip.count }}</span>
      </button>
    </div>

    <div class="sop-run-filter">
      <input
        v-model="search"
        class="sop-search"
        type="text"
        :placeholder="$t('sop.runsList.searchPlaceholder')"
        @input="onSearchInput"
      />
      <t-select
        :model-value="filter.template_id || ''"
        class="sop-flt"
        :options="templateOptions"
        @update:model-value="(v: any) => setTemplate(v)"
      />
    </div>

    <div class="sop-table-wrap">
      <table class="sop-table">
        <thead>
          <tr>
            <th class="col-id">{{ $t('sop.runsList.colRun') }}</th>
            <th>{{ $t('sop.runsList.colItem') }}</th>
            <th class="col-tpl">{{ $t('sop.runsList.colSop') }}</th>
            <th class="col-src">{{ $t('sop.runsList.colTrigger') }}</th>
            <th class="col-status">{{ $t('sop.runsList.colStatus') }}</th>
            <th class="col-time">{{ $t('sop.runsList.colStarted') }}</th>
            <th class="col-usage">{{ $t('sop.runsList.colUsage') }}</th>
            <th class="col-actions">{{ $t('sop.runsList.colActions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="run in store.runs" :key="run.id" class="sop-run-row" @click="openRun(run)">
            <td class="col-id mono">{{ shortId(run.id) }}</td>
            <td>
              <span v-if="itemLabel(run.item_id)">{{ itemLabel(run.item_id) }}</span>
              <span v-else class="sop-muted">—</span>
            </td>
            <td class="col-tpl">
              {{ templateLabel(run.template_id) }}
              <span class="sop-muted">· v{{ revisionLabel(run) }}</span>
            </td>
            <td class="col-src">{{ triggerSourceLabel(run.trigger_kind) }}</td>
            <td class="col-status">
              <span class="sop-run-status" :class="`is-${run.status}`">{{ runStatusLabel(run.status) }}</span>
            </td>
            <td class="col-time">{{ formatTime(run.started_at || run.created_at) }}</td>
            <td class="col-usage mono">
              {{ (run.usage?.input_tokens || 0) + (run.usage?.output_tokens || 0) }}
              <span class="sop-muted" v-if="run.usage?.cost_usd">· ${{ run.usage.cost_usd.toFixed(3) }}</span>
            </td>
            <td class="col-actions" @click.stop>
              <t-button v-if="run.status === 'failed' || run.status === 'cancelled'" size="small" theme="danger" variant="outline"
                :loading="retrying === run.id" @click="retry(run)">
                {{ $t('sop.runsList.retry') }}
              </t-button>
              <t-button size="small" variant="outline" @click="openRun(run)">
                {{ $t('sop.runsList.detail') }}
              </t-button>
            </td>
          </tr>
          <tr v-if="!store.runs.length">
            <td colspan="8" class="sop-empty-row">{{ $t('sop.runsList.empty') }}</td>
          </tr>
        </tbody>
      </table>
    </div>

    <div class="sop-count">{{ $t('sop.runsList.count', { n: store.runs.length }) }}</div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import { storeToRefs } from 'pinia';
import { MessagePlugin } from 'tdesign-vue-next';
import { listTemplates, retryRun, runStats, type SopRun, type SopRunStats } from '@/api/sop';
import { RUN_STATUS_LABELS, successRate, triggerSourceLabel } from '@/api/sop/helpers';
import { useSopStore } from '@/stores/sop';

const router = useRouter();
const { t } = useI18n();
const store = useSopStore();
const { currentProjectId, templates, itemStats } = storeToRefs(store);

const search = ref(String(store.runFilter.q || ''));
const stats = ref<SopRunStats>({ success_rate: 0, finished_count: 0, total_count: 0, total_tokens: 0, status_counts: {} });
const refreshing = ref(false);
const retrying = ref('');
let stopPolling: (() => void) | null = null;
let searchTimer: ReturnType<typeof setTimeout> | null = null;

const filter = computed(() => store.runFilter);

const chipOrder = ['', 'running', 'queued', 'paused', 'waiting', 'succeeded', 'failed', 'cancelled'] as const;

const chips = computed(() =>
  chipOrder.map((value) => ({
    value,
    label: value ? runStatusLabel(value) : t('sop.runsList.all'),
    count: value ? stats.value.status_counts?.[value] || 0 : stats.value.total_count || 0,
    class: value === 'failed' ? 'is-failed' : '',
  })),
);

const activeCount = computed(
  () =>
    (stats.value.status_counts?.running || 0) +
    (stats.value.status_counts?.queued || 0) +
    (stats.value.status_counts?.waiting || 0) +
    (stats.value.status_counts?.paused || 0),
);

const tokenText = computed(() => {
  const total = stats.value.total_tokens || 0;
  if (total >= 1000) return `${(total / 1000).toFixed(1)}k`;
  return String(total);
});

const templateOptions = computed(() => [
  { value: '', label: t('sop.runsList.allSop') },
  ...(templates.value || []).map((tpl) => ({ value: tpl.id, label: tpl.name })),
]);

function runStatusLabel(status: string): string {
  return (RUN_STATUS_LABELS as Record<string, string>)[status] || status;
}

function shortId(id: string): string {
  return id ? id.slice(0, 8) : '—';
}

function itemLabel(itemId: string): string {
  if (!itemId) return '';
  // 运行表只存 item_id；列表页拿 items 做一次本地映射（同项目内）
  const item = store.items.find((entry) => entry.id === itemId);
  return item ? `${item.no || ''} ${item.title}`.trim() : '';
}

function templateLabel(templateId: string): string {
  return (templates.value || []).find((tpl) => tpl.id === templateId)?.name || templateId || '—';
}

function revisionLabel(run: SopRun): string {
  return run.workflow_snapshot ? '1' : '1';
}

function formatTime(value: string | null): string {
  if (!value) return '—';
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString();
}

function pickStatus(status: string) {
  store.runFilter.status = filter.value.status === status ? undefined : status || undefined;
  void store.loadRuns().catch(() => undefined);
}

function setTemplate(value: unknown) {
  store.runFilter.template_id = String(value ?? '') || undefined;
  void store.loadRuns().catch(() => undefined);
}

function onSearchInput() {
  if (searchTimer) clearTimeout(searchTimer);
  searchTimer = setTimeout(() => {
    store.runFilter.q = search.value.trim() || undefined;
    void store.loadRuns().catch(() => undefined);
  }, 300);
}

async function loadStats() {
  try {
    stats.value = await runStats(currentProjectId.value || undefined);
  } catch {
    stats.value = { success_rate: 0, finished_count: 0, total_count: 0, total_tokens: 0, status_counts: {} };
  }
}

async function refreshAll() {
  refreshing.value = true;
  try {
    await Promise.all([store.loadRuns(), loadStats(), store.loadTemplates()]);
  } finally {
    refreshing.value = false;
  }
}

async function retry(run: SopRun) {
  retrying.value = run.id;
  try {
    await retryRun(run.id);
    MessagePlugin.success(t('sop.runsList.retried'));
    await refreshAll();
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('sop.runsList.retryFailed'));
  } finally {
    retrying.value = '';
  }
}

function openRun(run: SopRun) {
  void router.push({ name: 'sopRunDetail', params: { id: run.id } });
}

watch(
  () => currentProjectId.value,
  async () => {
    search.value = '';
    store.runFilter = {};
    await refreshAll();
  },
);

onMounted(async () => {
  await refreshAll();
  // 5s 自动刷新只换行数据，搜索框在组件里、不会重挂载
  stopPolling = store.startPolling(() => {
    void store.loadRuns().catch(() => undefined);
    void loadStats();
  }, 5000);
});

onUnmounted(() => {
  stopPolling?.();
  if (searchTimer) clearTimeout(searchTimer);
});

void itemStats;
</script>

<style lang="less" scoped src="./sopRunList.less"></style>
