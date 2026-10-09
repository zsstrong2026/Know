<template>
  <div class="doc-assistant-page">
    <div class="page-header">
      <div class="page-header-title">
        <div class="page-back-row" v-if="showBack">
          <t-button size="small" variant="text" theme="primary" @click="goBack">
            <template #icon><t-icon name="arrow-left" /></template>
            {{ t('docAssistant.template.back') }}
          </t-button>
        </div>
        <h2>{{ t('docAssistant.template.title') }}</h2>
        <p class="page-header-subtitle">{{ t('docAssistant.template.subtitle') }}</p>
      </div>
      <t-button theme="primary" @click="openCreate">
        <template #icon><t-icon name="add" /></template>
        {{ t('docAssistant.template.create') }}
      </t-button>
    </div>

    <div class="page-body">
      <t-skeleton v-if="loading" animation="gradient" :row-col="[{ width: '100%', height: '120px' }, { width: '100%', height: '120px' }]" />
      <t-empty v-else-if="templates.length === 0" :description="t('docAssistant.template.empty')" />
      <div v-else class="template-grid">
        <div v-for="tpl in templates" :key="tpl.id" class="template-card">
          <div class="template-card-header">
            <span class="template-card-name" :title="tpl.name">{{ tpl.name }}</span>
            <t-tag theme="primary" variant="light">{{ t('docAssistant.template.chaptersCount', { count: tpl.chapters?.length ?? 0 }) }}</t-tag>
          </div>
          <p v-if="tpl.description" class="template-card-desc" :title="tpl.description">{{ tpl.description }}</p>
          <p v-else class="template-card-desc template-card-desc--empty">{{ t('docAssistant.template.noDescription') }}</p>
          <div v-if="tpl.global_config && (tpl.global_config.default_tone || tpl.global_config.target_audience || tpl.global_config.top_k)" class="template-card-meta">
            <span v-if="tpl.global_config.default_tone" class="template-meta-item">
              <t-icon name="sound" size="14px" />
              {{ t('docAssistant.template.defaultTone') }}：{{ tpl.global_config.default_tone }}
            </span>
            <span v-if="tpl.global_config.target_audience" class="template-meta-item">
              <t-icon name="usergroup" size="14px" />
              {{ t('docAssistant.template.targetAudience') }}：{{ tpl.global_config.target_audience }}
            </span>
            <span v-if="tpl.global_config.top_k" class="template-meta-item">
              <t-icon name="layers" size="14px" />
              top_k：{{ tpl.global_config.top_k }}
            </span>
          </div>
          <div class="template-card-footer">
            <span class="template-card-time">{{ formatTime(tpl.updated_at) }}</span>
            <div class="template-card-actions">
              <t-button size="small" variant="text" theme="primary" @click="useTemplate(tpl)">
                <template #icon><t-icon name="play" /></template>
                {{ t('docAssistant.template.use') }}
              </t-button>
              <t-button size="small" variant="text" theme="primary" @click="openEdit(tpl)">
                <template #icon><t-icon name="edit-1" /></template>
                {{ t('common.edit') }}
              </t-button>
              <t-button size="small" variant="text" theme="danger" @click="handleDelete(tpl)">
                <template #icon><t-icon name="delete" /></template>
                {{ t('common.delete') }}
              </t-button>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- 新建 / 编辑弹窗 -->
    <t-dialog
      class="doc-assistant-dialog"
      :visible="dialogVisible"
      :header="editingId ? t('docAssistant.template.edit') : t('docAssistant.template.create')"
      :confirm-btn="{ content: t('common.save'), loading: saving }"
      :cancel-btn="t('common.cancel')"
      width="720px"
      :on-confirm="handleSave"
      :on-close="closeDialog"
      @close="closeDialog"
    >
      <t-form label-align="top" :data="form">
        <t-form-item :label="t('docAssistant.template.name')" required-mark>
          <t-input v-model="form.name" :placeholder="t('docAssistant.template.namePlaceholder')" :maxlength="200" />
        </t-form-item>
        <t-form-item :label="t('docAssistant.template.description')">
          <t-textarea v-model="form.description" :placeholder="t('docAssistant.template.descriptionPlaceholder')" :autosize="{ minRows: 2, maxRows: 4 }" />
        </t-form-item>

        <div class="form-section-title">{{ t('docAssistant.template.globalConfig') }}</div>
        <div class="global-config-grid">
          <t-form-item :label="t('docAssistant.template.defaultTone')">
            <t-input v-model="form.global_config.default_tone" :placeholder="t('docAssistant.template.defaultTonePlaceholder')" />
          </t-form-item>
          <t-form-item :label="t('docAssistant.template.targetAudience')">
            <t-input v-model="form.global_config.target_audience" :placeholder="t('docAssistant.template.targetAudiencePlaceholder')" />
          </t-form-item>
          <t-form-item :label="t('docAssistant.template.defaultExtraRequirement')">
            <t-input v-model="form.global_config.default_extra_requirement" :placeholder="t('docAssistant.template.defaultExtraRequirementPlaceholder')" />
          </t-form-item>
          <t-form-item :label="t('docAssistant.template.topK')">
            <t-input-number v-model="form.global_config.top_k" :min="0" :max="20" theme="column" />
          </t-form-item>
        </div>

        <div class="form-section-title form-section-title--chapters">
          <span>{{ t('docAssistant.template.chapters') }}</span>
        </div>
        <t-form-item v-if="form.chapters.length === 0">
          <div class="chapters-empty">{{ t('docAssistant.template.emptyChapters') }}</div>
        </t-form-item>
        <div v-for="(chapter, index) in form.chapters" :key="index" class="chapter-editor">
          <div class="chapter-editor-head">
            <span class="chapter-editor-no">{{ index + 1 }}</span>
            <t-input v-model="chapter.title" class="chapter-editor-title" :placeholder="t('docAssistant.template.chapterTitlePlaceholder')" :maxlength="200" />
            <t-button size="small" variant="text" theme="danger" :disabled="form.chapters.length <= 1" @click="removeChapter(index)">
              <template #icon><t-icon name="delete" /></template>
            </t-button>
          </div>
          <t-textarea v-model="chapter.instruction" class="chapter-editor-instruction" :placeholder="t('docAssistant.template.instructionPlaceholder')" :autosize="{ minRows: 2, maxRows: 5 }" />
          <div class="chapter-editor-options">
            <t-form-item :label="t('docAssistant.template.maxWords')" class="chapter-option">
              <t-input-number v-model="chapter.max_words" :min="0" :max="5000" theme="column" :placeholder="t('docAssistant.template.maxWordsPlaceholder')" />
            </t-form-item>
            <div class="chapter-option chapter-switch">
              <span class="chapter-switch-label">{{ t('docAssistant.template.mustReference') }}</span>
              <t-switch v-model="chapter.must_reference" />
            </div>
            <div class="chapter-option chapter-switch">
              <span class="chapter-switch-label">{{ t('docAssistant.template.skippable') }}</span>
              <t-switch v-model="chapter.skippable" />
            </div>
          </div>
        </div>
        <div class="chapter-add">
          <t-button size="small" theme="primary" variant="outline" @click="addChapter">
            <template #icon><t-icon name="add" /></template>
            {{ t('docAssistant.template.addChapter') }}
          </t-button>
        </div>
      </t-form>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { MessagePlugin, DialogPlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import {
  listDocTemplates,
  createDocTemplate,
  updateDocTemplate,
  deleteDocTemplate,
  type DocTemplate,
  type DocTemplatePayload,
  type DocTemplateChapter,
} from '@/api/doc-assistant'

const { t } = useI18n()
const router = useRouter()

const loading = ref(false)
const saving = ref(false)
const templates = ref<DocTemplate[]>([])
const dialogVisible = ref(false)
const editingId = ref('')

const makeEmptyChapter = (): DocTemplateChapter => ({
  title: '',
  instruction: '',
  max_words: undefined,
  must_reference: false,
  skippable: false,
})

interface TemplateForm {
  name: string
  description: string
  global_config: NonNullable<DocTemplatePayload['global_config']>
  chapters: DocTemplateChapter[]
}

const form = reactive<TemplateForm>({
  name: '',
  description: '',
  global_config: {
    default_tone: '',
    target_audience: '',
    default_extra_requirement: '',
    top_k: undefined,
  },
  chapters: [makeEmptyChapter()],
})

const formatTime = (value?: string): string => {
  if (!value) return ''
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return value
  return d.toLocaleString()
}

const loadTemplates = async () => {
  loading.value = true
  try {
    const res = await listDocTemplates()
    templates.value = res.data || []
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('docAssistant.template.loadFailed'))
  } finally {
    loading.value = false
  }
}

