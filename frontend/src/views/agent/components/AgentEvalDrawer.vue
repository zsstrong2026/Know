<template>
  <SettingDrawer
    v-model:visible="drawerVisible"
    class="agent-eval-drawer"
    :title="t('agent.eval.title')"
    icon="chart"
    width="760px"
    :min-width="560"
    :max-width="1280"
    storage-key="setting-drawer:width:agent-eval"
    hide-footer
  >
    <div class="agent-eval">
      <div class="agent-eval-toolbar">
        <t-button theme="primary" :loading="triggering" @click="triggerRun">
          <template #icon><t-icon name="play-circle" /></template>
          {{ t('agent.eval.runNow') }}
        </t-button>
        <div v-if="latestRun" class="agent-eval-latest">
          <span class="agent-eval-latest-label">{{ t('agent.eval.latest') }}</span>
          <t-tag :theme="statusTheme(latestRun.status)" variant="light-outline" size="small">
            {{ statusLabel(latestRun.status) }}
          </t-tag>
          <span class="agent-eval-latest-meta">
            {{ t('agent.eval.passedRatio', { passed: latestRun.passed, total: latestRun.total }) }}
            <template v-if="latestRun.duration_ms > 0"> · {{ formatDuration(latestRun.duration_ms) }}</template>
          </span>
        </div>
      </div>

      <t-table
        :data="runs"
        :columns="historyColumns"
        row-key="run_id"
        size="small"
        :loading="loadingList"
        :hover="true"
        @row-click="onHistoryRowClick"
      >
        <template #timeCell="{ row }">
          {{ formatTime(row.created_at) }}
        </template>
        <template #statusCell="{ row }">
          <t-tag :theme="statusTheme(row.status)" variant="light-outline" size="small">
            {{ statusLabel(row.status) }}
          </t-tag>
        </template>
        <template #resultCell="{ row }">
          <span :class="['agent-eval-result', { 'agent-eval-result--all-passed': row.total > 0 && row.passed === row.total }]">
            {{ row.passed }}/{{ row.total }}
          </span>
        </template>
        <template #durationCell="{ row }">
          {{ formatDuration(row.duration_ms) }}
        </template>
        <template #opCell="{ row }">
          <t-button variant="text" size="small" theme="primary" @click.stop="selectRun(row)">
            {{ t('agent.eval.view') }}
          </t-button>
          <t-popconfirm :content="t('agent.eval.deleteConfirm')" @confirm="deleteRun(row)">
            <t-button variant="text" size="small" theme="danger" :loading="deletingRunId === row.run_id">
              {{ t('agent.eval.delete') }}
            </t-button>
          </t-popconfirm>
        </template>
        <template #empty>
          <div class="agent-eval-empty">{{ t('agent.eval.historyEmpty') }}</div>
        </template>
      </t-table>

      <div class="agent-eval-detail">
        <div class="agent-eval-detail-title">{{ t('agent.eval.detailTitle') }}</div>
        <template v-if="selectedRun">
          <div class="agent-eval-detail-head">
            <t-tag :theme="statusTheme(selectedRun.status)" variant="light-outline" size="small">
              {{ statusLabel(selectedRun.status) }}
            </t-tag>
            <span class="agent-eval-detail-meta">
              {{ t('agent.eval.passedRatio', { passed: selectedRun.passed, total: selectedRun.total }) }}
              <template v-if="selectedRun.duration_ms > 0"> · {{ formatDuration(selectedRun.duration_ms) }}</template>
              <template v-if="selectedRun.created_at"> · {{ formatTime(selectedRun.created_at) }}</template>
            </span>
          </div>
          <div v-if="selectedRun.status === 'running'" class="agent-eval-progress">
            <t-progress :percentage="progressPct" />
          </div>
          <div v-if="selectedRun.error" class="agent-eval-run-error">{{ selectedRun.error }}</div>
          <div v-if="loadingDetail" class="agent-eval-detail-loading">
            <t-loading size="small" />
          </div>
          <template v-else>
            <t-collapse v-model="expandedCaseIds" class="agent-eval-cases">
              <t-collapse-panel
                v-for="c in cases"
                :key="c.id"
                :value="c.id"
                :disabled="c.status === 'running'"
              >
                <template #header>
                  <div class="agent-eval-case-header">
                    <span class="agent-eval-case-title">{{ c.case_title }}</span>
                    <t-loading v-if="c.status === 'running'" size="small" />
                    <t-tag
                      v-else
                      :theme="c.status === 'passed' ? 'success' : 'danger'"
                      variant="light-outline"
                      size="small"
                    >
                      {{ c.status === 'passed' ? t('agent.eval.casePassed') : t('agent.eval.caseFailed') }}
                    </t-tag>
                  </div>
                </template>
                <div class="agent-eval-case-body">
                  <div class="agent-eval-case-query">
                    <span class="agent-eval-field-label">{{ t('agent.eval.query') }}</span>
                    <div class="agent-eval-case-query-text">{{ c.query }}</div>
                  </div>

                  <div class="agent-eval-case-section">
                    <div class="agent-eval-field-label">{{ t('agent.eval.assertions') }}</div>
                    <div class="agent-eval-assertions">
                      <div v-for="(a, idx) in c.assertions" :key="idx" class="agent-eval-assertion">
                        <t-icon
                          :name="a.passed ? 'check-circle' : 'close-circle'"
                          :class="['agent-eval-assertion-icon', a.passed ? 'is-passed' : 'is-failed']"
                        />
                        <span class="agent-eval-assertion-name">{{ a.name }}</span>
                        <span class="agent-eval-assertion-compare">{{ a.expected }} → {{ a.actual }}</span>
                      </div>
                    </div>
                  </div>

                  <div v-if="c.judge && c.judge.length" class="agent-eval-case-section">
                    <div class="agent-eval-field-label">{{ t('agent.eval.judge') }}</div>
                    <div class="agent-eval-judge">
                      <div v-for="(j, idx) in c.judge" :key="idx" class="agent-eval-judge-item">
                        <span class="agent-eval-judge-metric">{{ judgeMetricLabel(j.metric) }}</span>
                        <span v-if="j.score != null" class="agent-eval-judge-score">{{ j.score }}</span>
                        <span v-if="j.reason" class="agent-eval-judge-reason">{{ j.reason }}</span>
                        <span v-if="j.error" class="agent-eval-judge-error">{{ j.error }}</span>
                      </div>
                    </div>
                  </div>

                  <div v-if="c.tool_calls && c.tool_calls.length" class="agent-eval-case-section">
                    <div class="agent-eval-field-label">{{ t('agent.eval.toolCalls') }}</div>
                    <div class="agent-eval-tools">
                      <t-tag v-for="(tc, idx) in c.tool_calls" :key="idx" size="small" variant="outline">
                        {{ tc.tool_name }}
                      </t-tag>
                    </div>
                  </div>

                  <div class="agent-eval-case-section">
                    <div class="agent-eval-field-label">
                      {{ t('agent.eval.answer') }} · {{ t('agent.eval.references', { count: c.reference_count }) }}
                    </div>
                    <div class="agent-eval-answer">{{ c.answer || t('agent.eval.noAnswer') }}</div>
                  </div>

                  <div v-if="c.error" class="agent-eval-case-error">{{ c.error }}</div>
                </div>
              </t-collapse-panel>
            </t-collapse>
            <div v-if="cases.length === 0" class="agent-eval-empty">{{ t('agent.eval.casesEmpty') }}</div>
          </template>
        </template>
        <div v-else class="agent-eval-detail-placeholder">{{ t('agent.eval.detailEmpty') }}</div>
      </div>
    </div>
  </SettingDrawer>
