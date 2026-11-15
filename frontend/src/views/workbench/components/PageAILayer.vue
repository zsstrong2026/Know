<template>
  <!-- Page AI 层：浮动工具条 + 五组菜单 + 结果预览 + 右侧 AI 侧栏（§9.15） -->
  <div class="page-ai" :class="{ 'is-collapsed': !panelOpen, 'is-docked': Boolean(panelTarget) }">
    <!-- 常驻 AI 入口：无选区也能打开侧栏（改写/问答都从这里进）。
         嵌入到自带右侧面板的页面（如 Journals）时可关闭，由外层标签切换。 -->
    <button
      v-if="editable && !panelOpen && showLauncher !== false"
      class="pai-launcher"
      type="button"
      :aria-label="t('workbench.pageAi.bar.open')"
      :title="t('workbench.pageAi.bar.open')"
      @click="panelOpen = true"
    >
      <t-icon name="star" size="14px" />
      <span>{{ t('workbench.pageAi.bar.ai') }}</span>
    </button>

    <!-- 浮动工具条：选区非空且位于可编辑文本块时出现 -->
    <div v-if="barVisible" class="pai-bar" role="toolbar" :aria-label="t('workbench.pageAi.bar.ai')">
      <span class="pai-brand"><t-icon name="star" size="11px" />{{ t('workbench.pageAi.bar.ai') }}</span>
      <span class="pai-sep" />
      <button type="button" :disabled="codeOnlyActive" @click="quickAction('edit.polish')">
        {{ t('workbench.pageAi.action.polish') }}
      </button>
      <button type="button" :disabled="codeOnlyActive" @click="quickAction('edit.humanize')">
        {{ t('workbench.pageAi.action.humanize') }}
      </button>
      <button type="button" :disabled="codeOnlyActive" @click="quickTranslate">
        {{ t('workbench.pageAi.action.translate', { value: translateLangName(lastLang) }) }}
      </button>
      <button ref="moreBtnEl" type="button" :aria-expanded="menuOpen" @click="toggleMenu">
        {{ t('workbench.pageAi.bar.more') }}
      </button>
      <span class="pai-sep" />
      <button type="button" @click="openPanel">{{ t('workbench.pageAi.bar.ask') }}</button>
      <span v-if="tooLong" class="pai-warn">{{ t('workbench.pageAi.state.tooLong') }}</span>
    </div>

    <PageAIMenu
      v-if="menuOpen"
      ref="menuEl"
      :code-only="codeOnlyActive"
      :last-lang="lastLang"
      class="pai-menu-host"
      @select="onMenuSelect"
      @close="menuOpen = false"
    />
    <PageAIResult
      v-if="visible && !silentApply"
      :text="text"
      :mode="mode"
      :title="title"
      :streaming="streaming"
      :candidates="candidates"
      :active-index="activeIndex"
      :has-alternatives="hasAlternatives"
      :retryable="retryable"
      :issues="issues"
      :rejected-issues="rejectedIssues"
      :is-proofread="activeAction === 'proofread'"
      :original-text="originalText"
      class="pai-result-host"
      @apply="onApply"
      @retry="retry"
      @cancel="cancel"
      @alternatives="alternatives"
      @select-candidate="selectCandidate"
      @toggle-issue="toggleIssue"
    />

    <!--
      右侧 AI 侧栏：斜杠命令 + 内联命令共用同一条流式管线。
      传入 panelTarget 时用 Teleport 停靠到页面自己的右侧栏（例如 Journals），
      浮动工具条与结果预览仍留在编辑区内。
    -->
    <Teleport :to="panelTarget || 'body'" :disabled="!panelTarget">
    <aside
      v-if="panelOpen"
      class="pai-panel"
      :class="{ 'is-docked': Boolean(panelTarget) }"
      :aria-label="t('workbench.pageAi.panel.title')"
    >
      <!-- 停靠模式下由外层标签负责切换，标题与关闭按钮都是冗余信息，不再展示 -->
      <header v-if="!panelTarget" class="pp-head">
        <span class="pp-title">{{ t('workbench.pageAi.panel.title') }}</span>
        <span class="spacer" />
        <button type="button" :aria-label="t('workbench.pageAi.panel.close')" @click="panelOpen = false">
          <t-icon name="close" size="13px" />
        </button>
      </header>

      <!-- 智能体选择：与工作台设置里的默认智能体共用同一份偏好 -->
      <div class="pp-agent">
        <t-select
          :value="activeAgentId"
          size="small"
          class="pp-agent-select"
          :loading="agentsLoading"
          :placeholder="t('workbench.pageAi.panel.agentPlaceholder')"
          @change="onAgentChange"
        >
          <t-option
            v-for="agent in agentOptions"
            :key="agent.id"
            :value="agent.id"
            :label="agent.label"
          />
        </t-select>
      </div>

      <div class="pp-quick">
        <button v-for="cmd in quickCommands" :key="cmd.id" type="button" @click="runSlash(cmd.id)">
          {{ t(cmd.labelKey) }}
        </button>
      </div>

      <!-- 对话流：用户提问与 AI 回答都留在侧栏内，回答按 Markdown 渲染 -->
      <div ref="chatScrollEl" class="pp-chat" aria-live="polite">
        <p v-if="!chats.length" class="pp-chat-empty">{{ t('workbench.pageAi.panel.empty') }}</p>
        <div
          v-for="chat in chats"
          :key="chat.id"
          class="pp-msg"
          :class="chat.role === 'user' ? 'is-user' : 'is-assistant'"
        >
          <div v-if="chat.role === 'assistant'" class="pp-msg-role">
            <t-icon name="star" size="11px" />
            {{ t('workbench.pageAi.panel.title') }}
          </div>
          <div class="pp-msg-body">
            <AiMarkdown v-if="chat.role === 'assistant' && chat.text" :content="chat.text" />
            <span v-else-if="chat.role === 'assistant'" class="pp-msg-pending">
              {{ t('workbench.pageAi.state.generating') }}
            </span>
            <p v-else class="pp-msg-plain">{{ chat.text }}</p>
          </div>

          <!--
            生成结果的操作直接贴在回答下方：替换选中 / 插入下方 / 复制。
            不再提供「应用到文档」入口，也不再走编辑区里的预览弹窗。
          -->
          <div
            v-if="chat.role === 'assistant' && chat.text && !chat.streaming && !props.codeOnly"
            class="pp-actions"
          >
            <button
              type="button"
              class="pp-action is-primary"
              :disabled="!hasSelection"
              @click="applyChatToDoc(chat, 'replace')"
            >
              {{ t('workbench.pageAi.preview.replace') }}
            </button>
            <button type="button" class="pp-action" @click="applyChatToDoc(chat, 'below')">
              {{ t('workbench.pageAi.preview.insertBelow') }}
            </button>
            <button type="button" class="pp-action" @click="copyChat(chat)">
              {{ copiedChatId === chat.id ? t('workbench.pageAi.preview.copyDone') : t('workbench.pageAi.preview.copy') }}
            </button>
          </div>

          <!-- 检索来源：只有问答车道会带 -->
          <div v-if="chat.references?.length" class="pp-refs">
            <span class="pp-refs-title">{{ t('workbench.pageAi.slash.askKb') }}</span>
            <span v-for="ref in chat.references" :key="ref.knowledge_id" class="pp-ref">
              {{ ref.knowledge_title || ref.knowledge_id }}
            </span>
          </div>
        </div>
      </div>

      <div class="pp-input-wrap">
        <!-- 斜杠菜单：输入框为空或以 / 开头时出现（句中 / 不触发） -->
        <div v-if="slashOpen" class="pp-slash" role="menu" :aria-label="t('workbench.pageAi.slash.title')">
          <div class="pp-slash-head">{{ t('workbench.pageAi.slash.title') }}</div>
          <button
            v-for="(cmd, index) in filteredCommands"
            :key="cmd.id"
            type="button"
            role="menuitem"
            :class="{ 'is-active': index === slashIndex }"
            :disabled="cmd.needsSelection && !hasSelection"
            @click="runSlash(cmd.id)"
          >
            <b>{{ t(cmd.labelKey) }}</b>
            <span>{{ t(cmd.hintKey) }}</span>
          </button>
          <div v-if="filteredCommands.length === 0" class="pp-slash-empty">
            {{ t('workbench.pageAi.slash.empty') }}
          </div>
          <div class="pp-slash-foot">{{ t('workbench.pageAi.slash.foot') }}</div>
        </div>

        <textarea
          v-model="draft"
          class="pp-input"
          rows="3"
          :placeholder="t('workbench.pageAi.panel.placeholder')"
          @keydown="onInputKeydown"
          @input="onInput"
        />
        <div class="pp-foot">
          <span v-if="targetLang" class="pp-chip">
            {{ t('workbench.pageAi.slash.targetLang', { lang: translateLangName(targetLang) }) }}
          </span>
          <span class="spacer" />
          <button type="button" class="pp-send" :disabled="!draft.trim()" @click="sendFreeText">
            {{ t('workbench.pageAi.panel.send') }}
          </button>
        </div>
        <p class="pp-disclaimer">{{ t('workbench.pageAi.panel.disclaimer') }}</p>
      </div>
    </aside>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
