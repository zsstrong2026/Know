<template>
  <!-- 工作台设置（§9.14）：编辑器 15 项 + 工作台 AI 4 项；都是个人偏好 -->
  <section class="ws-page">
    <header class="ws-head">
      <div>
        <div class="ws-title">{{ t('workbench.settings.title') }}</div>
        <div class="ws-sub">{{ t('workbench.settings.subtitle') }}</div>
      </div>
      <span class="spacer" />
      <div class="ws-tabs" role="tablist" :aria-label="t('workbench.settings.title')">
        <button
          v-for="tab in TABS"
          :key="tab.id"
          type="button"
          role="tab"
          :aria-selected="activeTab === tab.id"
          :class="{ 'is-active': activeTab === tab.id }"
          @click="switchTab(tab.id)"
        >
          {{ t(tab.labelKey) }}
        </button>
      </div>
    </header>

    <div class="ws-body">
      <!-- 编辑器标签页 -->
      <template v-if="activeTab === 'editor'">
        <t-card :title="t('workbench.settings.editor.text')" :bordered="false" class="ws-card">
          <div class="ws-field">
            <span class="ws-label">{{ t('workbench.settings.editor.fontFamily') }}</span>
            <t-radio-group
              :value="editorPrefs.fontFamily"
              variant="default-filled"
              size="small"
              @change="(value: string | number | boolean) => updateEditorPrefs({ fontFamily: String(value) as FontFamily })"
            >
              <t-radio-button v-for="option in FONT_FAMILIES" :key="option" :value="option">
                {{ t(`workbench.settings.fontFamily.${option}`) }}
              </t-radio-button>
            </t-radio-group>
          </div>

          <div class="ws-field">
            <span class="ws-label">{{ t('workbench.settings.editor.fontSize') }}</span>
            <t-slider
              :value="editorPrefs.fontSize"
              :min="FONT_SIZE_MIN"
              :max="FONT_SIZE_MAX"
              :step="1"
              class="ws-slider"
              @change="(value: string | number | boolean) => updateEditorPrefs({ fontSize: Number(value) })"
            />
            <span class="ws-value">{{ editorPrefs.fontSize }}px</span>
          </div>

          <div class="ws-field">
            <span class="ws-label">{{ t('workbench.settings.editor.pageWidth') }}</span>
            <t-radio-group
              :value="editorPrefs.pageWidth"
              variant="default-filled"
              size="small"
              @change="(value: string | number | boolean) => updateEditorPrefs({ pageWidth: String(value) as PageWidth })"
            >
              <t-radio-button v-for="option in PAGE_WIDTHS" :key="option" :value="option">
                {{ t(`workbench.settings.pageWidth.${option}`) }}
              </t-radio-button>
            </t-radio-group>
          </div>

          <div class="ws-field">
            <span class="ws-label">{{ t('workbench.settings.editor.showDocInfo') }}</span>
            <t-switch
              :value="editorPrefs.showDocInfo"
              @change="(value: string | number | boolean) => updateEditorPrefs({ showDocInfo: Boolean(value) })"
            />
          </div>

          <div class="ws-field">
            <span class="ws-label">{{ t('workbench.settings.editor.codeLineNumbers') }}</span>
            <t-switch
              :value="editorPrefs.codeLineNumbers"
              @change="(value: string | number | boolean) => updateEditorPrefs({ codeLineNumbers: Boolean(value) })"
            />
          </div>
        </t-card>

        <t-card :title="t('workbench.settings.editor.create')" :bordered="false" class="ws-card">
          <div class="ws-field">
            <span class="ws-label">{{ t('workbench.settings.editor.defaultCreateMode') }}</span>
            <t-select
              :value="editorPrefs.defaultCreateMode"
              size="small"
              class="ws-select"
              @change="(value: string | number | boolean) => updateEditorPrefs({ defaultCreateMode: String(value) as CreateMode })"
            >
              <t-option v-for="option in CREATE_MODES" :key="option" :value="option" :label="t(`workbench.settings.createMode.${option}`)" />
            </t-select>
            <p class="ws-hint">{{ t('workbench.settings.editor.createModeHint') }}</p>
          </div>

          <div class="ws-field">
            <span class="ws-label">{{ t('workbench.settings.editor.autoNameWithDate') }}</span>
            <t-switch
              :value="editorPrefs.autoNameWithDate"
              @change="(value: string | number | boolean) => updateEditorPrefs({ autoNameWithDate: Boolean(value) })"
            />
            <p class="ws-hint">{{ t('workbench.settings.editor.autoNameHint') }}</p>
          </div>

          <div class="ws-field">
            <span class="ws-label">{{ t('workbench.settings.editor.showAddIcon') }}</span>
            <t-switch
              :value="editorPrefs.showAddIcon"
              @change="(value: string | number | boolean) => updateEditorPrefs({ showAddIcon: Boolean(value) })"
            />
          </div>
        </t-card>

        <t-card :title="t('workbench.settings.editor.canvas')" :bordered="false" class="ws-card">
          <div class="ws-field">
            <span class="ws-label">{{ t('workbench.settings.editor.canvasScheme') }}</span>
            <t-select
              :value="editorPrefs.canvasScheme"
              size="small"
              class="ws-select"
              @change="(value: string | number | boolean) => updateEditorPrefs({ canvasScheme: String(value) as CanvasScheme })"
            >
              <t-option v-for="option in CANVAS_SCHEMES" :key="option" :value="option" :label="t(`workbench.settings.canvasScheme.${option}`)" />
            </t-select>
          </div>

          <div class="ws-field">
            <span class="ws-label">{{ t('workbench.settings.editor.scrollZoom') }}</span>
            <t-switch
              :value="editorPrefs.scrollZoom"
              @change="(value: string | number | boolean) => updateEditorPrefs({ scrollZoom: Boolean(value) })"
            />
            <p class="ws-hint">{{ t('workbench.settings.editor.scrollZoomHint') }}</p>
          </div>
        </t-card>

        <t-card :title="t('workbench.settings.editor.note')" :bordered="false" class="ws-card">
          <div class="ws-field">
            <span class="ws-label">{{ t('workbench.settings.editor.noteBackground') }}</span>
            <t-select
              :value="editorPrefs.noteBackground"
              size="small"
              class="ws-select"
              @change="(value: string | number | boolean) => updateEditorPrefs({ noteBackground: String(value) as NoteBackground })"
            >
              <t-option v-for="option in NOTE_BACKGROUNDS" :key="option" :value="option" :label="t(`workbench.settings.noteBackground.${option}`)" />
            </t-select>
          </div>

          <div class="ws-field">
            <span class="ws-label">{{ t('workbench.settings.editor.noteRadius') }}</span>
            <t-select
              :value="editorPrefs.noteRadius"
              size="small"
              class="ws-select"
              @change="(value: string | number | boolean) => updateEditorPrefs({ noteRadius: String(value) as NoteRadius })"
            >
              <t-option v-for="option in NOTE_RADII" :key="option" :value="option" :label="t(`workbench.settings.noteRadius.${option}`)" />
            </t-select>
          </div>

          <div class="ws-field">
            <span class="ws-label">{{ t('workbench.settings.editor.noteShadow') }}</span>
            <t-select
              :value="editorPrefs.noteShadow"
              size="small"
              class="ws-select"
              @change="(value: string | number | boolean) => updateEditorPrefs({ noteShadow: String(value) as NoteShadow })"
            >
              <t-option v-for="option in NOTE_SHADOWS" :key="option" :value="option" :label="t(`workbench.settings.noteShadow.${option}`)" />
            </t-select>
          </div>

          <div class="ws-field">
            <span class="ws-label">{{ t('workbench.settings.editor.noteBorderStyle') }}</span>
            <t-radio-group
              :value="editorPrefs.noteBorderStyle"
              variant="default-filled"
              size="small"
              @change="(value: string | number | boolean) => updateEditorPrefs({ noteBorderStyle: String(value) as NoteBorderStyle })"
            >
              <t-radio-button v-for="option in NOTE_BORDER_STYLES" :key="option" :value="option">
                {{ t(`workbench.settings.noteBorderStyle.${option}`) }}
              </t-radio-button>
            </t-radio-group>
          </div>

          <div class="ws-field">
            <span class="ws-label">{{ t('workbench.settings.editor.noteBorderWidth') }}</span>
            <t-slider
              :value="editorPrefs.noteBorderWidth"
              :min="BORDER_WIDTH_MIN"
              :max="BORDER_WIDTH_MAX"
              :step="1"
              :disabled="editorPrefs.noteBorderStyle === 'none'"
              class="ws-slider"
              @change="(value: string | number | boolean) => updateEditorPrefs({ noteBorderWidth: Number(value) })"
            />
            <span class="ws-value">{{ editorPrefs.noteBorderWidth }}px</span>
            <p v-if="editorPrefs.noteBorderStyle === 'none'" class="ws-hint">
              {{ t('workbench.settings.editor.borderWidthDisabled') }}
            </p>
          </div>
        </t-card>

        <div class="ws-actions">
          <t-button variant="outline" @click="onResetEditor">
            {{ t('workbench.settings.resetEditor') }}
          </t-button>
        </div>
      </template>

      <!-- 工作台 AI 标签页 -->
      <template v-else>
        <t-card :title="t('workbench.settings.ai.general')" :bordered="false" class="ws-card">
          <div class="ws-field">
            <span class="ws-label">{{ t('workbench.settings.ai.enabled') }}</span>
            <t-switch
              :value="aiPrefs.enabled"
              @change="(value: string | number | boolean) => updateAIPrefs({ enabled: Boolean(value) })"
            />
            <p class="ws-hint">{{ t('workbench.settings.ai.enabledHint') }}</p>
          </div>

          <div class="ws-field">
            <span class="ws-label">{{ t('workbench.settings.ai.citations') }}</span>
            <t-switch
              :value="aiPrefs.citations"
              @change="(value: string | number | boolean) => updateAIPrefs({ citations: Boolean(value) })"
            />
          </div>

          <div class="ws-field">
            <span class="ws-label">{{ t('workbench.settings.ai.confirmInsert') }}</span>
            <t-switch
              :value="aiPrefs.confirmInsert"
              @change="(value: string | number | boolean) => updateAIPrefs({ confirmInsert: Boolean(value) })"
            />
            <p class="ws-hint">{{ t('workbench.settings.ai.confirmInsertHint') }}</p>
          </div>
        </t-card>

        <t-card :title="t('workbench.settings.ai.agent')" :bordered="false" class="ws-card">
          <div v-if="agentsLoading" class="ws-loading"><t-loading :loading="true" size="small" /></div>
          <div v-else-if="agentCards.length === 0" class="ws-empty">
            <p>{{ t('workbench.settings.ai.noAgents') }}</p>
            <t-button size="small" variant="outline" @click="goAgents">
              {{ t('workbench.settings.ai.manageAgents') }}
            </t-button>
          </div>
          <div v-else class="ws-agents">
            <!-- 卡片只展示工作台关心的信息，不展示 KB/MCP/IM 等无关配置 -->
            <button
              v-for="agent in agentCards"
              :key="agent.id"
              type="button"
              class="ws-agent"
              :class="{ 'is-active': aiPrefs.defaultAgentId === agent.id, 'is-disabled': !agent.ready }"
              :aria-pressed="aiPrefs.defaultAgentId === agent.id"
              :disabled="!agent.ready"
              @click="updateAIPrefs({ defaultAgentId: agent.id })"
            >
              <span class="ws-agent-name">{{ agent.name }}</span>
              <span class="ws-agent-meta">
                {{ agent.builtin ? t('workbench.settings.ai.builtin') : t('workbench.settings.ai.mine') }}
                <template v-if="agent.model"> · {{ agent.model }}</template>
              </span>
              <span v-if="!agent.ready" class="ws-agent-warn">
                {{ t('workbench.settings.ai.notReady') }}
              </span>
              <span v-else-if="agent.description" class="ws-agent-desc">{{ agent.description }}</span>
            </button>
          </div>
          <div class="ws-scope">
            <t-icon name="lock-on" size="12px" />
            <span>{{ t('workbench.settings.ai.dataScope') }}</span>
          </div>
        </t-card>

        <div class="ws-actions">
          <t-button variant="outline" @click="onResetAI">
            {{ t('workbench.settings.resetAI') }}
          </t-button>
          <t-button variant="text" @click="goAgents">
            {{ t('workbench.settings.ai.manageAgents') }}
          </t-button>
        </div>
      </template>

      <p class="ws-foot">{{ t('workbench.settings.storageNote') }}</p>
    </div>
  </section>
