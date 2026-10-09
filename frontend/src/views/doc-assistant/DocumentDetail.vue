<template>
  <div class="doc-assistant-detail">
    <t-skeleton v-if="loading" animation="gradient" :row-col="[{ width: '100%', height: '40px' }, { width: '100%', height: '400px' }]" />

    <template v-else-if="doc">
      <!-- 顶部操作栏 -->
      <div class="detail-header">
        <div class="detail-header-left">
          <t-button variant="text" theme="default" class="back-btn" @click="goBack">
            <template #icon><t-icon name="arrow-left" /></template>
            {{ t('docAssistant.detail.back') }}
          </t-button>
          <div class="detail-title-wrap">
            <h2 class="detail-title">{{ t('docAssistant.detail.title') }}</h2>
            <t-tag v-if="task" variant="light" class="detail-theme" :title="task.theme">{{ task.theme }}</t-tag>
            <t-tag :theme="doc.status === 'edited' ? 'warning' : 'success'" variant="light">
              {{ t(`docAssistant.detail.docStatus.${doc.status}`) }}
            </t-tag>
          </div>
        </div>
        <div class="detail-header-actions">
          <t-select
            v-if="versions.length > 1"
            :value="doc.id"
            class="version-select"
            :placeholder="t('docAssistant.detail.versionPlaceholder')"
            @change="switchVersion"
          >
            <t-option v-for="v in versions" :key="v.id" :value="v.id" :label="`v${v.version}`" />
          </t-select>
          <t-button variant="outline" :loading="exporting === 'markdown'" @click="handleExport('markdown')">
            <template #icon><t-icon name="file-md" /></template>
            {{ t('docAssistant.detail.exportMarkdown') }}
          </t-button>
          <t-button variant="outline" :loading="exporting === 'word'" @click="handleExport('word')">
            <template #icon><t-icon name="file-word" /></template>
            {{ t('docAssistant.detail.exportWord') }}
          </t-button>
        </div>
      </div>

      <!-- 章节列表 + 正文 -->
      <div class="detail-body">
        <div class="chapter-nav">
          <div class="chapter-nav-title">{{ t('docAssistant.detail.chapters') }}</div>
          <div
            v-for="ch in doc.chapters"
            :key="ch.no"
            class="chapter-nav-item"
            :class="{ 'chapter-nav-item--active': activeChapterNo === ch.no }"
            @click="activeChapterNo = ch.no"
          >
            <span class="chapter-nav-no">{{ ch.no }}</span>
            <span class="chapter-nav-title-text" :title="ch.title">{{ ch.title }}</span>
            <span v-if="chapterStatusDot(ch.status)" class="chapter-nav-dot" :class="`chapter-nav-dot--${ch.status}`" :title="chapterStatusLabel(ch.status)" />
          </div>
        </div>

        <div class="chapter-content">
          <template v-if="activeChapter">
            <div class="chapter-content-head">
              <div class="chapter-content-title-wrap">
                <h3 class="chapter-content-title">{{ activeChapter.no }}. {{ activeChapter.title }}</h3>
                <span class="chapter-content-words">{{ wordCount(activeChapter.content) }} {{ t('docAssistant.detail.words') }}</span>
              </div>
              <div class="chapter-content-actions">
                <t-button size="small" variant="text" theme="primary" @click="openEditDialog">
                  <template #icon><t-icon name="edit-1" /></template>
                  {{ t('docAssistant.detail.editChapter') }}
                </t-button>
                <t-button size="small" variant="text" @click="handleRegenerate">
                  <template #icon><t-icon name="refresh" /></template>
                  {{ t('docAssistant.detail.regenerateChapter') }}
                </t-button>
              </div>
            </div>

            <div class="chapter-content-body">
              <MarkdownContent :content="activeChapter.content || ''" />
            </div>

            <div v-if="activeChapter.references?.length" class="chapter-references">
              <div class="chapter-references-title">
                <t-icon name="link" size="15px" />
                {{ t('docAssistant.detail.references') }}
              </div>
              <div v-for="(ref, idx) in activeChapter.references" :key="idx" class="chapter-reference-item">
                <span class="chapter-reference-title" :title="ref.title">{{ ref.title }}</span>
                <span v-if="ref.filename" class="chapter-reference-file">{{ ref.filename }}</span>
                <span v-if="ref.chunk_ids?.length" class="chapter-reference-chunks">{{ ref.chunk_ids.length }} chunk(s)</span>
              </div>
            </div>
          </template>
          <div v-else class="chapter-content-empty">{{ t('docAssistant.detail.noContent') }}</div>
        </div>
      </div>
    </template>

    <t-result v-else theme="error" :title="t('docAssistant.detail.loadFailed')" />

    <!-- 单章编辑弹窗 -->
    <t-dialog
      :visible="editDialogVisible"
      :header="t('docAssistant.detail.editChapter')"
      :confirm-btn="{ content: t('common.save'), loading: saving }"
      :cancel-btn="t('common.cancel')"
      width="720px"
      :on-confirm="handleSaveChapter"
      :on-close="closeEditDialog"
      @close="closeEditDialog"
    >
      <div class="edit-dialog-hint">{{ t('docAssistant.detail.editHint') }}</div>
      <t-textarea
        v-model="editContent"
        :autosize="{ minRows: 12, maxRows: 24 }"
        :placeholder="t('docAssistant.detail.editPlaceholder')"
      />
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { MessagePlugin, DialogPlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import MarkdownContent from './components/MarkdownContent.vue'
import {
  getDocDocument,
  listDocDocuments,
  getDocTask,
  updateDocChapter,
  regenerateDocChapter,
  exportDocMarkdown,
  exportDocWord,
  type DocDocument,
  type DocChapter,
  type DocGenerationTask,
  type DocChapterStatus,
} from '@/api/doc-assistant'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

const doc = ref<DocDocument | null>(null)
const task = ref<DocGenerationTask | null>(null)
const versions = ref<DocDocument[]>([])
const loading = ref(true)
const activeChapterNo = ref(1)

const editDialogVisible = ref(false)
const editContent = ref('')
const saving = ref(false)
const exporting = ref<'markdown' | 'word' | ''>('')
const regenerating = ref(false)

const activeChapter = computed<DocChapter | null>(() =>
  doc.value?.chapters.find((ch) => ch.no === activeChapterNo.value) || null,
)

const wordCount = (content?: string): number => (content || '').length

const chapterStatusLabel = (status: DocChapterStatus): string => t(`docAssistant.chapterStatus.${status}`)

const chapterStatusDot = (status?: DocChapterStatus): boolean =>
  status === 'failed' || status === 'skipped'

const loadDocument = async (docId: string) => {
  try {
    const res = await getDocDocument(docId)
    const current = res.data
    doc.value = current
    activeChapterNo.value = current.chapters?.[0]?.no ?? 1
    // 拉取同任务下的全部版本（倒序），供版本切换
    try {
      const verRes = await listDocDocuments(current.task_id)
      versions.value = verRes.data || []
    } catch {
      versions.value = [current]
    }
    // 拉取任务信息（文档名称用于导出文件名与主题标签展示）
    if (current.task_id) {
      try {
        const taskRes = await getDocTask(current.task_id)
        task.value = taskRes.data || null
      } catch {
        task.value = null
      }
    } else {
      task.value = null
    }
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('docAssistant.detail.loadFailed'))
  } finally {
    loading.value = false
  }
}