/**
 * Page AI 的视图层。
 *
 * 责任边界：
 *   - 本组件只做「展示 + 把用户意图翻译成请求」；
 *   - 生成、预览、指纹校验与写入全部在 usePageAI 里；
 *   - 编辑器读写通过 createPageAIAdapter 注入，组件不直接碰 store。
 */
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import PageAIMenu from './PageAIMenu.vue'
import PageAIResult from './PageAIResult.vue'
import AiMarkdown from './AiMarkdown.vue'
import { usePageAI, type PageAIEditorAdapter } from '@/workbench/ai/use-page-ai'
import { toPlainDocumentText } from '@/workbench/ai/text-diff'
import {
  streamAskAI,
  streamInlineAI,
  WorkbenchAIError,
  type AskAIReference,
} from '@/workbench/ai/use-inline-ai'
import { WORKBENCH_ERROR } from '@/workbench/error-code'
import { listAgents, type CustomAgent } from '@/api/agent'
import { useWorkbenchPrefs } from '@/workbench/use-workbench-prefs'
import { resolveDefaultAgent } from '@/workbench/ai/prefs'
import { shouldChunk } from '@/workbench/ai/long-doc'
import {
  INLINE_AI_TEXT_LIMIT,
  type InlineAIRequest,
} from '@/workbench/ai/inline-ai-types'
import {
  PAGE_AI_ACTIONS,
  type PageAIAction,
  compilePageAIAction,
  translateLangName,
  type TranslateLang,
} from '@/workbench/ai/page-actions'