</template>

<script setup lang="ts">
/**
 * 工作台设置页（§9.14）。
 *
 * 两件事都是个人偏好：改完立即生效，没有「保存」按钮；「恢复默认」只重置
 * 当前标签页那一份。偏好不写 Y.Doc、不产生 snapshot_version、不影响归档。
 */
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { listAgents } from '@/api/agent'
import { useWorkbenchPrefs } from '@/workbench/use-workbench-prefs'
import {
  BORDER_WIDTH_MAX,
  BORDER_WIDTH_MIN,
  CANVAS_SCHEMES,
  CREATE_MODES,
  FONT_FAMILIES,
  FONT_SIZE_MAX,
  FONT_SIZE_MIN,
  NOTE_BACKGROUNDS,
  NOTE_BORDER_STYLES,
  NOTE_RADII,
  NOTE_SHADOWS,
  PAGE_WIDTHS,
  type CanvasScheme,
  type CreateMode,
  type FontFamily,
  type NoteBackground,
  type NoteBorderStyle,
  type NoteRadius,
  type NoteShadow,
  type PageWidth,
} from '@/workbench/editor/prefs'
import { resolveDefaultAgent } from '@/workbench/ai/prefs'

type TabId = 'editor' | 'ai'

const TABS: readonly { id: TabId; labelKey: string }[] = [
  { id: 'editor', labelKey: 'workbench.settings.tab.editor' },
  { id: 'ai', labelKey: 'workbench.settings.tab.ai' },
]

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const { editorPrefs, aiPrefs, updateEditorPrefs, resetEditorPrefs, updateAIPrefs, resetAIPrefs } =
  useWorkbenchPrefs()

