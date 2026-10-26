<template>
  <!-- AI 辅助分析右栏：手动开始 → 多轮追问 → 生成方案；结论实时回传给表单 -->
  <section class="rq-chat">
    <header class="rq-chat__head">
      <span class="rq-chat__title">{{ $t('pm.requirements.ai.title') }}</span>
      <t-select
        class="rq-chat__agent"
        :value="selectedAgentId"
        :options="agentOptions"
        :placeholder="$t('pm.requirements.ai.agentDefault')"
        :loading="agentsLoading"
        :disabled="starting || isStreaming"
        clearable
        size="small"
        @change="onAgentChange"
      />
    </header>

    <!-- 方案链接：至少有一项结论时出现 -->
    <div v-if="hasPlan" class="rq-chat__plan">
      <button type="button" class="rq-chat__plan-link" @click="planVisible = true">
        {{ $t('pm.requirements.ai.planLink') }}
      </button>
    </div>

    <div ref="chatEl" class="rq-chat__body">
      <!-- 未开始：空态 + 手动开启 -->
      <div v-if="!turns.length" class="rq-chat__idle">
        <p class="rq-chat__idle-text">{{ $t('pm.requirements.ai.idle') }}</p>
        <t-button theme="primary" :loading="starting" :disabled="starting" @click="start">
          {{ $t('pm.requirements.ai.start') }}
        </t-button>
      </div>
      <template v-else>
        <div v-for="(turn, index) in renderedTurns" :key="index" class="rq-msg" :class="`is-${turn.role}`">
          <div class="rq-msg__role">{{ turn.role === 'ai' ? 'AI' : $t('pm.requirements.ai.you') }}</div>
          <!-- AI 回复按块渲染，用户输入保持纯文本 -->
          <div class="rq-msg__bubble" :class="{ 'is-blocks': turn.blocks.length }">
            <template v-if="turn.blocks.length">
              <template v-for="(block, blockIndex) in turn.blocks" :key="blockIndex">
                <div v-if="block.kind === 'heading'" class="rq-msg__heading">{{ block.text }}</div>
                <p v-else-if="block.kind === 'paragraph'" class="rq-msg__para">{{ block.text }}</p>
                <ul v-else-if="block.kind === 'bullets'" class="rq-msg__list">
                  <li v-for="(item, itemIndex) in block.items" :key="itemIndex">{{ item }}</li>
                </ul>
                <ol v-else-if="block.kind === 'ordered'" class="rq-msg__list is-ordered">
                  <li v-for="(item, itemIndex) in block.items" :key="itemIndex">{{ item }}</li>
                </ol>
              </template>
            </template>
            <span v-else-if="turn.content">{{ turn.content }}</span>
            <span v-else class="rq-msg__typing">{{ $t('pm.requirements.ai.thinking') }}</span>
          </div>
        </div>
      </template>
    </div>

    <div v-if="turns.length" class="rq-chat__tools">
      <t-button size="small" variant="outline" :disabled="starting || isStreaming" @click="restart">
        {{ $t('pm.requirements.ai.restart') }}
      </t-button>
      <t-button size="small" theme="primary" variant="outline" :disabled="starting || isStreaming" @click="generatePlan">
        {{ $t('pm.requirements.ai.generatePlan') }}
      </t-button>
    </div>

    <div class="rq-chat__input">
      <t-textarea
        v-model="input"
        :placeholder="$t('pm.requirements.ai.placeholder')"
        :autosize="{ minRows: 3, maxRows: 3 }"
        :disabled="!turns.length || starting || isStreaming"
        @keydown.enter.exact.prevent="submitInput"
      />
      <t-button
        theme="primary"
        :loading="isStreaming"
        :disabled="!turns.length || starting || isStreaming || !input.trim()"
        @click="submitInput"
      >
        {{ $t('pm.requirements.ai.send') }}
      </t-button>
    </div>

    <PmRequirementPlanDialog v-model:visible="planVisible" :markdown="planMarkdown" />
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, reactive, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import { analyzePmRequirement, type PmRequirement } from '@/api/pm';
import { useStream } from '@/api/chat/streame';
import { AGENT_MODE_SMART_REASONING, listAgents, type CustomAgent } from '@/api/agent';
import {
  parseAnalysisSections,
  type RequirementAnalysis,
} from './pmRequirementState';
import { formatChatBlocks } from './pmChatFormat';
import PmRequirementPlanDialog from './PmRequirementPlanDialog.vue';

const props = defineProps<{
  requirement: PmRequirement;
  initialAnalysis: RequirementAnalysis;
}>();
const emit = defineEmits<{ 'analysis-change': [analysis: RequirementAnalysis] }>();

const { t } = useI18n();
const { isStreaming, error, onChunk, startStream, stopStream } = useStream();