const resetForm = () => {
  editingId.value = ''
  form.name = ''
  form.description = ''
  form.global_config = { default_tone: '', target_audience: '', default_extra_requirement: '', top_k: undefined }
  form.chapters = [makeEmptyChapter()]
}

const openCreate = () => {
  resetForm()
  dialogVisible.value = true
}

const openEdit = (tpl: DocTemplate) => {
  editingId.value = tpl.id
  form.name = tpl.name || ''
  form.description = tpl.description || ''
  form.global_config = {
    default_tone: tpl.global_config?.default_tone || '',
    target_audience: tpl.global_config?.target_audience || '',
    default_extra_requirement: tpl.global_config?.default_extra_requirement || '',
    top_k: tpl.global_config?.top_k ?? undefined,
  }
  form.chapters = (tpl.chapters?.length ? tpl.chapters : [makeEmptyChapter()]).map((ch) => ({
    title: ch.title || '',
    instruction: ch.instruction || '',
    max_words: ch.max_words ?? undefined,
    must_reference: !!ch.must_reference,
    skippable: !!ch.skippable,
  }))
  dialogVisible.value = true
}

const closeDialog = () => {
  if (saving.value) return
  dialogVisible.value = false
}

// 使用模板：跳转文档列表页并自动打开生成弹窗预选该模板
const useTemplate = (tpl: DocTemplate) => {
  router.push({ path: '/platform/doc-assistant/tasks', query: { template_id: tpl.id } })
}