const switchVersion = async (docId: string) => {
  if (!docId || docId === doc.value?.id) return
  loading.value = true
  await loadDocument(docId)
}

const goBack = () => {
  if (window.history.length > 1) {
    router.back()
  } else {
    router.push('/platform/doc-assistant/tasks')
  }
}

// ---- 单章编辑续写 ----

const openEditDialog = () => {
  if (!activeChapter.value) return
  editContent.value = activeChapter.value.content || ''
  editDialogVisible.value = true
}

const closeEditDialog = () => {
  if (saving.value) return
  editDialogVisible.value = false
}

const handleSaveChapter = async () => {
  if (!doc.value || !activeChapter.value) return
  if (!editContent.value.trim()) {
    MessagePlugin.warning(t('docAssistant.detail.editEmpty'))
    return false
  }
  saving.value = true
  try {
    const res = await updateDocChapter(doc.value.id, activeChapter.value.no, editContent.value)
    doc.value = res.data
    // 刷新版本列表（新版本在前）
    try {
      const verRes = await listDocDocuments(doc.value.task_id)
      versions.value = verRes.data || []
    } catch {
      // 版本列表刷新失败不阻塞
    }
    editDialogVisible.value = false
    MessagePlugin.success(t('docAssistant.detail.editSuccess'))
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('docAssistant.detail.editFailed'))
  } finally {
    saving.value = false
  }
  return true
}

// ---- 单章重新生成 ----

const handleRegenerate = () => {
  if (!doc.value || !activeChapter.value) return
  const chapterNo = activeChapter.value.no
  const chapterTitle = activeChapter.value.title
  const confirmDialog = DialogPlugin.confirm({
    header: t('docAssistant.detail.regenerateConfirmTitle'),
    body: t('docAssistant.detail.regenerateConfirmBody', { title: chapterTitle }),
    confirmBtn: { content: t('docAssistant.detail.regenerateChapter'), theme: 'primary' as const },
    cancelBtn: t('common.cancel'),
    theme: 'warning',
    onConfirm: async () => {
      regenerating.value = true
      try {
        const res = await regenerateDocChapter(doc.value!.id, chapterNo)
        doc.value = res.data
        try {
          const verRes = await listDocDocuments(doc.value.task_id)
          versions.value = verRes.data || []
        } catch {
          // 忽略
        }
        MessagePlugin.success(t('docAssistant.detail.regenerateSuccess'))
      } catch (error: any) {
        MessagePlugin.error(error?.message || t('docAssistant.detail.regenerateFailed'))
      } finally {
        regenerating.value = false
      }
      confirmDialog.destroy()
    },
  })
}