/** tab 缺失或非法时回退 editor（§9.14.1）。 */
function parseTab(raw: unknown): TabId {
  return raw === 'ai' ? 'ai' : 'editor'
}

const activeTab = ref<TabId>(parseTab(route.query.tab))

/** 只改 ?tab=，不堆历史；刷新与书签都按 query 恢复。 */
function switchTab(tab: TabId) {
  if (activeTab.value === tab) return
  activeTab.value = tab
  void router.replace({ query: { ...route.query, tab } })
}

// ---- 智能体列表 ----

interface AgentCard {
  id: string
  name: string
  description?: string
  model?: string
  builtin: boolean
  ready: boolean
}

const agentsLoading = ref(true)
const agentCards = ref<AgentCard[]>([])

/**
 * 拉取可选智能体。
 *
 * 只取工作台需要的字段：名字、内置/我创建、模型、就绪状态与描述。
 * KB 绑定、MCP、Skills、Sandbox、IM 与 API Key 一律不展示也不参与。
 */
async function loadAgents() {
  agentsLoading.value = true
  try {
    const resp = await listAgents()
    const disabled = new Set(resp.disabled_own_agent_ids ?? [])
    const rows = (resp.data ?? []).filter((agent) => !disabled.has(agent.id))
    const cards: AgentCard[] = rows.map((agent) => ({
      id: agent.id,
      name: agent.name,
      description: agent.description,
      model: agent.config?.model_id ?? '',
      builtin: Boolean(agent.is_builtin),
      // 没有模型视为未就绪：工作台只复用模型与提示词，没模型就无法工作。
      ready: Boolean(agent.config?.model_id),
    }))
    // 内置在前，「我创建」在后（§9.14.5）。
    cards.sort((a, b) => Number(b.builtin) - Number(a.builtin))
    agentCards.value = cards

    // 已选智能体失效时自动回退并提示；一个可用项都没有时保留原选择。
    // 注意 resolveDefaultAgent 认的是 is_builtin，这里要把 builtin 映射过去。
    const resolved = resolveDefaultAgent(
      aiPrefs.value.defaultAgentId,
      cards.map((card) => ({ id: card.id, is_builtin: card.builtin, ready: card.ready })),
    )
    if (resolved && resolved !== aiPrefs.value.defaultAgentId) {
      updateAIPrefs({ defaultAgentId: resolved })
      MessagePlugin.info(t('workbench.settings.ai.fallback'))
    }
  } catch {
    MessagePlugin.error(t('workbench.settings.ai.loadFailed'))
    agentCards.value = []
  } finally {
    agentsLoading.value = false
  }
}

