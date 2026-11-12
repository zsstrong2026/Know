<template>
  <div class="doc-assistant-page">
    <div class="page-header">
      <div class="page-header-title">
        <h2>{{ t('docAssistant.generate.title') }}</h2>
        <p class="page-header-subtitle">{{ t('docAssistant.generate.subtitle') }}</p>
      </div>
    </div>

    <div class="page-body generate-body">
      <t-card class="generate-card">
        <t-form label-align="top" :data="form">
          <t-form-item :label="t('docAssistant.generate.template')">
            <t-select
              v-model="form.template_id"
              :placeholder="t('docAssistant.generate.templatePlaceholder')"
              :loading="templateLoading"
              clearable
              filterable
            >
              <t-option v-for="tpl in templates" :key="tpl.id" :value="tpl.id" :label="tpl.name" />
            </t-select>
            <template #help>
              <div class="generate-help">
                <span v-if="selectedTemplate" class="generate-help-text">{{ selectedTemplate.description }}</span>
                <span v-else class="generate-help-text">{{ t('docAssistant.generate.noTemplateHint') }}</span>
                <span v-if="selectedTemplate" class="generate-help-tags">
                  <t-tag v-for="(ch, index) in selectedTemplate.chapters" :key="'tpl-ch-' + index" size="small" variant="outline" class="chapter-tag">
                    {{ ch.title }}
                  </t-tag>
                </span>
              </div>
            </template>
          </t-form-item>

          <t-form-item :label="t('docAssistant.generate.knowledgeBases')" required-mark>
            <t-select
              v-model="form.kb_ids"
              :placeholder="t('docAssistant.generate.knowledgeBasesPlaceholder')"
              multiple
              filterable
              :loading="kbLoading"
            >
              <t-option v-for="kb in knowledgeBases" :key="kb.id" :value="kb.id" :label="kb.name" />
            </t-select>
          </t-form-item>

          <t-form-item :label="t('docAssistant.generate.model')">
            <ModelSelector
              model-type="KnowledgeQA"
              :selected-model-id="form.model_id"
              :placeholder="t('docAssistant.generate.modelPlaceholder')"
              @update:selectedModelId="form.model_id = $event"
            />
            <div class="generate-help">
              <span class="generate-help-text">{{ t('docAssistant.generate.modelHint') }}</span>
            </div>
          </t-form-item>

          <t-form-item :label="t('docAssistant.generate.theme')" required-mark>
            <t-textarea
              v-model="form.theme"
              :placeholder="t('docAssistant.generate.themePlaceholder')"
              :autosize="{ minRows: 3, maxRows: 6 }"
              :maxlength="2000"
            />
          </t-form-item>

          <t-form-item :label="t('docAssistant.generate.extraRequirement')">
            <t-textarea
              v-model="form.extra_requirement"
              :placeholder="t('docAssistant.generate.extraRequirementPlaceholder')"
              :autosize="{ minRows: 2, maxRows: 4 }"
              :maxlength="2000"
            />
          </t-form-item>

          <t-form-item :label="t('docAssistant.generate.archive')">
            <t-checkbox v-model="form.auto_archive">
              {{ t('docAssistant.generate.autoArchive') }}
            </t-checkbox>
            <div class="generate-help">
              <span class="generate-help-text">{{ t('docAssistant.generate.autoArchiveHint') }}</span>
            </div>
          </t-form-item>

          <div class="generate-actions">
            <t-button theme="primary" :loading="submitting" :disabled="!canSubmit" @click="handleSubmit">
              <template #icon><t-icon name="send" /></template>
              {{ t('docAssistant.generate.submit') }}
            </t-button>
            <t-button variant="outline" :disabled="submitting" @click="resetForm">
              {{ t('common.clear') }}
            </t-button>
          </div>
        </t-form>
      </t-card>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import ModelSelector from '@/components/ModelSelector.vue'