</template>

<script setup lang="ts">
import { ref, computed, watch, onUnmounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import SettingDrawer from '@/components/settings/SettingDrawer.vue'
import {
  runAgentEval,
  listAgentEvals,
  getAgentEvalRun,
  deleteAgentEvalRun,
  type AgentEvalRun,
  type AgentEvalCaseResult,
} from '@/api/agent'

const props = defineProps<{
  visible: boolean
  agentId: string
}>()

const emit = defineEmits<{
  (e: 'update:visible', visible: boolean): void
}>()

const { t, te } = useI18n()

const drawerVisible = computed({
  get: () => props.visible,
  set: (val) => emit('update:visible', val),
})

const runs = ref<AgentEvalRun[]>([])
const loadingList = ref(false)
const triggering = ref(false)
const deletingRunId = ref('')

const selectedRun = ref<AgentEvalRun | null>(null)
const cases = ref<AgentEvalCaseResult[]>([])
const loadingDetail = ref(false)
const expandedCaseIds = ref<string[]>([])

const latestRun = computed(() => runs.value[0] ?? null)

const progressPct = computed(() => {
  if (!selectedRun.value || selectedRun.value.total <= 0) return 0
  const done = cases.value.filter((c) => c.status !== 'running').length
  return Math.min(100, Math.round((done / selectedRun.value.total) * 100))
})

const historyColumns = computed(() => [
  { colKey: 'created_at', title: t('agent.eval.columns.time'), cell: 'timeCell' },
  { colKey: 'status', title: t('agent.eval.columns.status'), cell: 'statusCell', width: 92 },
  { colKey: 'result', title: t('agent.eval.columns.result'), cell: 'resultCell', width: 88 },
  { colKey: 'duration_ms', title: t('agent.eval.columns.duration'), cell: 'durationCell', width: 92 },
  { colKey: 'op', title: t('agent.eval.columns.actions'), cell: 'opCell', width: 150 },
])

function onHistoryRowClick(context: { row: AgentEvalRun }) {
  void selectRun(context.row)
}

function statusLabel(status: string): string {
  const key = `agent.eval.status.${status}`
  return te(key) ? t(key) : status
}

function statusTheme(status: string): 'success' | 'warning' | 'danger' {
  if (status === 'completed') return 'success'
  if (status === 'failed') return 'danger'
  return 'warning'
}

function judgeMetricLabel(metric: string): string {
  const key = `agent.eval.judgeMetrics.${metric}`
  return te(key) ? t(key) : metric
}

function unwrap(res: any): any {
  return res?.data !== undefined ? res.data : res
}

function formatDuration(ms?: number): string {
  if (!ms || ms <= 0) return '-'
  if (ms < 1000) return `${ms}ms`
  const s = ms / 1000
  if (s < 60) return `${s.toFixed(1)}s`
  const m = Math.floor(s / 60)
  return `${m}m${Math.round(s - m * 60)}s`
}

function formatTime(iso?: string): string {
  if (!iso) return '-'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString(undefined, {
    month: 'numeric',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  })
}

async function loadList() {
  loadingList.value = true
  try {
    const res = await listAgentEvals(props.agentId)
    const data = unwrap(res)
    runs.value = Array.isArray(data) ? data : []
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('agent.eval.loadFailed'))
  } finally {
    loadingList.value = false
  }
}