// ---- 导出 ----

const downloadBlob = (blob: Blob, filename: string) => {
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.style.display = 'none'
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  setTimeout(() => {
    link.remove()
    URL.revokeObjectURL(url)
  }, 0)
}

// 导出文件基础名：与文档名称（任务主题）保持一致，异常时回退 document-v版本号
const exportBaseName = (): string => {
  if (!doc.value) return 'document'
  const name = (task.value?.theme || '')
    .replace(/[\\/:*?"<>|\r\n\t]+/g, '_')
    .replace(/^[.\s]+|[.\s]+$/g, '')
  return name || `document-v${doc.value.version}`
}

const handleExport = async (kind: 'markdown' | 'word') => {
  if (!doc.value) return
  exporting.value = kind
  try {
    const blob = kind === 'markdown'
      ? await exportDocMarkdown(doc.value.id)
      : await exportDocWord(doc.value.id)
    const filename = kind === 'markdown'
      ? `${exportBaseName()}.md`
      : `${exportBaseName()}.docx`
    downloadBlob(blob, filename)
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('docAssistant.detail.exportFailed'))
  } finally {
    exporting.value = ''
  }
}

onMounted(() => {
  const docId = route.params.id as string
  if (docId) {
    void loadDocument(docId)
  } else {
    loading.value = false
    MessagePlugin.error(t('docAssistant.detail.missingId'))
  }
})
</script>

<style lang="less" scoped>
.doc-assistant-detail {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  overflow: hidden;
  padding: 16px 24px 24px;
  box-sizing: border-box;
}

.detail-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-shrink: 0;
  padding-bottom: 14px;
  border-bottom: 1px solid var(--td-component-stroke);

  &-left {
    display: flex;
    align-items: center;
    gap: 4px;
    min-width: 0;
  }

  &-actions {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-shrink: 0;
  }
}

.back-btn {
  margin-left: -8px;
}

.detail-title-wrap {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.detail-title {
  margin: 0;
  font-size: 17px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  white-space: nowrap;
}

.detail-theme {
  max-width: 280px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.version-select {
  width: 120px;
}

.detail-body {
  flex: 1;
  min-height: 0;
  display: flex;
  gap: 16px;
  margin-top: 16px;
  overflow: hidden;
}

.chapter-nav {
  flex: 0 0 240px;
  width: 240px;
  min-height: 0;
  overflow-y: auto;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  padding: 10px;

  &-title {
    font-size: 13px;
    font-weight: 600;
    color: var(--td-text-color-secondary);
    padding: 4px 8px 10px;
  }

  &-item {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px;
    border-radius: 6px;
    cursor: pointer;
    margin-bottom: 2px;
    transition: background 0.15s ease;

    &:hover {
      background: var(--td-bg-color-container-hover);
    }

    &--active {
      background: var(--td-brand-color-light);

      .chapter-nav-title-text {
        color: var(--td-brand-color);
        font-weight: 500;
      }
    }
  }

  &-no {
    flex: 0 0 20px;
    width: 20px;
    height: 20px;
    border-radius: 50%;
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-secondary);
    font-size: 11px;
    font-weight: 600;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  &-title-text {
    flex: 1;
    min-width: 0;
    font-size: 13px;
    color: var(--td-text-color-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    line-height: 1.4;
  }

  &-dot {
    flex: 0 0 8px;
    width: 8px;
    height: 8px;
    border-radius: 50%;

    &--failed {
      background: var(--td-error-color);
    }

    &--skipped {
      background: var(--td-warning-color);
    }
  }
}

.chapter-content {
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  overflow: hidden;

  &-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 14px 20px;
    border-bottom: 1px solid var(--td-component-stroke);
    flex-shrink: 0;
  }

  &-title-wrap {
    display: flex;
    align-items: baseline;
    gap: 10px;
    min-width: 0;
  }

  &-title {
    margin: 0;
    font-size: 16px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  &-words {
    font-size: 12px;
    color: var(--td-text-color-placeholder);
    flex-shrink: 0;
  }

  &-actions {
    display: flex;
    align-items: center;
    flex-shrink: 0;
  }

  &-body {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    padding: 16px 24px;
  }

  &-empty {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    color: var(--td-text-color-placeholder);
    font-size: 14px;
  }
}

.chapter-references {
  flex-shrink: 0;
  padding: 14px 24px 18px;
  border-top: 1px solid var(--td-component-stroke);

  &-title {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 13px;
    font-weight: 600;
    color: var(--td-text-color-secondary);
    margin-bottom: 8px;
  }
}

.chapter-reference-item {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  padding: 4px 0;

  .chapter-reference-title {
    color: var(--td-text-color-primary);
    max-width: 60%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .chapter-reference-file {
    color: var(--td-text-color-secondary);
  }

  .chapter-reference-chunks {
    color: var(--td-text-color-placeholder);
    font-size: 11px;
  }
}

.edit-dialog-hint {
  font-size: 13px;
  color: var(--td-text-color-secondary);
  margin-bottom: 12px;
  line-height: 1.6;
}
</style>