const props = defineProps<{
  /** 编辑器适配器工厂；返回 null 表示当前不可用（只读/未挂载） */
  adapter: () => PageAIEditorAdapter | null
  /** 当前条目 id */
  itemId: string
  /** 是否可编辑（回收站、只读预览不出现工具条） */
  editable: boolean
  /** 当前选区是否落在代码块内 */
  codeOnly?: boolean
  /** 默认智能体 id（来自工作台 AI 偏好） */
  agentId?: string
  /** 是否先预览再写入（confirmInsert） */
  confirmInsert?: boolean
  /** 首次渲染时侧栏是否展开；外层有面板切换器时传 false */
  initialOpen?: boolean
  /** 是否显示右侧常驻 AI 启动按钮 */
  showLauncher?: boolean
  /**
   * 侧栏的停靠容器：传入时把侧栏 Teleport 到该元素（如页面自带的右侧栏），
   * 不传则保持原来的编辑区右侧浮层行为。
   */
  panelTarget?: HTMLElement | null
}>()

const emit = defineEmits<{
  /** 侧栏开合上报：父级据此收窄编辑区，避免侧栏覆盖正文 */
  'panel-change': [open: boolean]
}>()

const { t } = useI18n()
const menuOpen = ref(false)
/** 「更多」触发按钮：点击它由 click 负责切换，不参与外部点击关闭 */
const moreBtnEl = ref<HTMLElement | null>(null)
/** 菜单实例：用于判断点击是否落在弹窗内部 */
const menuEl = ref<InstanceType<typeof PageAIMenu> | null>(null)
/** 侧栏默认展开：编辑页进入即可看到 AI 对话与改写入口 */
const panelOpen = ref(props.initialOpen !== false)
const draft = ref('')
const slashOpen = ref(false)
const slashIndex = ref(0)
const targetLang = ref<TranslateLang>('en')
/** 选区长度（用于超长提示与工具条可见性） */
const selectionLength = ref(0)
/** 选区是否在代码块内；由编辑器实际块类型决定 */
const inCodeBlock = ref(false)
/**
 * 侧栏对话的一条消息。
 *
 * 侧栏是完整的对话框：用户的输入与 AI 的回答都留在这里，
 * 只有用户点「应用到文档」才会进入编辑区的 diff 预览。
 */
interface WorkbenchChat {
  id: string
  role: 'user' | 'assistant'
  text: string
  streaming?: boolean
  /** 问答车道命中的工作知识库来源 */
  references?: AskAIReference[]
}

const chats = ref<WorkbenchChat[]>([])
const chatScrollEl = ref<HTMLElement | null>(null)
/** 对话结果直接写入期间置 true：预览卡片不再出现，避免「应用到文档」弹窗。 */
const silentApply = ref(false)
/** 刚复制过的消息 id，用于「复制 → 已复制」的短暂反馈。 */
const copiedChatId = ref('')
/** 当前对话请求；新提问会中止上一条，避免两段回答交错。 */
let chatController: AbortController | null = null
let chatSeq = 0

/** 追加一条消息并滚到底部。 */
function pushChat(
  role: WorkbenchChat['role'],
  text: string,
  extra: Partial<WorkbenchChat> = {},
): WorkbenchChat {
  chatSeq += 1
  // 用 reactive 创建：流式更新 text 时只重渲染这一条，不用整列表替换。
  const chat = reactive<WorkbenchChat>({ id: `chat-${chatSeq}`, role, text, ...extra })
  chats.value.push(chat)
  // 只保留最近 100 条：侧栏对话会跨条目累积，不裁剪会让 DOM 与内存无限增长。
  if (chats.value.length > 100) chats.value.splice(0, chats.value.length - 100)
  scrollChat()
  return chat
}

/** 滚到底部：新消息与流式增量都要跟住。 */
function scrollChat() {
  void nextTick(() => {
    const el = chatScrollEl.value
    if (el) el.scrollTop = el.scrollHeight
  })
}

const hasSelection = computed(() => selectionLength.value > 0)
const tooLong = computed(() => selectionLength.value > INLINE_AI_TEXT_LIMIT)
const barVisible = computed(() => props.editable && hasSelection.value)
/** 代码块判定以编辑器实况为准，props.codeOnly 只是补充信号。 */
const codeOnlyActive = computed(() => inCodeBlock.value || Boolean(props.codeOnly))

// ---- 智能体选择（与设置页共用同一份 aiPrefs） ----
const { aiPrefs, updateAIPrefs } = useWorkbenchPrefs()
const agents = ref<CustomAgent[]>([])
const agentsLoading = ref(false)
/**
 * 侧栏当前使用的智能体：优先取本条偏好的 defaultAgentId；
 * props.agentId 只是兜底，保证调用方显式指定时不被打回默认。
 */
const activeAgentId = computed(() => {
  const selected = aiPrefs.value.defaultAgentId || props.agentId || ''
  return resolveDefaultAgent(
    selected,
    agents.value.map((agent) => ({ id: agent.id, is_builtin: agent.is_builtin })),
  )
})

/** 下拉选项：内置在前、其余按后端顺序；空 id 表示跟随租户默认模型。 */
const agentOptions = computed(() => [
  { id: '', label: t('workbench.pageAi.panel.agentDefault') },
  ...agents.value.map((agent) => ({
    id: agent.id,
    label: agent.is_builtin
      ? t('workbench.pageAi.panel.agentBuiltin', { name: agent.name })
      : agent.name,
  })),
])