async function selectRun(run: AgentEvalRun) {
  stopPolling()
  selectedRun.value = run
  cases.value = []
  expandedCaseIds.value = []
  loadingDetail.value = true
  try {
    const res = await getAgentEvalRun(props.agentId, run.run_id)
    const data = unwrap(res)
    if (selectedRun.value?.run_id !== run.run_id) return
    const nextRun: AgentEvalRun = data?.run || run
    selectedRun.value = nextRun
    cases.value = data?.cases || []
    if (nextRun.status === 'running') startPolling()
  } catch (e: any) {
    if (e?.status === 404) {
      MessagePlugin.warning(t('agent.eval.runMissing'))
      selectedRun.value = null
      void loadList()
    } else {
      MessagePlugin.error(e?.message || t('agent.eval.loadFailed'))
    }
  } finally {
    loadingDetail.value = false
  }
}

async function triggerRun() {
  if (triggering.value) return
  triggering.value = true
  try {
    const res = await runAgentEval(props.agentId)
    const data = unwrap(res)
    MessagePlugin.success(t('agent.eval.triggerSuccess'))
    await loadList()
    if (data) await selectRun(data)
  } catch (e: any) {
    if (e?.status === 409) {
      MessagePlugin.warning(t('agent.eval.conflictRunning'))
      await loadList()
      const running = e?.run_id
        ? runs.value.find((r) => r.run_id === e.run_id) || null
        : runs.value.find((r) => r.status === 'running') || null
      if (running) await selectRun(running)
    } else {
      MessagePlugin.error(e?.message || t('agent.eval.triggerFailed'))
    }
  } finally {
    triggering.value = false
  }
}

async function deleteRun(run: AgentEvalRun) {
  deletingRunId.value = run.run_id
  try {
    await deleteAgentEvalRun(props.agentId, run.run_id)
    if (selectedRun.value?.run_id === run.run_id) {
      stopPolling()
      selectedRun.value = null
      cases.value = []
      expandedCaseIds.value = []
    }
    MessagePlugin.success(t('agent.eval.deleteSuccess'))
    await loadList()
  } catch (e: any) {
    MessagePlugin.error(e?.message || t('agent.eval.deleteFailed'))
  } finally {
    deletingRunId.value = ''
  }
}

let pollTimer: ReturnType<typeof setInterval> | null = null

function startPolling() {
  stopPolling()
  pollTimer = setInterval(() => {
    void refreshSelectedRun()
  }, 2000)
}

