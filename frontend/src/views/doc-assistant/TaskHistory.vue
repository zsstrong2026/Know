<template>
  <div class="doc-assistant-page">
    <div class="page-header">
      <div class="page-header-title">
        <h2>{{ t('docAssistant.history.title') }}</h2>
        <p class="page-header-subtitle">{{ t('docAssistant.history.subtitle') }}</p>
      </div>
      <t-button theme="primary" @click="router.push('/platform/doc-assistant/generate')">
        <template #icon><t-icon name="add" /></template>
        {{ t('docAssistant.history.newGeneration') }}
      </t-button>
    </div>

    <div class="page-body">
      <t-skeleton v-if="loading && tasks.length === 0" animation="gradient" :row-col="[{ width: '100%', height: '110px' }, { width: '100%', height: '110px' }]" />
      <t-empty v-else-if="tasks.length === 0" :description="t('docAssistant.history.empty')" />
      <div v-else class="task-list">
        <div v-for="task in tasks" :key="task.id" class="task-card">
          <div class="task-card-head">
            <div class="task-card-title-wrap">
              <span class="task-card-theme" :title="task.theme">{{ task.theme }}</span>
              <span v-if="templateName(task)" class="task-card-template">
                <t-icon name="component" size="13px" />
                {{ templateName(task) }}
              </span>
            </div>
            <t-tag :theme="statusTagTheme(task.status)" variant="light">{{ statusLabel(task.status) }}</t-tag>
          </div>

          <div v-if="task.progress?.chapters?.length" class="task-card-progress">
            <div class="progress-chapters">
              <span
                v-for="ch in task.progress.chapters"
                :key="ch.no"
                class="progress-chapter"
                :class="`progress-chapter--${ch.status}`"
                :title="`${ch.no}. ${ch.title}（${chapterStatusLabel(ch.status)}）`"
              />
            </div>
            <span class="progress-count">{{ doneCount(task) }}/{{ task.progress.chapters.length }}</span>
          </div>

          <div v-if="task.error" class="task-card-error" :title="task.error">{{ task.error }}</div>

          <div class="task-card-foot">
            <span class="task-card-time">{{ formatTime(task.created_at) }}</span>
            <div class="task-card-actions">
              <t-button size="small" variant="text" theme="primary" :disabled="!hasDocuments(task)" :loading="viewingId === task.id" @click="handleView(task)">
                <template #icon><t-icon name="browse" /></template>
                {{ t('docAssistant.history.viewDocument') }}
              </t-button>
              <t-button size="small" variant="text" :disabled="!canRegenerate(task)" :loading="regeneratingId === task.id" @click="handleRegenerate(task)">
                <template #icon><t-icon name="refresh" /></template>
                {{ t('docAssistant.history.regenerate') }}
              </t-button>
              <t-button size="small" variant="text" theme="danger" @click="handleDelete(task)">
                <template #icon><t-icon name="delete" /></template>
                {{ t('common.delete') }}
              </t-button>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { MessagePlugin, DialogPlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import {
  listDocTasks,
  deleteDocTask,
  listDocDocuments,
  createDocGenerationTask,
  type DocGenerationTask,
  type DocTaskStatus,
  type DocChapterStatus,
  type DocTemplate,
} from '@/api/doc-assistant'
import { listDocTemplates } from '@/api/doc-assistant'

const { t } = useI18n()
const router = useRouter()

const tasks = ref<DocGenerationTask[]>([])
const loading = ref(false)
const viewingId = ref('')
const regeneratingId = ref('')
const templateMap = ref<Record<string, DocTemplate>>({})
let pollTimer: ReturnType<typeof setInterval> | null = null

const ACTIVE_STATUSES: DocTaskStatus[] = ['pending', 'running']

const runningCount = computed(() =>
  tasks.value.filter((task) => ACTIVE_STATUSES.includes(task.status)).length,
)

const formatTime = (value?: string): string => {
  if (!value) return ''
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return value
  return d.toLocaleString()
}

const statusLabel = (status: DocTaskStatus): string => t(`docAssistant.status.${status}`)

const statusTagTheme = (status: DocTaskStatus): 'primary' | 'success' | 'warning' | 'danger' | 'default' => {
  switch (status) {
    case 'success':
      return 'success'
    case 'failed':
      return 'danger'
    case 'partial_success':
      return 'warning'
    case 'pending':
    case 'running':
      return 'primary'
    default:
      return 'default'
  }
}

const chapterStatusLabel = (status: DocChapterStatus): string => t(`docAssistant.chapterStatus.${status}`)

const templateName = (task: DocGenerationTask): string => {
  const snapshot = task.template_snapshot as DocTemplate | null | undefined
  if (snapshot?.name) return snapshot.name
  return templateMap.value[task.template_id]?.name || ''
}

const doneCount = (task: DocGenerationTask): number =>
  (task.progress?.chapters || []).filter((ch) => ch.status === 'done' || ch.status === 'failed' || ch.status === 'skipped').length

const hasDocuments = (task: DocGenerationTask): boolean =>
  task.status === 'success' || task.status === 'partial_success'

const canRegenerate = (task: DocGenerationTask): boolean =>
  !ACTIVE_STATUSES.includes(task.status) && (task.status === 'failed' || task.status === 'cancelled' || (task.kb_ids?.length ?? 0) > 0)

const loadTemplates = async () => {
  try {
    const res = await listDocTemplates()
    const map: Record<string, DocTemplate> = {}
    for (const tpl of res.data || []) map[tpl.id] = tpl
    templateMap.value = map
  } catch {
    // 模板映射仅用于展示模板名，失败时保留快照名
  }
}

const loadTasks = async () => {
  try {
    const res = await listDocTasks()
    tasks.value = res.data || []
  } catch (error: any) {
    if (loading.value) MessagePlugin.error(error?.message || t('docAssistant.history.loadFailed'))
  } finally {
    loading.value = false
  }
}

const stopPolling = () => {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

const startPolling = () => {
  stopPolling()
  pollTimer = setInterval(async () => {
    try {
      const res = await listDocTasks()
      tasks.value = res.data || []
      if (runningCount.value === 0) stopPolling()
    } catch {
      // 单次轮询失败不中断，下次继续
    }
  }, 4000)
}

const handleView = async (task: DocGenerationTask) => {
  viewingId.value = task.id
  try {
    const res = await listDocDocuments(task.id)
    const docs = res.data || []
    if (docs.length === 0) {
      MessagePlugin.warning(t('docAssistant.history.noDocuments'))
      return
    }
    // 版本倒序，docs[0] 为最新版本
    router.push(`/platform/doc-assistant/documents/${docs[0].id}`)
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('docAssistant.history.loadDocumentsFailed'))
  } finally {
    viewingId.value = ''
  }
}

const handleRegenerate = async (task: DocGenerationTask) => {
  const kbIds = task.kb_ids || []
  if (kbIds.length === 0) {
    MessagePlugin.warning(t('docAssistant.history.regenerateNoKb'))
    return
  }
  regeneratingId.value = task.id
  try {
    await createDocGenerationTask({
      template_id: task.template_id,
      kb_ids: kbIds,
      model_id: task.model_id || undefined,
      theme: task.theme,
      extra_requirement: task.extra_requirement || undefined,
    })
    MessagePlugin.success(t('docAssistant.history.regenerateSuccess'))
    await loadTasks()
    startPolling()
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('docAssistant.history.regenerateFailed'))
  } finally {
    regeneratingId.value = ''
  }
}