async function loadAgents() {
  if (agents.value.length > 0 || agentsLoading.value) return
  agentsLoading.value = true
  try {
    const response = await listAgents()
    agents.value = response?.data ?? []
  } catch {
    // 智能体列表失败只影响下拉，问答仍可走租户默认模型。
    agents.value = []
  } finally {
    agentsLoading.value = false
  }
}

/** 切换智能体：写入偏好，后续内联与问答都用它。 */
function onAgentChange(value: string | number | boolean) {
  updateAIPrefs({ defaultAgentId: String(value ?? '') })
}

const ai = usePageAI({
  editor: () => props.adapter(),
  itemId: () => props.itemId,
  agentId: () => activeAgentId.value || props.agentId || '',
  confirmInsert: () => props.confirmInsert ?? true,
  onError: (err) => {
    // 只提示类型，不显示正文（§9.15.5）。
    const key =
      err.kind === 'too-long'
        ? 'workbench.pageAi.state.tooLong'
        : err.kind === 'no-model'
          ? 'workbench.pageAi.state.noModel'
          : err.kind === 'stream-broken'
            ? 'workbench.pageAi.state.streamBroken'
            : err.kind === 'not-found'
              ? 'workbench.pageAi.state.blockMissing'
              : 'workbench.pageAi.state.streamBroken'
    MessagePlugin.warning(t(key))
  },
})

const {
  text,
  title,
  mode,
  streaming,
  visible,
  lastLang,
  candidates,
  activeIndex,
  hasAlternatives,
  retryable,
  issues,
  rejectedIssues,
  activeAction,
  originalText,
  run,
  apply,
  retry,
  cancel,
  alternatives,
  selectCandidate,
  toggleIssue,
  adopt,
} = ai

/** 斜杠命令表：前四个是内联车道，最后一个是问答车道（§9.15.4）。 */
const SLASH_COMMANDS = [
  { id: 'summarize', labelKey: 'workbench.pageAi.slash.summarize', hintKey: 'workbench.pageAi.slash.summarizeHint', action: 'summarize', needsSelection: false },
  { id: 'outline', labelKey: 'workbench.pageAi.slash.outline', hintKey: 'workbench.pageAi.slash.outlineHint', action: 'outline', needsSelection: false },
  { id: 'extractActions', labelKey: 'workbench.pageAi.slash.extractActions', hintKey: 'workbench.pageAi.slash.extractActionsHint', action: 'custom', needsSelection: false },
  { id: 'translate', labelKey: 'workbench.pageAi.slash.translate', hintKey: 'workbench.pageAi.slash.translateHint', action: 'translate', needsSelection: false },
  { id: 'explain', labelKey: 'workbench.pageAi.slash.explain', hintKey: 'workbench.pageAi.slash.explainHint', action: 'custom', needsSelection: true },
  { id: 'askKb', labelKey: 'workbench.pageAi.slash.askKb', hintKey: 'workbench.pageAi.slash.askKbHint', action: null, needsSelection: false },
  { id: 'polish', labelKey: 'workbench.pageAi.slash.rewrite', hintKey: 'workbench.pageAi.slash.polishHint', action: 'polish', needsSelection: false },
] as const

/** 侧栏快捷 chips：四个常用内联命令 + 「改进写作」改写入口，与斜杠共用处理。 */
const quickCommands = computed(() =>
  SLASH_COMMANDS.filter((cmd) => ['summarize', 'outline', 'extractActions', 'translate', 'polish'].includes(cmd.id)),
)