interface ChatTurn {
  role: 'ai' | 'user';
  content: string;
}

const turns = ref<ChatTurn[]>([]);
// 渲染用视图：AI 回复额外解析出排版块，用户输入不着色不改写
const renderedTurns = computed(() =>
  turns.value.map((turn) => ({
    role: turn.role,
    content: turn.content,
    blocks: turn.role === 'ai' ? formatChatBlocks(turn.content) : [],
  })),
);
const input = ref('');
const sessionId = ref('');
const starting = ref(false);
const planVisible = ref(false);
const chatEl = ref<HTMLElement | null>(null);
// 结论随流式回复逐步补全，用户未看到的字段不会被清空
const analysis = reactive<RequirementAnalysis>({ ...props.initialAnalysis });
// 本次流式回复累积的纯文本，结束后解析四段结论
let answerBuffer = '';

// 智能体：仅列出租户内自建（非内置）智能体，留空即默认通用分析
const agents = ref<CustomAgent[]>([]);
const agentsLoading = ref(false);
const agentsLoaded = ref(false);
const selectedAgentId = ref('');
const selectedAgent = computed(
  () => agents.value.find((item) => item.id === selectedAgentId.value) || null,
);
const agentOptions = computed(() => agents.value.map((item) => ({ label: item.name, value: item.id })));

// 由四段结论拼出完整方案；任一段有内容即可查看
const planMarkdown = computed(() => {
  const sections: Array<[string, string]> = [
    [t('pm.requirements.form.value'), analysis.value],
    [t('pm.requirements.form.impact'), analysis.impact],
    [t('pm.requirements.form.risk'), analysis.risk],
    [t('pm.requirements.form.designPlan'), analysis.designPlan],
  ];
  const filled = sections.filter(([, text]) => text.trim());
  if (!filled.length) return '';
  return filled.map(([label, text]) => `## ${label}\n${text.trim()}`).join('\n\n');
});
const hasPlan = computed(() => Boolean(planMarkdown.value));

// 只取 answer 正文，逐步追加到当前 AI 气泡
onChunk((data) => {
  if (data?.response_type !== 'answer') return;
  const text = typeof data.content === 'string' ? data.content : '';
  if (!text) return;
  answerBuffer += text;
  const last = turns.value[turns.value.length - 1];
  if (last && last.role === 'ai') last.content += text;
  void scrollToBottom();
});

watchError();

function watchError() {
  // 流式错误只在一次请求内提示一次
  watch(error, (value) => {
    if (value) MessagePlugin.error(t('pm.requirements.ai.streamFailed'));
  });
}

async function loadAgents() {
  if (agentsLoaded.value || agentsLoading.value) return;
  agentsLoading.value = true;
  try {
    const res = await listAgents();
    agents.value = (res?.data ?? []).filter((item) => !item.is_builtin);
    agentsLoaded.value = true;
    if (selectedAgentId.value && !selectedAgent.value) selectedAgentId.value = '';
  } catch {
    MessagePlugin.error(t('pm.requirements.ai.agentLoadFailed'));
  } finally {
    agentsLoading.value = false;
  }
}

void loadAgents();

// 切换智能体：会话已建立时按新智能体重开首轮，未开始时仅记录选择
function onAgentChange(value: unknown) {
  selectedAgentId.value = typeof value === 'string' ? value : '';
  if (!sessionId.value) return;
  void start();
}

async function start() {
  if (starting.value || isStreaming.value) return;
  stopStream();
  turns.value = [];
  answerBuffer = '';
  starting.value = true;
  try {
    const session = await analyzePmRequirement(props.requirement.id);
    sessionId.value = session.session_id;
    starting.value = false;
    await send(buildFirstQuery());
  } catch {
    MessagePlugin.error(t('pm.requirements.ai.startFailed'));
  } finally {
    starting.value = false;
  }
}

function restart() {
  void start();
}

// 首轮指令：先追问澄清，不直接给结论
function buildFirstQuery(): string {
  return [
    `请分析需求：${props.requirement.title}`,
    `初步想法：${props.requirement.description || '（暂无）'}`,
    '请先提出需要澄清的问题（每轮最多 3 个），并附上你的推测答案，等我回答后再继续。',
  ].join('\n');
}

// 出方案指令：固定四段结构，便于前端解析与回写
function buildPlanQuery(): string {
  return [
    '请基于以上对话给出最终分析方案，严格按以下四段输出，不要输出其他标题：',
    '## 需求价值',
    '## 关联功能影响',
    '## 技术风险',
    '## 实现方案',
  ].join('\n');
}

async function submitInput() {
  const text = input.value.trim();
  if (!text || isStreaming.value || starting.value || !sessionId.value) return;
  input.value = '';
  await send(text);
}