// 模板管理为二级页：显示返回方案助手首页入口
const showBack = true
const goBack = () => {
  router.push('/platform/doc-assistant')
}

const addChapter = () => {
  if (form.chapters.length >= 30) {
    MessagePlugin.warning(t('docAssistant.template.maxChapters'))
    return
  }
  form.chapters.push(makeEmptyChapter())
}

const removeChapter = (index: number) => {
  if (form.chapters.length <= 1) return
  form.chapters.splice(index, 1)
}

const handleSave = async () => {
  const name = form.name.trim()
  if (!name) {
    MessagePlugin.warning(t('docAssistant.template.nameRequired'))
    return false
  }
  const chapters = form.chapters
    .filter((ch) => ch.title.trim())
    .map((ch) => ({ ...ch, title: ch.title.trim() }))
  if (chapters.length === 0) {
    MessagePlugin.warning(t('docAssistant.template.chapterTitleRequired'))
    return false
  }
  const payload: DocTemplatePayload = {
    name,
    description: form.description?.trim() || '',
    global_config: {
      default_tone: form.global_config.default_tone?.trim() || '',
      target_audience: form.global_config.target_audience?.trim() || '',
      default_extra_requirement: form.global_config.default_extra_requirement?.trim() || '',
      top_k: form.global_config.top_k || 0,
    },
    chapters,
  }
  saving.value = true
  try {
    if (editingId.value) {
      await updateDocTemplate(editingId.value, payload)
      MessagePlugin.success(t('docAssistant.template.updateSuccess'))
    } else {
      await createDocTemplate(payload)
      MessagePlugin.success(t('docAssistant.template.createSuccess'))
    }
    dialogVisible.value = false
    await loadTemplates()
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('docAssistant.template.saveFailed'))
  } finally {
    saving.value = false
  }
  return true
}