const filteredCommands = computed(() => {
  const keyword = draft.value.replace(/^\//, '').trim()
  if (!keyword) return SLASH_COMMANDS
  return SLASH_COMMANDS.filter((cmd) => t(cmd.labelKey).includes(keyword))
})

/** 采样选区长度：工具条显隐与超长提示都依赖它。 */
function refreshSelection() {
  const adapter = props.adapter()
  const snapshot = adapter?.getSelection() ?? null
  selectionLength.value = snapshot?.text.length ?? 0
  // 代码块内只保留「解释选中」（§9.15.1），由编辑器实际位置决定，不靠 props 猜。
  inCodeBlock.value = Boolean(snapshot && adapter?.isCodeBlock?.())
}

/**
 * 监听选区变化。
 *
 * 用 document 的 selectionchange 而不是 store 的 selection slot：后者只在
 * 编辑器内部触发，键盘选词、拖选跨过宿主边界时都会漏。这里统一在事件循环
 * 末尾采样一次，避免和 BlockSuite 自己的 selection 更新抢时序。
 */
function onDocumentSelectionChange() {
  window.setTimeout(refreshSelection, 0)
}

onMounted(() => {
  document.addEventListener('selectionchange', onDocumentSelectionChange)
  refreshSelection()
  // 侧栏默认展开，进入编辑页即拉一次智能体列表供下拉使用。
  void loadAgents()
})

watch(
  () => [props.itemId, props.editable],
  () => {
    // 切条目一律中止未完成请求并丢弃预览（§9.15.1）。
    cancel()
    menuOpen.value = false
    refreshSelection()
  },
)

function toggleMenu() {
  menuOpen.value = !menuOpen.value
  if (menuOpen.value) slashOpen.value = false
}

/**
 * 点击页面其他位置（编辑器空白、侧栏等）时收起「更多」弹窗。
 *
 * 用捕获阶段监听：编辑器内部的 pointerdown 可能 stopPropagation，
 * 冒泡阶段会漏掉点击空白的情况。菜单内部与触发按钮自身不关闭。
 */
function onDocumentPointerDown(event: PointerEvent) {
  const target = event.target as Node | null
  if (!target) return
  if (menuEl.value?.$el?.contains(target)) return
  if (moreBtnEl.value?.contains(target)) return
  menuOpen.value = false
}

watch(menuOpen, (open) => {
  if (open) document.addEventListener('pointerdown', onDocumentPointerDown, true)
  else document.removeEventListener('pointerdown', onDocumentPointerDown, true)
})

/** 工具条直达动作：子菜单型动作（如翻译）可直接带上默认子项，跳过展开菜单。 */
function quickAction(id: string, submenuId?: string) {
  // 代码块内快捷动作同样被禁用，避免绕过菜单的可用性规则（§9.15.1）。
  if (codeOnlyActive.value) {
    MessagePlugin.info(t('workbench.pageAi.state.codeBlockHint'))
    return
  }
  const action = PAGE_AI_ACTIONS.find((item) => item.id === id)
  if (action) void onMenuSelect(action, submenuId)
}

/** 「翻译为」直达：按上次选择的语言生成子项 id，否则用默认语言。 */
function quickTranslate() {
  quickAction('edit.translate', `edit.translate.${lastLang.value}`)
}

/** 菜单选中：编译成请求参数后立刻发起（子菜单项带 submenuId）。 */
async function onMenuSelect(action: PageAIAction, submenuId?: string) {
  menuOpen.value = false
  const compiled = compilePageAIAction(action, submenuId)
  if (!compiled) return
  const snapshot = props.adapter()?.getSelection() ?? null
  if (!snapshot) {
    MessagePlugin.warning(t('workbench.pageAi.state.needSelection'))
    return
  }
  if (compiled.targetLang) {
    lastLang.value = compiled.targetLang
    targetLang.value = compiled.targetLang
  }
  const request: InlineAIRequest = {
    action: compiled.action,
    text: snapshot.text,
    instruction: compiled.instruction,
    target_lang: compiled.targetLang,
    writeMode: compiled.write,
  }
  await run(request, labelOf(compiled))
}

function labelOf(compiled: ReturnType<typeof compilePageAIAction> & object): string {
  return compiled.labelArgs
    ? t(compiled.labelKey, compiled.labelArgs)
    : t(compiled.labelKey)
}

async function onApply(writeMode: 'replace' | 'below') {
  const ok = await apply(writeMode)
  if (ok) MessagePlugin.success(t('workbench.pageAi.state.applied'))
}

/** 侧栏输入：只在为空或以 / 开头时打开命令菜单（句中 / 不触发）。 */
function onInput() {
  const value = draft.value
  slashOpen.value = value.startsWith('/') && !value.includes(' ')
  slashIndex.value = 0
}

function onInputKeydown(event: KeyboardEvent) {
  if (slashOpen.value && filteredCommands.value.length > 0) {
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      slashIndex.value = (slashIndex.value + 1) % filteredCommands.value.length
      return
    }
    if (event.key === 'ArrowUp') {
      event.preventDefault()
      const size = filteredCommands.value.length
      slashIndex.value = (slashIndex.value - 1 + size) % size
      return
    }
    if (event.key === 'Enter') {
      event.preventDefault()
      const cmd = filteredCommands.value[slashIndex.value]
      if (cmd) void runSlash(cmd.id)
      return
    }
    if (event.key === 'Escape') {
      event.preventDefault()
      slashOpen.value = false
      return
    }
  }
  // ⌘/Ctrl + Enter 直接发送（与聊天输入框一致）。
  if (event.key === 'Enter' && (event.metaKey || event.ctrlKey)) {
    event.preventDefault()
    void sendFreeText()
  }
}

/**
 * 执行斜杠命令。
 *
 * 页面级命令把当前页纯文本作为 text（不是 context_text），仍受 8000 字上限。
 */
async function runSlash(id: string) {
  const cmd = SLASH_COMMANDS.find((item) => item.id === id)
  if (!cmd) return
  slashOpen.value = false
  draft.value = ''

  if (cmd.id === 'askKb') {
    // 问答车道：只查工作 KB，结果只进对话，不提供插入（§9.15.4）。
    await runAsk(pageTextOrSelection())
    return
  }

  const snapshot = props.adapter()?.getSelection() ?? null
  const text = pageTextOrSelection()
  if (!text.trim()) {
    MessagePlugin.warning(t('workbench.pageAi.state.needSelection'))
    return
  }
  if (cmd.needsSelection && !snapshot) {
    MessagePlugin.warning(t('workbench.pageAi.state.needSelection'))
    return
  }
  // 总结/大纲支持分块，超长不拦；其余动作仍按单次上限提示分段。
  if (text.length > INLINE_AI_TEXT_LIMIT && !shouldChunk(cmd.action ?? '', text)) {
    MessagePlugin.warning(t('workbench.pageAi.state.tooLong'))
    return
  }

  const request: InlineAIRequest = {
    action: cmd.action as InlineAIRequest['action'],
    text,
    writeMode: cmd.id === 'explain' ? 'aside' : 'below',
    target_lang: cmd.id === 'translate' ? targetLang.value : undefined,
  }
  await run(request, t(cmd.labelKey))
}

