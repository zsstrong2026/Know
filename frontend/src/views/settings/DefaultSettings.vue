<template>
  <div class="default-settings">
    <div class="section-header">
      <h2>{{ t('defaultSettings.title') }}</h2>
      <p class="section-description">{{ t('defaultSettings.description') }}</p>
    </div>

    <div class="intro">
      <t-icon name="info-circle" class="intro-icon" />
      <div>
        <p class="intro-title">{{ t('defaultSettings.introTitle') }}</p>
        <p class="intro-desc">{{ t('defaultSettings.introDescription') }}</p>
        <p v-if="!canEdit" class="intro-desc readonly">{{ t('defaultSettings.readOnly') }}</p>
      </div>
    </div>

    <h3 class="group-title">{{ t('defaultSettings.modelGroup') }}</h3>
    <t-alert v-if="settingsLoadFailed" theme="error" :message="t('defaultSettings.loadFailed')">
      <template #operation>
        <t-button variant="text" @click="loadSettings">{{ t('common.retry') }}</t-button>
      </template>
    </t-alert>
    <div class="settings-group">
      <div v-for="slot in modelSlots" :key="slot.field" class="setting-row">
        <div class="setting-info">
          <label>{{ t(`defaultSettings.slots.${slot.key}.label`) }}</label>
          <p class="desc">{{ t(`defaultSettings.slots.${slot.key}.description`) }}</p>
          <p v-if="settingsLoaded" class="desc hint" :class="{ warn: effective[slot.field].stale || effective[slot.field].source === 'none' }">
            {{ effectiveHint(slot, effective[slot.field]) }}
          </p>
        </div>
        <div class="setting-control model-control">
          <ModelSelector
            :model-type="slot.type"
            :selected-model-id="settings[slot.field]"
            :disabled="!canEdit || saving || !settingsLoaded"
            :all-models="models"
            :clearable="true"
            :placeholder="slotPlaceholder(slot)"
            @update:selected-model-id="(id: string) => onModelChange(slot.field, id)"
            @add-model="openModels(slot.type)"
          />
        </div>
      </div>
    </div>

    <template v-if="webSearchSupported">
      <h3 class="group-title">{{ t('defaultSettings.webSearchGroup') }}</h3>
      <div class="settings-group">
        <div class="setting-row">
          <div class="setting-info">
            <label>{{ t('defaultSettings.webSearch.label') }}</label>
            <p class="desc">{{ t('defaultSettings.webSearch.description') }}</p>
            <p v-if="!webSearchDefaultId" class="desc hint warn">{{ t('defaultSettings.webSearch.unset') }}</p>
          </div>
          <div class="setting-control model-control">
            <t-select
              :value="webSearchDefaultId"
              :options="webSearchOptions"
              :disabled="!canEdit || saving || webSearchLoading"
              :loading="webSearchLoading"
              :placeholder="t('defaultSettings.webSearch.placeholder')"
              clearable
              style="width: 100%"
              @change="onWebSearchChange"
            />
          </div>
        </div>
      </div>
    </template>

    <!-- 解析引擎：空间默认路由，知识库未单独配置时按文件类型继承 -->
    <h3 class="group-title">{{ t('defaultSettings.parserGroup') }}</h3>
    <p class="group-desc">{{ t('defaultSettings.parserDescription') }}</p>
    <KBParserSettings
      hide-header
      :parser-engine-rules="parserRules"
      :disabled="!canEdit || parserSaving"
      :show-inherit-controls="false"
      @update:parser-engine-rules="onParserRulesChange"
    />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import ModelSelector from '@/components/ModelSelector.vue'
import KBParserSettings from '@/views/knowledge/settings/KBParserSettings.vue'
import type { ModelConfig } from '@/api/model'
import {
  getParserEngineConfig,
  updateParserEngineConfig,
  type ParserEngineConfig,
  type ParserEngineRule,
} from '@/api/system'
import {
  getTenantDefaultSettings,
  updateTenantDefaultSettings,
  type DefaultSettings,
} from '@/api/default-settings'
import { patchWebSearchProvider } from '@/api/web-search-provider'
import { useAuthStore } from '@/stores/auth'
import { useUIStore } from '@/stores/ui'
import { useChatResourcesStore } from '@/stores/chatResources'
import { useDeploymentCapabilitiesStore } from '@/stores/deploymentCapabilities'
import { useEditorResourcesStore } from '@/stores/editorResources'
import {
  effectiveModelDefault,
  effectiveUtilityDefault,
  type DefaultModelType,
  type EffectiveDefault,
} from './defaultSettingsState'

type ModelField = keyof DefaultSettings

interface ModelSlot {
  field: ModelField
  key: string
  type: DefaultModelType
}