function goAgents() {
  void router.push({ name: 'agentList' })
}

function onResetEditor() {
  resetEditorPrefs()
  MessagePlugin.success(t('workbench.settings.resetDone'))
}

function onResetAI() {
  resetAIPrefs()
  MessagePlugin.success(t('workbench.settings.resetDone'))
}

onMounted(() => {
  void loadAgents()
})
</script>

<style lang="less" scoped>
.ws-page {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: auto;
  background: var(--td-bg-color-container);
}

.ws-head {
  display: flex;
  align-items: flex-start;
  gap: var(--app-space-3);
  padding: var(--app-space-5) var(--app-space-6) var(--app-space-3);
  border-bottom: 1px solid var(--td-component-stroke);
}

.ws-title {
  font-size: var(--app-text-xl);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.ws-sub {
  margin-top: var(--app-space-1);
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
}

.spacer {
  flex: 1;
}

.ws-tabs {
  display: flex;
  gap: var(--app-space-1);

  button {
    padding: var(--app-space-1) var(--app-space-3);
    border-radius: var(--app-radius-xs);
    font-size: var(--app-text-sm);
    color: var(--td-text-color-secondary);

    &.is-active {
      background: var(--td-brand-color-1);
      color: var(--td-brand-color);
    }
  }
}

.ws-body {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  gap: var(--app-space-3);
  padding: var(--app-space-4) var(--app-space-6) var(--app-space-6);
  max-width: 880px;
}

.ws-card {
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
}

.ws-field {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--app-space-2);
  padding: var(--app-space-2) 0;

  & + .ws-field {
    border-top: 1px solid var(--td-component-stroke);
  }
}