/** 有选区优先用选区，否则整页纯文本（§9.15.4）。 */
function pageTextOrSelection(): string {
  const snapshot = props.adapter()?.getSelection() ?? null
  return snapshot?.text || (props.adapter()?.getPageText() ?? '')
}

/** 问答车道：检索工作知识库后回答，来源随消息一起展示。 */
async function runAsk(question: string) {
  if (!question.trim()) {
    MessagePlugin.warning(t('workbench.pageAi.state.needSelection'))
    return
  }
  panelOpen.value = true
  chatController?.abort()
  pushChat('user', question)
  const reply = pushChat('assistant', '', { streaming: true, references: [] })

  chatController = new AbortController()
  const signal = chatController.signal
  try {
    await streamAskAI(
      {
        question: question.slice(0, INLINE_AI_TEXT_LIMIT),
        item_id: props.itemId,
        agent_id: activeAgentId.value || props.agentId,
      },
      {
        onDelta: (delta) => {
          reply.text += delta
          scrollChat()
        },
        onReferences: (refs) => {
          reply.references = refs
        },
      },
      signal,
    )
  } catch (err) {
    if (!isAbortError(err)) {
      MessagePlugin.warning(
        err instanceof WorkbenchAIError && err.code === WORKBENCH_ERROR.noModel
          ? t('workbench.pageAi.state.noModel')
          : t('workbench.pageAi.panel.askFailed'),
      )
    }
  } finally {
    reply.streaming = false
    if (chatController?.signal === signal) chatController = null
    scrollChat()
  }
}

/**
 * 把当前问答结果直接写入文档。
 *
 * 与内联动作不同，侧栏对话不再经过预览弹窗：
 *   - 先整理成纯文本（去 Markdown 标记、去说明/分析段落），只写创作内容本身；
 *   - adopt 只用来登记一次待写入内容，随后立刻 apply；期间 silentApply 保证结果卡片不出现。
 * 写入仍然走指纹校验，选区变了就只提示、不落笔。
 */
async function applyChatToDoc(chat: WorkbenchChat, writeMode: 'replace' | 'below') {
  const content = toPlainDocumentText(chat.text)
  if (!content) return
  if (writeMode === 'replace' && !hasSelection.value) {
    MessagePlugin.warning(t('workbench.pageAi.state.needSelection'))
    return
  }

  silentApply.value = true
  try {
    const ok = adopt(content, t('workbench.pageAi.slash.applyToDoc'), writeMode, true)
    if (!ok) {
      MessagePlugin.warning(t('workbench.pageAi.state.needSelection'))
      return
    }
    const applied = await apply(writeMode)
    if (applied) MessagePlugin.success(t('workbench.pageAi.state.applied'))
  } finally {
    // 无论成功失败都清掉预览状态：侧栏对话不保留编辑区浮层。
    cancel()
    silentApply.value = false
  }
}

/** 复制对话结果为纯文本（与预览卡片同口径，不渲染 Markdown）。 */
async function copyChat(chat: WorkbenchChat) {
  if (!chat.text) return
  try {
    await navigator.clipboard.writeText(chat.text)
    copiedChatId.value = chat.id
    window.setTimeout(() => {
      if (copiedChatId.value === chat.id) copiedChatId.value = ''
    }, 1500)
  } catch {
    // 剪贴板不可用时静默失败，不打断写作。
  }
}

/** 中止判定：用户主动打断不弹错误提示。 */
function isAbortError(err: unknown): boolean {
  return err instanceof DOMException && err.name === 'AbortError'
}

/** 自由输入 = 对话：用户提问与回答都留在侧栏，不直接写文档。 */
async function sendFreeText() {
  const instruction = draft.value.trim()
  if (!instruction) return
  const source = pageTextOrSelection()
  if (!source.trim()) {
    MessagePlugin.warning(t('workbench.pageAi.state.needSelection'))
    return
  }
  if (source.length > INLINE_AI_TEXT_LIMIT) {
    MessagePlugin.warning(t('workbench.pageAi.state.tooLong'))
    return
  }
  draft.value = ''
  slashOpen.value = false
  await runChat(instruction, source)
}

/** 发起一轮对话：用户消息先落位，回答按增量写入同一条助手消息。 */
async function runChat(instruction: string, source: string) {
  chatController?.abort()
  pushChat('user', instruction)
  const reply = pushChat('assistant', '', { streaming: true })

  chatController = new AbortController()
  const signal = chatController.signal
  /** 流式增量先攒着，80ms 刷一次：Markdown 重解析不必每帧都做。 */
  let pending = ''
  let flushTimer: number | null = null
  const flush = () => {
    if (flushTimer !== null) {
      window.clearTimeout(flushTimer)
      flushTimer = null
    }
    if (!pending) return
    reply.text += pending
    pending = ''
    scrollChat()
  }

  try {
    await streamInlineAI(
      {
        action: 'custom',
        text: source,
        instruction,
        item_id: props.itemId,
        agent_id: activeAgentId.value || props.agentId,
        stream: true,
      },
      (delta) => {
        pending += delta
        if (flushTimer === null) flushTimer = window.setTimeout(flush, 80)
      },
      signal,
    )
    flush()
  } catch (err) {
    flush()
    if (!isAbortError(err)) {
      reply.text ||= t('workbench.pageAi.panel.askFailed')
    }
  } finally {
    reply.streaming = false
    if (chatController?.signal === signal) chatController = null
    scrollChat()
  }
}