const modelSlots: ModelSlot[] = [
  { field: 'chat_model_id', key: 'chat', type: 'KnowledgeQA' },
  { field: 'utility_model_id', key: 'utility', type: 'KnowledgeQA' },
  { field: 'embedding_model_id', key: 'embedding', type: 'Embedding' },
  { field: 'rerank_model_id', key: 'rerank', type: 'Rerank' },
  { field: 'vlm_model_id', key: 'vlm', type: 'VLLM' },
  { field: 'asr_model_id', key: 'asr', type: 'ASR' },
]

const { t } = useI18n()
const authStore = useAuthStore()
const uiStore = useUIStore()
const chatResources = useChatResourcesStore()
const deploymentCapabilities = useDeploymentCapabilitiesStore()
const editorResources = useEditorResourcesStore()

const canEdit = computed(() => authStore.hasRole('admin'))
const saving = ref(false)
const settingsLoaded = ref(false)
const settingsLoadFailed = ref(false)

const settings = reactive<DefaultSettings>({
  chat_model_id: '',
  utility_model_id: '',
  embedding_model_id: '',
  rerank_model_id: '',
  vlm_model_id: '',
  asr_model_id: '',
})

const models = computed<ModelConfig[]>(() => chatResources.allModels.filter((m) => m.status === 'active'))

const effective = computed(() => {
  const out = {} as Record<ModelField, EffectiveDefault<ModelConfig>>
  for (const slot of modelSlots) {
    out[slot.field] = slot.field === 'utility_model_id'
      ? effectiveUtilityDefault(models.value, settings.utility_model_id, settings.chat_model_id)
      : effectiveModelDefault(models.value, slot.type, settings[slot.field])
  }
  return out
})

const modelName = (model: ModelConfig | null) => model?.display_name?.trim() || model?.name || ''

const effectiveHint = (slot: ModelSlot, state: EffectiveDefault<ModelConfig>) => {
  const name = modelName(state.model)
  if (state.stale) {
    return state.model
      ? t('defaultSettings.hints.staleFallback', { name })
      : t('defaultSettings.hints.staleNone')
  }
  switch (state.source) {
    case 'workspace':
      return t('defaultSettings.hints.workspace')
    case 'system':
      return t('defaultSettings.hints.system', { name })
    case 'auto':
      return t('defaultSettings.hints.auto', { name })
    case 'chat':
      return t('defaultSettings.hints.followChat', { name })
    default:
      return t(`defaultSettings.slots.${slot.key}.none`)
  }
}

const slotPlaceholder = (slot: ModelSlot) =>
  slot.field === 'utility_model_id'
    ? t('defaultSettings.followChatPlaceholder')
    : t('defaultSettings.unsetPlaceholder')

const loadSettings = async () => {
  settingsLoaded.value = false
  settingsLoadFailed.value = false
  try {
    const response = await getTenantDefaultSettings()
    if (!response.data) throw new Error('Default settings response is missing data')
    for (const slot of modelSlots) {
      settings[slot.field] = response.data[slot.field] || ''
    }
    settingsLoaded.value = true
  } catch (error) {
    settingsLoadFailed.value = true
    console.error('Failed to load default settings:', error)
  }
}

const onModelChange = async (field: ModelField, id: string) => {
  if (!canEdit.value || !settingsLoaded.value || saving.value || settings[field] === (id || '')) return
  const previous = settings[field]
  settings[field] = id || ''
  saving.value = true
  try {
    await updateTenantDefaultSettings({ ...settings })
    // Refresh every picker. A failed refresh must not undo an already saved choice.
    await chatResources.ensureModels(true).catch((error) => {
      console.error('Failed to refresh models after saving defaults:', error)
    })
    MessagePlugin.success(t('defaultSettings.toasts.saveSuccess'))
  } catch (error: any) {
    settings[field] = previous
    MessagePlugin.error(t('defaultSettings.toasts.saveFailed', { message: error?.message || '' }))
  } finally {
    saving.value = false
  }
}

const webSearchSupported = computed(() => deploymentCapabilities.isSupported('settings.websearch'))
const webSearchProviders = computed(() => chatResources.webSearchProviders)
const webSearchLoading = ref(false)

const webSearchDefaultId = computed(() => webSearchProviders.value.find((p) => p.is_default)?.id || '')
const webSearchOptions = computed(() =>
  webSearchProviders.value
    .filter((p) => p.id)
    .map((p) => ({ label: p.name, value: p.id as string })),
)

const loadWebSearchProviders = async () => {
  webSearchLoading.value = true
  try {
    await chatResources.ensureWebSearchProviders(true)
  } catch (error) {
    console.error('Failed to load web search providers:', error)
  } finally {
    webSearchLoading.value = false
  }
}