const handleDelete = (task: DocGenerationTask) => {
  const confirmDialog = DialogPlugin.confirm({
    header: t('docAssistant.history.deleteConfirmTitle'),
    body: t('docAssistant.history.deleteConfirmBody', { theme: task.theme }),
    confirmBtn: { content: t('common.delete'), theme: 'danger' as const },
    cancelBtn: t('common.cancel'),
    theme: 'warning',
    onConfirm: async () => {
      try {
        await deleteDocTask(task.id)
        MessagePlugin.success(t('docAssistant.history.deleteSuccess'))
        tasks.value = tasks.value.filter((item) => item.id !== task.id)
      } catch (error: any) {
        MessagePlugin.error(error?.message || t('docAssistant.history.deleteFailed'))
      }
      confirmDialog.destroy()
    },
  })
}

onMounted(async () => {
  loading.value = true
  await loadTemplates()
  await loadTasks()
  if (runningCount.value > 0) startPolling()
  document.addEventListener('visibilitychange', handleVisibility)
})

onUnmounted(() => {
  stopPolling()
  document.removeEventListener('visibilitychange', handleVisibility)
})

// 页面重新可见时恢复轮询（例如从文档详情页返回）
const handleVisibility = () => {
  if (document.visibilityState === 'visible' && runningCount.value > 0) {
    void getDocTaskStatusRefresh()
  }
}