const handleDelete = (tpl: DocTemplate) => {
  const confirmDialog = DialogPlugin.confirm({
    header: t('docAssistant.template.deleteConfirmTitle'),
    body: t('docAssistant.template.deleteConfirmBody', { name: tpl.name }),
    confirmBtn: { content: t('common.delete'), theme: 'danger' as const },
    cancelBtn: t('common.cancel'),
    theme: 'warning',
    onConfirm: async () => {
      try {
        await deleteDocTemplate(tpl.id)
        MessagePlugin.success(t('docAssistant.template.deleteSuccess'))
        await loadTemplates()
      } catch (error: any) {
        MessagePlugin.error(error?.message || t('docAssistant.template.deleteFailed'))
      }
      confirmDialog.destroy()
    },
  })
}

onMounted(() => {
  void loadTemplates()
})
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

.page-back-row {
  display: flex;
  align-items: center;
  margin-bottom: 4px;
}

.page-body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 0 24px 24px;
}

.template-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 16px;
}

.template-card {
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: 8px;
  padding: 16px;
  display: flex;
  flex-direction: column;
  gap: 10px;
  transition: box-shadow 0.2s ease;

  &:hover {
    box-shadow: var(--td-shadow-2);
  }

  &-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 8px;
  }

  &-name {
    font-size: 15px;
    font-weight: 600;
    color: var(--td-text-color-primary);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  &-desc {
    margin: 0;
    font-size: 13px;
    line-height: 1.6;
    color: var(--td-text-color-secondary);
    display: -webkit-box;
    -webkit-line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
    min-height: 20px;

    &--empty {
      color: var(--td-text-color-placeholder);
    }
  }

  &-meta {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
  }

  &-footer {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-top: auto;
    padding-top: 4px;
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

.template-meta-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: 12px;
  color: var(--td-text-color-secondary);
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 4px;
  padding: 2px 8px;
}

.form-section-title {
  font-size: 14px;
  font-weight: 600;
  color: var(--td-text-color-primary);
  margin: 12px 0 8px;
  display: flex;
  align-items: center;
  justify-content: space-between;

  &--chapters {
    margin-top: 20px;
    padding-top: 16px;
    border-top: 1px solid var(--td-component-stroke);
  }
}

.global-config-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0 16px;
}

.chapters-empty {
  width: 100%;
  text-align: center;
  padding: 24px 0;
  color: var(--td-text-color-placeholder);
  font-size: 13px;
  background: var(--td-bg-color-secondarycontainer);
  border-radius: 6px;
  border: 1px dashed var(--td-component-border);
}

.chapter-editor {
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  padding: 12px;
  margin-bottom: 12px;
  background: var(--td-bg-color-secondarycontainer);

  &-head {
    display: flex;
    align-items: center;
    gap: 8px;
  }

  &-no {
    flex: 0 0 24px;
    height: 24px;
    border-radius: 50%;
    background: var(--td-brand-color-light);
    color: var(--td-brand-color);
    font-size: 12px;
    font-weight: 600;
    display: flex;
    align-items: center;
    justify-content: center;
  }

  &-title {
    flex: 1;
  }

  &-instruction {
    margin-top: 8px;
  }

  &-options {
    display: flex;
    align-items: flex-end;
    flex-wrap: wrap;
    gap: 16px;
    margin-top: 8px;
  }
}

.chapter-add {
  display: flex;
  justify-content: center;
  margin-top: 4px;
}

.chapter-option {
  margin-bottom: 0;

  &.chapter-switch {
    display: flex;
    align-items: center;
    gap: 8px;
    padding-bottom: 2px;
  }
}

.chapter-switch-label {
  font-size: 13px;
  color: var(--td-text-color-secondary);
  white-space: nowrap;
}
</style>

<style lang="less">
/* 模板弹窗固定高度、内容滚动（t-dialog teleport 到 body，须用全局样式） */
.doc-assistant-dialog .t-dialog__body {
  height: min(560px, calc(100vh - 220px));
  overflow-y: auto;
  overscroll-behavior: contain;
}
</style>