// Providers keep their own is_default flag; setting one clears the others
// on the server, so clearing is the only case that targets the old default.
const onWebSearchChange = async (value: unknown) => {
  const id = typeof value === 'string' ? value : ''
  const current = webSearchDefaultId.value
  if (!canEdit.value || saving.value || webSearchLoading.value || id === current) return
  saving.value = true
  try {
    if (id) {
      await patchWebSearchProvider(id, { is_default: true })
    } else if (current) {
      await patchWebSearchProvider(current, { is_default: false })
    }
    for (const provider of webSearchProviders.value) {
      provider.is_default = provider.id === id
    }
    await loadWebSearchProviders()
    MessagePlugin.success(t('defaultSettings.toasts.saveSuccess'))
  } catch (error: any) {
    MessagePlugin.error(t('defaultSettings.toasts.saveFailed', { message: error?.message || '' }))
  } finally {
    saving.value = false
  }
}

const openModels = (type: DefaultModelType) => {
  const subsection = type === 'KnowledgeQA' ? 'chat' : type === 'VLLM' ? 'vllm' : type.toLowerCase()
  uiStore.openSettings('models', subsection)
  window.dispatchEvent(
    new CustomEvent('settings-nav', { detail: { section: 'models', subsection } }),
  )
}

// ---- 解析引擎默认路由 ----
// 引擎目录接口对所有成员开放，默认路由随它一起返回；保存需要管理员，
// 因此编辑前再取一次完整配置，避免覆盖引擎连接参数。
const parserRules = ref<ParserEngineRule[]>([])
const parserConfig = ref<ParserEngineConfig | null>(null)
const parserSaving = ref(false)

const loadParserDefaults = async () => {
  try {
    await editorResources.ensureParserEngines(true)
    parserRules.value = [...editorResources.parserEngineDefaultRules]
  } catch (error) {
    console.error('Failed to load parser engine defaults:', error)
  }
}

const onParserRulesChange = async (rules: ParserEngineRule[]) => {
  if (!canEdit.value || parserSaving.value) return
  const previous = parserRules.value
  parserRules.value = rules
  parserSaving.value = true
  try {
    const base = parserConfig.value ?? (await getParserEngineConfig())?.data ?? {}
    const res = await updateParserEngineConfig({ ...base, default_parser_engine_rules: rules })
    parserConfig.value = res?.data ?? base
    parserRules.value = res?.data?.default_parser_engine_rules ?? rules
    // 知识库编辑器读的是引擎目录的缓存快照，保存后刷新一次才能立即看到新默认
    await editorResources.ensureParserEngines(true).catch(() => {})
    MessagePlugin.success(t('defaultSettings.toasts.saveSuccess'))
  } catch (error: any) {
    parserRules.value = previous
    MessagePlugin.error(t('defaultSettings.toasts.saveFailed', { message: error?.message || '' }))
  } finally {
    parserSaving.value = false
  }
}

onMounted(() => {
  void chatResources.ensureModels(true).catch(() => {})
  void loadSettings()
  void loadParserDefaults()
  if (webSearchSupported.value) void loadWebSearchProviders()
})
</script>

<style lang="less" scoped>
@import (reference) '@/components/css/settings-section.less';

.default-settings {
  width: 100%;
}

.section-header {
  .settings-section-header();
}

.intro {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 14px 16px;
  margin-bottom: 8px;
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-secondarycontainer);
}

.intro-icon {
  color: var(--td-brand-color);
  margin-top: 2px;
  flex-shrink: 0;
}

.intro-title {
  margin: 0 0 4px 0;
  font-size: var(--app-text-base);
  font-weight: 500;
  color: var(--td-text-color-primary);
}

.intro-desc {
  margin: 0;
  font-size: var(--app-text-md);
  line-height: 1.6;
  color: var(--td-text-color-secondary);

  &.readonly {
    margin-top: 6px;
    color: var(--td-warning-color);
  }
}

.group-title {
  margin: 24px 0 0;
  font-size: var(--app-text-base);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.group-desc {
  margin: 6px 0 12px;
  font-size: var(--app-text-md);
  line-height: 1.6;
  color: var(--td-text-color-secondary);
}

.settings-group {
  display: flex;
  flex-direction: column;
}

.setting-row {
  .setting-row();
}

.setting-info {
  .setting-info();

  .hint {
    margin-top: 4px !important;
    color: var(--td-text-color-placeholder);

    &.warn {
      color: var(--td-warning-color);
    }
  }
}

.setting-control {
  .setting-control();
}

.model-control {
  min-width: 280px;
}
</style>