function stopPolling() {
  if (pollTimer !== null) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

async function refreshSelectedRun() {
  const current = selectedRun.value
  if (!current || current.status !== 'running') {
    stopPolling()
    return
  }
  try {
    const res = await getAgentEvalRun(props.agentId, current.run_id)
    const data = unwrap(res)
    if (selectedRun.value?.run_id !== current.run_id) return
    const nextRun: AgentEvalRun = data?.run || current
    selectedRun.value = nextRun
    cases.value = data?.cases || []
    if (nextRun.status !== 'running') {
      stopPolling()
      void loadList()
    }
  } catch (e: any) {
    if (e?.status === 404) {
      stopPolling()
      MessagePlugin.warning(t('agent.eval.runMissing'))
      selectedRun.value = null
      void loadList()
    }
  }
}

watch(
  () => props.visible,
  (visible) => {
    if (visible && props.agentId) {
      stopPolling()
      selectedRun.value = null
      cases.value = []
      expandedCaseIds.value = []
      void loadList()
    } else if (!visible) {
      stopPolling()
    }
  },
)

onUnmounted(stopPolling)

defineExpose({ triggerRun })
</script>

<style scoped>
.agent-eval {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.agent-eval-toolbar {
  display: flex;
  align-items: center;
  gap: 14px;
  flex-wrap: wrap;
}

.agent-eval-latest {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  font-size: 12px;
  color: var(--td-text-color-secondary);
}

.agent-eval-latest-label {
  color: var(--td-text-color-placeholder);
}

.agent-eval-latest-meta {
  white-space: nowrap;
}

.agent-eval-result--all-passed {
  color: var(--td-success-color);
  font-weight: 600;
}

.agent-eval-empty {
  padding: 20px 0;
  text-align: center;
  font-size: 12px;
  color: var(--td-text-color-placeholder);
}

.agent-eval-detail {
  border-top: 1px solid var(--td-component-stroke);
  padding-top: 14px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.agent-eval-detail-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  display: flex;
  align-items: center;
  gap: 8px;
}

.agent-eval-detail-title::before {
  content: '';
  width: 3px;
  height: 14px;
  background: var(--td-brand-color);
  border-radius: 2px;
}

.agent-eval-detail-head {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.agent-eval-detail-meta {
  font-size: 12px;
  color: var(--td-text-color-secondary);
}

.agent-eval-progress {
  padding: 2px 0;
}

.agent-eval-run-error {
  padding: 8px 12px;
  border-radius: 6px;
  background: var(--td-error-color-1, rgba(213, 73, 65, 0.08));
  color: var(--td-error-color);
  font-size: 12px;
  line-height: 1.6;
  word-break: break-word;
}

.agent-eval-detail-loading {
  display: flex;
  justify-content: center;
  padding: 18px 0;
}

.agent-eval-detail-placeholder {
  padding: 28px 0;
  text-align: center;
  font-size: 13px;
  color: var(--td-text-color-placeholder);
}

.agent-eval-case-header {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.agent-eval-case-title {
  font-size: 13px;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.agent-eval-case-body {
  display: flex;
  flex-direction: column;
  gap: 14px;
  padding: 4px 2px 8px;
}

.agent-eval-case-query-text {
  margin-top: 6px;
  padding: 8px 12px;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 6px;
  font-size: 12px;
  line-height: 1.7;
  color: var(--td-text-color-primary);
  white-space: pre-wrap;
  word-break: break-word;
}

.agent-eval-case-section {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.agent-eval-field-label {
  font-size: 12px;
  font-weight: 500;
  color: var(--td-text-color-placeholder);
}

.agent-eval-assertions {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.agent-eval-assertion {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  line-height: 1.6;
}

.agent-eval-assertion-icon.is-passed {
  color: var(--td-success-color);
}

.agent-eval-assertion-icon.is-failed {
  color: var(--td-error-color);
}

.agent-eval-assertion-name {
  font-family: var(--td-font-family-mono, monospace);
  color: var(--td-text-color-primary);
  flex-shrink: 0;
}

.agent-eval-assertion-compare {
  color: var(--td-text-color-secondary);
  word-break: break-word;
}

.agent-eval-judge {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.agent-eval-judge-item {
  display: flex;
  align-items: baseline;
  gap: 8px;
  font-size: 12px;
  line-height: 1.6;
  flex-wrap: wrap;
}

.agent-eval-judge-metric {
  font-weight: 500;
  color: var(--td-text-color-primary);
  flex-shrink: 0;
}

.agent-eval-judge-score {
  color: var(--td-brand-color);
  font-weight: 600;
}

.agent-eval-judge-reason {
  color: var(--td-text-color-secondary);
  word-break: break-word;
}

.agent-eval-judge-error {
  color: var(--td-text-color-placeholder);
  font-style: italic;
}

.agent-eval-tools {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: wrap;
}

.agent-eval-answer {
  padding: 8px 12px;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 6px;
  font-size: 12px;
  line-height: 1.7;
  color: var(--td-text-color-primary);
  white-space: pre-wrap;
  word-break: break-word;
}

.agent-eval-case-error {
  color: var(--td-error-color);
  font-size: 12px;
  line-height: 1.6;
  word-break: break-word;
}
</style>

<style lang="less">
.agent-eval-drawer {
  .t-drawer__body {
    overflow-y: auto;
  }
}
</style>