.ws-label {
  flex: 0 0 190px;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-primary);
}

.ws-select {
  width: 180px;
}

.ws-slider {
  width: 240px;
}

.ws-value {
  min-width: 42px;
  font-size: var(--app-text-xs);
  color: var(--td-text-color-secondary);
  font-variant-numeric: tabular-nums;
}

.ws-hint {
  flex-basis: 100%;
  margin: 0 0 0 190px;
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}

.ws-actions {
  display: flex;
  gap: var(--app-space-2);
}

.ws-foot {
  margin: 0;
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}

.ws-loading {
  display: grid;
  place-items: center;
  padding: var(--app-space-6);
}

.ws-empty {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--app-space-2);
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);

  p {
    margin: 0;
  }
}

.ws-agents {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(240px, 1fr));
  gap: var(--app-space-2);
}

.ws-agent {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
  padding: var(--app-space-2) var(--app-space-3);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  text-align: left;

  &:hover:not(:disabled) {
    border-color: var(--td-brand-color-3);
  }

  &.is-active {
    border-color: var(--td-brand-color);
    background: var(--td-brand-color-1);
  }

  &.is-disabled {
    opacity: 0.6;
  }
}

.ws-agent-name {
  font-size: var(--app-text-sm);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.ws-agent-meta {
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}

.ws-agent-desc {
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-secondary);
}

.ws-agent-warn {
  font-size: var(--app-text-2xs);
  color: var(--td-warning-color);
}

.ws-scope {
  display: flex;
  align-items: center;
  gap: var(--app-space-1);
  margin-top: var(--app-space-2);
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}
</style>