function openPanel() {
  panelOpen.value = !panelOpen.value
}

onBeforeUnmount(() => {
  document.removeEventListener('selectionchange', onDocumentSelectionChange)
  document.removeEventListener('pointerdown', onDocumentPointerDown, true)
  chatController?.abort()
  ai.abort()
})

/** 侧栏开合只上报布局；对话记录保留，关掉再打开能看到之前的提问与回答。 */
watch(panelOpen, (open) => {
  emit('panel-change', open)
  if (!open) {
    slashOpen.value = false
    // 收起侧栏时中止进行中的回答，避免用户看不到却继续烧 token。
    chatController?.abort()
  }
}, { immediate: true })

defineExpose({
  refreshSelection,
  /** 供外层右侧面板切换器控制开合 */
  openPanel: () => {
    panelOpen.value = true
  },
  closePanel: () => {
    panelOpen.value = false
  },
})
</script>

<style lang="less" scoped>
.page-ai {
  position: absolute;
  inset: 0;
  display: flex;
  /* 侧栏固定贴右：与 .editor-mount 让出的 right:340px 对应，避免覆盖正文 */
  justify-content: flex-end;
  pointer-events: none;

  > * {
    pointer-events: auto;
  }
}

.pai-bar {
  position: absolute;
  /* 让出常驻的格式工具条：编辑器已按该变量下移，AI 条跟着下移同样的距离 */
  top: calc(var(--workbench-format-bar-height, 0px) + var(--app-space-3) + 34px);
  left: 50%;
  transform: translateX(-50%);
  display: flex;
  align-items: center;
  gap: var(--app-space-1);
  padding: var(--app-space-1);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-container);
  box-shadow: var(--td-shadow-2);
  font-size: var(--app-text-2xs);
  /* 不参与拖放：避免用户从工具条上起拖时把按钮拖进编辑器 */
  user-select: none;

  button {
    padding: var(--app-space-1) var(--app-space-2);
    border-radius: var(--app-radius-xs);
    color: var(--td-text-color-primary);

    &:hover:not(:disabled) {
      background: var(--td-brand-color-1);
      color: var(--td-brand-color);
    }

    &:disabled {
      color: var(--td-text-color-disabled);
    }
  }
}

.pai-brand {
  display: flex;
  align-items: center;
  gap: 3px;
  padding: 0 var(--app-space-1);
  color: var(--td-brand-color);
}

.pai-sep {
  width: 1px;
  height: 14px;
  background: var(--td-component-stroke);
}

.pai-warn {
  margin-left: var(--app-space-2);
  color: var(--td-warning-color);
}

.pai-menu-host {
  position: absolute;
  /* 让出常驻格式条与 AI 条，菜单排到它们下面 */
  top: calc(var(--workbench-format-bar-height, 0px) + var(--app-space-3) + 112px);
  left: 50%;
  transform: translateX(-50%);
  z-index: 20;
}

.pai-result-host {
  position: absolute;
  right: var(--app-space-4);
  bottom: var(--app-space-4);
  z-index: 20;
}

/* 侧栏展开时，预览卡片让出右栏宽度：320px 的卡片贴在 right:16px 上会正好
   压住 AI 输入区，导致输入框点不到。这里把它移到编辑区右下角。
   停靠模式下侧栏不在编辑区内，预览卡片不需要再让位。 */
.page-ai:not(.is-collapsed):not(.is-docked) .pai-result-host {
  right: calc(var(--workbench-ai-panel-width, 340px) + var(--app-space-4));
}

.pai-panel {
  /* 宽度含左侧 1px 分隔线：编辑区让出同一数值即可完全不相交 */
  box-sizing: border-box;
  width: var(--workbench-ai-panel-width, 340px);
  flex: none;
  display: flex;
  flex-direction: column;
  /* 侧栏压在工具条/菜单/预览卡片之上：输入区任何时候都要能点中 */
  position: relative;
  z-index: 30;
  border-left: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-container);
}

/* 停靠到页面右侧栏时铺满容器，分隔线由外层栏提供 */
.pai-panel.is-docked {
  width: 100%;
  height: 100%;
  border-left: 0;
}

/* 常驻 AI 启动按钮：侧栏收起时贴在编辑区右侧，任何时刻都能唤起 AI */
.pai-launcher {
  position: absolute;
  top: var(--app-space-3);
  right: var(--app-space-4);
  z-index: 5;
  display: inline-flex;
  align-items: center;
  gap: 5px;
  height: 30px;
  padding: 0 var(--app-space-3);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-pill);
  background: var(--td-bg-color-container);
  box-shadow: var(--td-shadow-1);
  font-size: var(--app-text-xs);
  color: var(--td-text-color-secondary);
  cursor: pointer;
  transition: color var(--app-motion-fast) ease, border-color var(--app-motion-fast) ease;

  &:hover {
    border-color: var(--td-brand-color);
    color: var(--td-brand-color);
  }
}

.pp-agent {
  padding: var(--app-space-2) var(--app-space-2) 0;
}

.pp-agent-select {
  width: 100%;
}