import { listDocTemplates, createDocGenerationTask, type DocTemplate } from '@/api/doc-assistant'
import { listKnowledgeBases } from '@/api/knowledge-base'

interface KbOption {
  id: string
  name: string
  type?: string
}

const { t } = useI18n()
const route = useRoute()
const router = useRouter()

const templates = ref<DocTemplate[]>([])
const templateLoading = ref(false)
const knowledgeBases = ref<KbOption[]>([])
const kbLoading = ref(false)
const submitting = ref(false)

const form = reactive<{
  template_id: string
  kb_ids: string[]
  model_id: string
  theme: string
  extra_requirement: string
  auto_archive: boolean
}>({
  template_id: '',
  kb_ids: [],
  model_id: '',
  theme: '',
  extra_requirement: '',
  // 默认勾选：生成的资料默认归到工作知识库，用户可显式关闭（§6.9）。
  auto_archive: true,
})

const selectedTemplate = computed(() =>
  templates.value.find((tpl) => tpl.id === form.template_id) || null,
)

const canSubmit = computed(() =>
  form.kb_ids.length > 0 && form.theme.trim() !== '' && !submitting.value,
)

const loadTemplates = async () => {
  templateLoading.value = true
  try {
    const res = await listDocTemplates()
    templates.value = res.data || []
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('docAssistant.generate.loadTemplateFailed'))
  } finally {
    templateLoading.value = false
  }
}

const loadKnowledgeBases = async () => {
  kbLoading.value = true
  try {
    const res: any = await listKnowledgeBases()
    knowledgeBases.value = (res?.data || []).map((kb: any) => ({
      id: String(kb.id),
      name: kb.name || '',
      type: kb.type,
    }))
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('docAssistant.generate.loadKbFailed'))
  } finally {
    kbLoading.value = false
  }
}

const resetForm = () => {
  form.template_id = ''
  form.kb_ids = []
  form.model_id = ''
  form.theme = ''
  form.extra_requirement = ''
  form.auto_archive = true
}

const handleSubmit = async () => {
  if (form.kb_ids.length === 0) {
    MessagePlugin.warning(t('docAssistant.generate.knowledgeBasesRequired'))
    return
  }
  if (!form.theme.trim()) {
    MessagePlugin.warning(t('docAssistant.generate.themeRequired'))
    return
  }
  submitting.value = true
  try {
    await createDocGenerationTask({
      template_id: form.template_id || undefined,
      kb_ids: form.kb_ids,
      model_id: form.model_id || undefined,
      theme: form.theme.trim(),
      extra_requirement: form.extra_requirement.trim() || undefined,
      // 自动归档：勾选时由服务端使用当前用户的工作知识库，生成成功后入队一次（§6.9）。
      auto_archive: form.auto_archive,
    })
    MessagePlugin.success(t('docAssistant.generate.submitSuccess'))
    router.push('/platform/doc-assistant/tasks')
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('docAssistant.generate.submitFailed'))
  } finally {
    submitting.value = false
  }
}

onMounted(async () => {
  await Promise.all([loadTemplates(), loadKnowledgeBases()])
  // 支持从模板列表「使用模板」入口携带 query 预选
  const presetId = typeof route.query.template_id === 'string' ? route.query.template_id : ''
  if (presetId && templates.value.some((tpl) => tpl.id === presetId)) {
    form.template_id = presetId
  }
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

.generate-body {
  max-width: 760px;
}

.generate-card {
  :deep(.t-card__body) {
    padding: 24px;
  }
}

.generate-actions {
  display: flex;
  gap: 12px;
  margin-top: 8px;
}

.generate-help {
  display: flex;
  flex-direction: column;
  gap: 6px;
  width: 100%;
  margin-top: 6px;

  &-text {
    font-size: 12px;
    color: var(--td-text-color-secondary);
    line-height: 1.6;
  }

  &-tags {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
  }
}

.chapter-tag {
  max-width: 220px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