const getDocTaskStatusRefresh = async () => {
  try {
    const res = await listDocTasks()
    tasks.value = res.data || []
    if (runningCount.value > 0) startPolling()
  } catch {
    // 忽略
  }
}
</script>

<style lang="less" scoped>
.doc-assistant-page {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  overflow: hidden;
}

.page-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  padding: 24px 24px 16px;
  flex-shrink: 0;

  &-title h2 {
    margin: 0;
    font-size: 20px;
    font-weight: 600;
    color: var(--td-text-color-primary);
  }

  &-subtitle {
    margin: 4px 0 0;
    font-size: 13px;
    color: var(--td-text-color-secondary);
  }
}

.page-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 0 24px 24px;
}

.task-list {
  display: flex;
  flex-direction: column;
  gap: 12px;
  max-width: 960px;
}

.task-card {
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  padding: 14px 16px;
  display: flex;
  flex-direction: column;
  gap: 10px;
  transition: box-shadow 0.2s ease;

  &:hover {
    box-shadow: var(--td-shadow-2);
  }

  &-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
  }

  &-title-wrap {
    display: flex;
    align-items: center;
    gap: 10px;
    min-width: 0;
  }

  &-theme {
    font-size: 15px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  &-template {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    font-size: 12px;
    color: var(--td-text-color-secondary);
    background: var(--td-bg-color-secondarycontainer);
    border-radius: 4px;
    padding: 2px 8px;
    flex-shrink: 0;
    max-width: 200px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  &-progress {
    display: flex;
    align-items: center;
    gap: 12px;
  }

  &-error {
    font-size: 12px;
    color: var(--td-error-color);
    background: var(--td-error-color-light);
    border-radius: 4px;
    padding: 6px 10px;
    line-height: 1.5;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  &-foot {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
  }

  &-time {
    font-size: 12px;
    color: var(--td-text-color-placeholder);
  }

  &-actions {
    display: flex;
    align-items: center;
  }
}

.progress-chapters {
  flex: 1;
  display: flex;
  gap: 4px;
  flex-wrap: wrap;
}

.progress-chapter {
  width: 10px;
  height: 10px;
  border-radius: 3px;
  background: var(--td-bg-color-secondarycontainer);
  border: 1px solid var(--td-component-border);

  &--done {
    background: var(--td-success-color);
    border-color: var(--td-success-color);
  }

  &--failed {
    background: var(--td-error-color);
    border-color: var(--td-error-color);
  }

  &--skipped {
    background: var(--td-warning-color);
    border-color: var(--td-warning-color);
  }

  &--retrieving,
  &--generating,
  &--running {
    background: var(--td-brand-color-light);
    border-color: var(--td-brand-color);
    animation: progress-pulse 1.2s ease-in-out infinite;
  }
}

@keyframes progress-pulse {
  0%, 100% {
    opacity: 1;
  }
  50% {
    opacity: 0.4;
  }
}

.progress-count {
  font-size: 12px;
  color: var(--td-text-color-secondary);
  flex-shrink: 0;
}
</style>