.pp-head {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  padding: var(--app-space-2);
  border-bottom: 1px solid var(--td-component-stroke);

  button {
    display: grid;
    place-items: center;
    width: 22px;
    height: 22px;
    border-radius: var(--app-radius-xs);
    color: var(--td-text-color-placeholder);

    &:hover {
      background: var(--td-bg-color-container-hover);
    }
  }
}

.pp-title {
  font-size: var(--app-text-sm);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.pp-quick {
  display: flex;
  flex-wrap: wrap;
  gap: var(--app-space-1);
  padding: var(--app-space-2);
  border-bottom: 1px solid var(--td-component-stroke);

  button {
    padding: 2px var(--app-space-2);
    border: 1px solid var(--td-component-stroke);
    border-radius: var(--app-radius-pill);
    font-size: var(--app-text-2xs);
    color: var(--td-text-color-secondary);

    &:hover {
      border-color: var(--td-brand-color);
      color: var(--td-brand-color);
    }
  }
}

.pp-input-wrap {
  position: relative;
  margin-top: auto;
  padding: var(--app-space-2);
  border-top: 1px solid var(--td-component-stroke);
}

.pp-chat {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: var(--app-space-2);
  border-bottom: 1px solid var(--td-component-stroke);
  display: flex;
  flex-direction: column;
  gap: var(--app-space-3);
}

.pp-chat-empty {
  margin: 0;
  padding: var(--app-space-4) 0;
  text-align: center;
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}

.pp-msg {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-1);

  &.is-user {
    align-items: flex-end;
  }
}

.pp-msg-role {
  display: flex;
  align-items: center;
  gap: 3px;
  font-size: var(--app-text-2xs);
  color: var(--td-brand-color);
}

.pp-msg-body {
  max-width: 100%;
  min-width: 0;
}

/* 用户消息用气泡区分；AI 回答保持通栏，便于阅读长文与表格 */
.pp-msg.is-user .pp-msg-body {
  max-width: 88%;
  padding: var(--app-space-2);
  border-radius: var(--app-radius-md);
  background: var(--td-brand-color-1);
}

.pp-msg-plain {
  margin: 0;
  font-size: var(--app-text-sm);
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-word;
  color: var(--td-text-color-primary);
}

.pp-msg-pending {
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}

.pp-refs {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--app-space-1);
  margin-top: var(--app-space-2);
}

.pp-refs-title {
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}

.pp-ref {
  padding: 1px var(--app-space-2);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-pill);
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-secondary);
}

/* 回答下方的写入动作：一排小按钮，紧贴生成内容 */
.pp-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--app-space-1);
  margin-top: var(--app-space-2);
}

.pp-action {
  padding: 2px var(--app-space-2);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-xs);
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-secondary);
  background: transparent;

  &:hover:not(:disabled) {
    color: var(--td-text-color-primary);
    background: var(--td-bg-color-container-hover);
  }

  &:disabled {
    opacity: 0.45;
    cursor: default;
  }

  /* 主操作：替换选中，与预览卡片的主题按钮同色 */
  &.is-primary {
    border-color: var(--td-brand-color);
    color: var(--td-brand-color);

    &:hover:not(:disabled) {
      color: var(--td-brand-color);
      background: var(--td-brand-color-1);
    }
  }
}

.pp-input {
  width: 100%;
  resize: none;
  padding: var(--app-space-2);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-xs);
  font-size: var(--app-text-sm);
  color: var(--td-text-color-primary);
  background: var(--td-bg-color-container);

  &:focus {
    outline: none;
    border-color: var(--td-brand-color);
  }
}

.pp-foot {
  display: flex;
  align-items: center;
  gap: var(--app-space-1);
  margin-top: var(--app-space-1);
}

.pp-chip {
  padding: 1px var(--app-space-2);
  border-radius: var(--app-radius-pill);
  background: var(--td-brand-color-1);
  font-size: var(--app-text-2xs);
  color: var(--td-brand-color);
}

.pp-send {
  padding: 2px var(--app-space-3);
  border-radius: var(--app-radius-xs);
  background: var(--td-brand-color);
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-anti);

  &:disabled {
    opacity: 0.5;
  }
}

.pp-disclaimer {
  margin: var(--app-space-1) 0 0;
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}

.pp-slash {
  position: absolute;
  left: var(--app-space-2);
  right: var(--app-space-2);
  bottom: 96px;
  z-index: 10;
  display: flex;
  flex-direction: column;
  padding: var(--app-space-1);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-container);
  box-shadow: var(--td-shadow-2);

  button {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: 1px;
    padding: var(--app-space-1) var(--app-space-2);
    border-radius: var(--app-radius-xs);
    text-align: left;

    &:hover:not(:disabled),
    &.is-active {
      background: var(--td-brand-color-1);
    }

    &:disabled {
      opacity: 0.5;
    }

    b {
      font-size: var(--app-text-sm);
      font-weight: 500;
      color: var(--td-text-color-primary);
    }

    span {
      font-size: var(--app-text-2xs);
      color: var(--td-text-color-placeholder);
    }
  }
}

.pp-slash-head,
.pp-slash-foot {
  padding: var(--app-space-1) var(--app-space-2);
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}

.pp-slash-foot {
  border-top: 1px solid var(--td-component-stroke);
}

.pp-slash-empty {
  padding: var(--app-space-2);
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}

.spacer {
  flex: 1;
}
</style>
