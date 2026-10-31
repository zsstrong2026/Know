<template>
  <!-- 批量归档对话框（§9.8.1）：预检分组 → 逐条执行 → 结果汇总 -->
  <t-dialog
    v-model:visible="visible"
    :header="headerText"
    width="560px"
    :close-on-overlay-click="!running"
    :footer="false"
    @close="onCancel"
  >
    <!-- 预检阶段：只读列表里已批量填充的状态，不发请求 -->
    <div v-if="!running && results.length === 0" class="ba-block">
      <div class="ba-counts">{{ countsText }}</div>
      <div class="ba-target">
        <t-icon name="lock-on" size="12px" />
        <span>{{ t('workbench.batchArchive.target') }}</span>
      </div>
      <p class="ba-note">{{ t('workbench.batchArchive.note') }}</p>
      <p v-if="(dropped ?? 0) > 0" class="ba-warn">
        {{ t('workbench.batchArchive.dropped', { count: dropped }) }}
      </p>
      <p v-if="actionable.length === 0" class="ba-warn">
        {{ t('workbench.batchArchive.nothingToDo') }}
      </p>
    </div>

    <!-- 执行中 / 结果：逐项显示标题、状态与原因 -->
    <div v-else class="ba-block">
      <div class="ba-progress">
        {{ t('workbench.batchArchive.progress', { done: doneCount, total: totalCount }) }}
      </div>
      <ul class="ba-list">
        <li v-for="row in results" :key="row.key" :class="`is-${row.status}`">
          <span class="row-title" :title="row.title">{{ row.title }}</span>
          <span class="row-status">{{ statusText(row) }}</span>
        </li>
      </ul>
      <div v-if="summary" class="ba-summary">
        {{ t('workbench.batchArchive.summary', {
          archived: summary.archived,
          failed: summary.failed,
          skipped: summary.skipped,
        }) }}
      </div>
      <p class="ba-note">{{ t('workbench.batchArchive.noRollback') }}</p>
    </div>

    <!-- 页脚由本组件完全接管（footer=false）：主操作必须始终可见 -->
    <div class="ba-footer">
      <t-button
        v-if="summary && summary.failed > 0 && !running"
        variant="outline"
        @click="retryFailed"
      >
        {{ t('workbench.batchArchive.retryFailed') }}
      </t-button>
      <span class="spacer" />
      <t-button variant="outline" :disabled="running" @click="onCancel">
        {{ t('workbench.batchArchive.close') }}
      </t-button>
      <t-button
        v-if="!finished"
        theme="primary"
        :loading="running"
        :disabled="actionable.length === 0"
        @click="onConfirm"
      >
        {{ confirmLabel }}
      </t-button>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
/**
 * 批量归档对话框。
 *
 * 状态机只有三段：预检（不含结果）→ 执行中 → 结果（可重试失败项）。
 * 所有分组与计数都来自 batch-archive.ts 的纯函数，组件只负责展示与触发。
 */
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  countBatchArchive,
  planBatchArchive,
  summarizeBatchResults,
  type BatchArchiveCandidate,
  type BatchArchiveResult,
  type BatchArchiveSummary,
} from '@/workbench/batch-archive'
import { runBatchArchive } from '@/workbench/use-batch-archive'

const props = defineProps<{
  modelValue: boolean
  /** 选中的条目快照；打开时读取一次，执行中不再变 */
  selected: BatchArchiveCandidate[]
  /** 超过上限被丢掉的条数 */
  dropped?: number
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  /** 全部结束（含部分失败）后触发，供父组件刷新列表与 KB 计数 */
  finished: []
}>()

const { t } = useI18n()

const visible = computed({
  get: () => props.modelValue,
  set: (value: boolean) => emit('update:modelValue', value),
})

const running = ref(false)
const results = ref<BatchArchiveResult[]>([])
const doneCount = ref(0)
/** 只统计真正要发请求的条数，跳过项不计入进度。 */
const totalCount = ref(0)
const retryTargets = ref<BatchArchiveCandidate[]>([])

/** 预检在组件内固定一次：执行中不再重算，避免状态漂移导致计数跳变。 */
const precheck = ref(planBatchArchive([]))
const actionable = computed(() => precheck.value.actionable)

const countsText = computed(() => {
  const counts = countBatchArchive(precheck.value)
  return t('workbench.batchArchive.counts', {
    create: counts.create,
    update: counts.update,
    retry: counts.retry,
    recreate: counts.recreate,
    skip: counts.skip,
  })
})