async function send(query: string) {
  // smart-reasoning 智能体走 Agent 管线，其余走 RAG 管线并携带 agent_id
  const agent = selectedAgent.value;
  const agentMode = agent?.config?.agent_mode === AGENT_MODE_SMART_REASONING;
  turns.value.push({ role: 'user', content: query });
  turns.value.push({ role: 'ai', content: '' });
  answerBuffer = '';
  await scrollToBottom();
  await startStream({
    session_id: sessionId.value,
    query,
    agent_enabled: agentMode,
    ...(agent ? { agent_id: agent.id } : {}),
    method: 'POST',
    url: agentMode ? '/api/v1/agent-chat' : '/api/v1/knowledge-chat',
  });
  const last = turns.value[turns.value.length - 1];
  if (last && last.role === 'ai' && !last.content) {
    last.content = t('pm.requirements.ai.emptyReply');
  }
  mergeParsed(answerBuffer);
  await scrollToBottom();
}

// 合并解析结果：只覆盖模型给出的非空字段
function mergeParsed(text: string) {
  const parsed = parseAnalysisSections(text);
  let changed = false;
  (['value', 'impact', 'risk', 'designPlan'] as const).forEach((key) => {
    const next = parsed[key];
    if (next && next !== analysis[key]) {
      analysis[key] = next;
      changed = true;
    }
  });
  if (changed) emit('analysis-change', { ...analysis });
}

function generatePlan() {
  if (isStreaming.value || starting.value || !sessionId.value) return;
  void send(buildPlanQuery());
}

async function scrollToBottom() {
  await nextTick();
  const el = chatEl.value;
  if (el) el.scrollTop = el.scrollHeight;
}
</script>

<style lang="less" scoped>
.rq-chat {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  overflow: hidden;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-page);
}

.rq-chat__head {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  padding: var(--app-space-2) var(--app-space-3);
  border-bottom: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-container);
}

.rq-chat__title {
  flex-shrink: 0;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.rq-chat__agent {
  flex: 1;
  min-width: 0;
}

/* 方案链接置于对话顶部，始终可见 */
.rq-chat__plan {
  flex-shrink: 0;
  padding: 6px var(--app-space-3);
  border-bottom: 1px solid var(--td-component-stroke);
  background: var(--td-brand-color-1);
}

.rq-chat__plan-link {
  padding: 0;
  font-family: inherit;
  font-size: var(--app-text-sm);
  color: var(--td-brand-color);
  background: transparent;
  border: 0;
  cursor: pointer;

  &:hover {
    text-decoration: underline;
  }
}

.rq-chat__body {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: var(--app-space-3);
}

.rq-chat__idle {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--app-space-3);
  height: 100%;
  text-align: center;
}

.rq-chat__idle-text {
  max-width: 320px;
  margin: 0;
  font-size: var(--app-text-sm);
  line-height: 1.7;
  color: var(--td-text-color-placeholder);
}

.rq-msg {
  margin-bottom: var(--app-space-3);

  &.is-user {
    text-align: right;

    .rq-msg__role {
      justify-content: flex-end;
    }

    .rq-msg__bubble {
      color: #fff;
      background: var(--td-brand-color);
    }
  }
}

.rq-msg__role {
  display: flex;
  margin-bottom: 2px;
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}

.rq-msg__bubble {
  display: inline-block;
  max-width: 92%;
  padding: 7px 10px;
  font-size: var(--app-text-md);
  line-height: 1.7;
  color: var(--td-text-color-primary);
  text-align: left;
  white-space: pre-wrap;
  word-break: break-word;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
}

.rq-msg__typing {
  color: var(--td-text-color-placeholder);
}

/* 块渲染时由各块控制换行，避免 pre-wrap 产生多余空行 */
.rq-msg__bubble.is-blocks {
  white-space: normal;
}

.rq-msg__heading {
  margin: 8px 0 4px;
  font-weight: 600;

  &:first-child {
    margin-top: 0;
  }
}

.rq-msg__para {
  margin: 0 0 6px;

  &:last-child {
    margin-bottom: 0;
  }
}

.rq-msg__list {
  margin: 0 0 6px;
  padding-left: 18px;

  &:last-child {
    margin-bottom: 0;
  }

  li + li {
    margin-top: 2px;
  }

  &.is-ordered {
    list-style: decimal;
  }

  &:not(.is-ordered) {
    list-style: disc;
  }
}

.rq-chat__tools {
  flex-shrink: 0;
  display: flex;
  justify-content: flex-end;
  gap: var(--app-space-2);
  padding: var(--app-space-2) var(--app-space-3);
  border-top: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-container);
}

.rq-chat__input {
  flex-shrink: 0;
  display: flex;
  gap: var(--app-space-2);
  padding: var(--app-space-2);
  border-top: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-container);

  :deep(.t-textarea) {
    flex: 1;
  }
}
</style>
