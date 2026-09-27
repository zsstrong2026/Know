<template>
  <t-dialog
    :visible="visible"
    width="640px"
    :header="t('docAssistant.generate.title')"
    :confirm-btn="{ content: t('docAssistant.generate.submit'), loading: submitting }"
    :cancel-btn="t('common.cancel')"
    :on-confirm="handleSubmit"
    :on-close="close"
    @close="close"
  >
    <t-form label-align="top" :data="form" class="generate-dialog-form">
      <t-form-item :label="t('docAssistant.generate.template')">
        <t-select
          v-model="form.template_id"
          :placeholder="t('docAssistant.generate.templatePlaceholder')"
          clearable
          filterable
        >
          <t-option v-for="tpl in templates" :key="tpl.id" :value="tpl.id" :label="tpl.name" />
        </t-select>
        <template #help>
          <span v-if="selectedTemplate" class="gen-dialog-help">{{ selectedTemplate.description }}</span>
          <span v-else class="gen-dialog-help">{{ t('docAssistant.generate.noTemplateHint') }}</span>
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
        <template #help>
          <span class="gen-dialog-help">{{ t('docAssistant.generate.modelHint') }}</span>
        </template>
      </t-form-item>

      <t-form-item :label="t('docAssistant.generate.theme')" required-mark>
        <t-textarea
          v-model="form.theme"
          :placeholder="t('docAssistant.generate.themePlaceholder')"
          :autosize="{ minRows: 2, maxRows: 4 }"
          :maxlength="2000"
        />
      </t-form-item>

      <t-form-item :label="t('docAssistant.generate.extraRequirement')">
        <t-textarea
          v-model="form.extra_requirement"
          :placeholder="t('docAssistant.generate.extraRequirementPlaceholder')"
          :autosize="{ minRows: 2, maxRows: 3 }"
          :maxlength="2000"
        />
      </t-form-item>
    </t-form>
  </t-dialog>
</template>

<script setup lang="ts">
import { ref, reactive, computed, watch, onMounted } from 'vue'
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

const props = defineProps<{
  visible: boolean
  presetTemplateId?: string
}>()

const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void
  (e: 'success'): void
}>()

const { t } = useI18n()

const templates = ref<DocTemplate[]>([])
const knowledgeBases = ref<KbOption[]>([])
const kbLoading = ref(false)
const submitting = ref(false)

const form = reactive<{
  template_id: string
  kb_ids: string[]
  model_id: string
  theme: string
  extra_requirement: string
}>({
  template_id: '',
  kb_ids: [],
  model_id: '',
  theme: '',
  extra_requirement: '',
})

const selectedTemplate = computed(
  () => templates.value.find((tpl) => tpl.id === form.template_id) || null,
)

const close = () => emit('update:visible', false)

const resetForm = () => {
  form.template_id = props.presetTemplateId || ''
  form.kb_ids = []
  form.model_id = ''
  form.theme = ''
  form.extra_requirement = ''
}

const loadTemplates = async () => {
  try {
    const res = await listDocTemplates()
    templates.value = res.data || []
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('docAssistant.generate.loadTemplateFailed'))
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

const handleSubmit = async () => {
  if (form.kb_ids.length === 0) {
    MessagePlugin.warning(t('docAssistant.generate.knowledgeBasesRequired'))
    return false
  }
  if (!form.theme.trim()) {
    MessagePlugin.warning(t('docAssistant.generate.themeRequired'))
    return false
  }
  submitting.value = true
  try {
    await createDocGenerationTask({
      template_id: form.template_id || undefined,
      kb_ids: form.kb_ids,
      model_id: form.model_id || undefined,
      theme: form.theme.trim(),
      extra_requirement: form.extra_requirement.trim() || undefined,
    })
    MessagePlugin.success(t('docAssistant.generate.submitSuccess'))
    emit('success')
    close()
  } catch (error: any) {
    MessagePlugin.error(error?.message || t('docAssistant.generate.submitFailed'))
    return false
  } finally {
    submitting.value = false
  }
  return true
}

watch(
  () => props.visible,
  (visible) => {
    if (visible) resetForm()
  },
)

onMounted(() => {
  void loadTemplates()
  void loadKnowledgeBases()
})
</script>

<style lang="less" scoped>
.generate-dialog-form :deep(.t-form__item) {
  margin-bottom: 18px;
}

.gen-dialog-help {
  font-size: 12px;
  color: var(--td-text-color-secondary);
  line-height: 1.6;
  display: block;
  margin-top: 4px;
}
</style>