const headerText = computed(() =>
  t('workbench.batchArchive.title', { count: precheck.value.actionable.length }),
)

const confirmLabel = computed(() =>
  running.value ? t('workbench.batchArchive.running') : t('workbench.batchArchive.start'),
)

/** 已出过结果就不再显示主操作，避免重复归档；重试用单独按钮。 */
const finished = computed(() => !running.value && results.value.length > 0)

const summary = computed<BatchArchiveSummary | null>(() =>
  results.value.length > 0 ? summarizeBatchResults(results.value) : null,
)

/** 打开时重置并做一次预检。 */
watch(
  () => props.modelValue,
  (open) => {
    if (!open) return
    results.value = []
    doneCount.value = 0
    retryTargets.value = []
    precheck.value = planBatchArchive(props.selected)
    totalCount.value = precheck.value.actionable.length
  },
  { immediate: true },
)

/**
 * 行状态文案。
 *
 * 失败行同时给出状态与原因（「失败 · 草稿尚未暂存」）：只写原因会让用户
 * 分不清是“没做”还是“做了但失败”，只写「失败」又丢了可操作信息。
 */
function statusText(row: BatchArchiveResult): string {
  if (row.status === 'archived') return t('workbench.batchArchive.status.archived')
  if (row.status === 'skipped') {
    return `${t('workbench.batchArchive.status.skipped')} · ${t('workbench.batchArchive.reason.up_to_date')}`
  }
  const reason = row.reason ?? 'network'
  return `${t('workbench.batchArchive.status.failed')} · ${t(`workbench.batchArchive.reason.${reason}`)}`
}

async function execute(plan: ReturnType<typeof planBatchArchive>) {
  running.value = true
  doneCount.value = 0
  totalCount.value = plan.actionable.length
  results.value = []
  try {
    const { results: all } = await runBatchArchive(plan, {
      onProgress: (done, total) => {
        doneCount.value = done
        totalCount.value = total
      },
    })
    results.value = all
  } finally {
    running.value = false
    emit('finished')
  }
}

async function onConfirm() {
  if (running.value) return
  if (precheck.value.actionable.length === 0) {
    visible.value = false
    return
  }
  await execute(precheck.value)
}

/** 只重试失败项：重新走同一编排，不重跑已成功的条目。 */
async function retryFailed() {
  const failed = results.value.filter((row) => row.status === 'failed')
  if (failed.length === 0) return
  const keys = new Set(failed.map((row) => row.key))
  retryTargets.value = props.selected.filter((item) => keys.has(item.key))
  const retryPlan = planBatchArchive(
    retryTargets.value.map((item) => ({
      ...item,
      // 重试前状态可能已经变了；统一按“需要重写”处理，由服务端判定最终结果。
      status: item.status === 'none' ? 'none' : ('partial' as const),
    })),
  )
  await execute(retryPlan)
}

/** 关闭：已发出的请求不取消（服务端已受理），只是不再等结果。 */
function onCancel() {
  visible.value = false
}
</script>

<style lang="less" scoped>
.ba-block {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-2);
}

.ba-counts {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-primary);
}

.ba-target {
  display: flex;
  align-items: center;
  gap: var(--app-space-1);
  font-size: var(--app-text-xs);
  color: var(--td-text-color-secondary);
}

.ba-note {
  margin: 0;
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}

.ba-warn {
  margin: 0;
  font-size: var(--app-text-xs);
  color: var(--td-warning-color);
}

.ba-progress {
  font-size: var(--app-text-xs);
  color: var(--td-text-color-secondary);
}

.ba-list {
  max-height: 260px;
  margin: 0;
  padding: 0;
  overflow-y: auto;
  list-style: none;

  li {
    display: flex;
    align-items: center;
    gap: var(--app-space-2);
    padding: var(--app-space-1) 0;
    font-size: var(--app-text-xs);
    border-bottom: 1px solid var(--td-component-stroke);
  }
}

.row-title {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--td-text-color-primary);
}

.row-status {
  flex: none;
  color: var(--td-text-color-placeholder);
}

.is-archived .row-status {
  color: var(--td-success-color);
}

.is-failed .row-status {
  color: var(--td-error-color);
}

.ba-summary {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-primary);
}

.ba-footer {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  margin-top: var(--app-space-4);
  padding-top: var(--app-space-3);
  border-top: 1px solid var(--td-component-stroke);
}

.spacer {
  flex: 1;
}
</style>
