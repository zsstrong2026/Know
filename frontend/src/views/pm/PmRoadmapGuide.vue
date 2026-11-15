<template>
  <!-- 规划引导：左侧 AI 全程引导对话，右侧产出物预览（产品认知 / 功能结构 / 核心流程 / 首期范围 / 整体规划） -->
  <div class="pg">
    <div class="pg-crumb">
      <button class="pg-link" type="button" @click="requestClose">{{ $t('pm.nav.roadmap') }}</button>
      <span>/</span>
      <span>{{ $t('pm.roadmap.guide.title') }}</span>
    </div>

    <header class="pg-head">
      <span class="pg-head-title">{{ $t('pm.roadmap.guide.title') }}</span>
      <span class="pg-head-desc">{{ headDescription }}</span>
      <div class="pg-head-ops">
        <t-button variant="outline" size="small" :disabled="submitting" @click="requestClose">
          {{ $t('pm.roadmap.guide.exit') }}
        </t-button>
      </div>
    </header>

    <div class="pg-grid">
      <!-- 左：AI 引导 -->
      <section class="pg-card pg-chat-card">
        <div class="pg-card-head">
          <span>{{ $t('pm.roadmap.guide.chatTitle') }}</span>
          <t-select
            class="pg-agent"
            :value="selectedAgentId"
            :options="agentOptions"
            :placeholder="$t('pm.roadmap.guide.agentDefault')"
            :loading="agentsLoading"
            :disabled="starting || isStreaming"
            clearable
            size="small"
            @change="onAgentChange"
          />
          <span class="pg-card-meta" :class="{ 'is-error': saveState === 'error' }">{{ saveLabel }}</span>
        </div>

        <div ref="chatRef" class="pg-chat">
          <div
            v-for="(message, index) in renderedMessages"
            :key="index"
            class="pg-msg"
            :class="`is-${message.role}`"
          >
            <span class="pg-avatar">{{ avatarOf(message.role) }}</span>
            <!-- AI 回复按块渲染，用户输入保持纯文本 -->
            <div class="pg-bubble" :class="{ 'is-blocks': message.blocks.length }">
              <template v-if="message.blocks.length">
                <template v-for="(block, blockIndex) in message.blocks" :key="blockIndex">
                  <div v-if="block.kind === 'heading'" class="pg-bubble-heading">{{ block.text }}</div>
                  <p v-else-if="block.kind === 'paragraph'" class="pg-bubble-para">{{ block.text }}</p>
                  <ul v-else-if="block.kind === 'bullets'" class="pg-bubble-list">
                    <li v-for="(item, itemIndex) in block.items" :key="itemIndex">{{ item }}</li>
                  </ul>
                  <ol v-else-if="block.kind === 'ordered'" class="pg-bubble-list is-ordered">
                    <li v-for="(item, itemIndex) in block.items" :key="itemIndex">{{ item }}</li>
                  </ol>
                  <div v-else-if="block.kind === 'table'" class="pg-bubble-table">
                    <table>
                      <thead>
                        <tr>
                          <th v-for="(header, headerIndex) in block.headers" :key="headerIndex">{{ header }}</th>
                        </tr>
                      </thead>
                      <tbody>
                        <tr v-for="(row, rowIndex) in block.rows" :key="rowIndex">
                          <td v-for="(cell, cellIndex) in row" :key="cellIndex">{{ cell }}</td>
                        </tr>
                      </tbody>
                    </table>
                  </div>
                  <dl v-else-if="block.kind === 'kv'" class="pg-bubble-kv">
                    <template v-for="(item, itemIndex) in block.items" :key="itemIndex">
                      <dt>{{ item.label }}</dt>
                      <dd>{{ item.value }}</dd>
                    </template>
                  </dl>
                </template>
              </template>
              <span v-else-if="message.text">{{ message.text }}</span>
              <span v-else class="pg-bubble-typing">{{ $t('pm.roadmap.guide.thinking') }}</span>
            </div>
          </div>
        </div>

        <div class="pg-input">
          <t-input
            v-model="input"
            :placeholder="$t('pm.roadmap.guide.inputPlaceholder')"
            :disabled="isStreaming"
            @enter="send"
          />
          <t-button
            theme="primary"
            :loading="isStreaming"
            :disabled="isStreaming || !input.trim()"
            @click="send"
          >
            {{ $t('pm.roadmap.guide.send') }}
          </t-button>
        </div>
      </section>

      <!-- 右：产出物预览 -->
      <section class="pg-card pg-output-card">
        <div class="pg-card-head">
          <span>{{ $t('pm.roadmap.guide.outputTitle') }}</span>
          <span class="pg-card-meta">{{ $t('pm.roadmap.guide.previewNote') }}</span>
        </div>

        <div class="pg-output">
          <t-tabs v-model="activeTab" class="pg-tabs">
            <!-- 产品认知：AI 自动整理后仍可人工修订，改动即时自动保存 -->
            <t-tab-panel value="cognition" :label="$t('pm.roadmap.guide.tabs.cognition')">
              <div class="pg-block">
                <p class="pg-stat-line">{{ $t('pm.roadmap.guide.cognition.hint') }}</p>
                <div class="pg-cognition">
                  <div v-for="field in COGNITION_FIELDS" :key="field.key" class="pg-cognition-item">
                    <span class="pg-cognition-label">{{ $t(field.labelKey) }}</span>
                    <t-textarea
                      :model-value="cognition[field.key]"
                      :autosize="{ minRows: 2, maxRows: 6 }"
                      :placeholder="$t('pm.roadmap.guide.cognition.placeholder')"
                      @update:model-value="(value: string) => updateCognition(field.key, value)"
                    />
                  </div>
                </div>
              </div>
            </t-tab-panel>

            <!-- 功能结构图 -->
            <t-tab-panel value="features" :label="$t('pm.roadmap.guide.tabs.features')">
              <div class="pg-block">
                <!-- 子系统由用户指定：作为本次规划功能清单的第一列默认值 -->
                <div class="pg-subsystem-bar">
                  <label for="pg-subsystem">{{ $t('pm.roadmap.guide.feature.subsystem') }}</label>
                  <t-input
                    id="pg-subsystem"
                    v-model="subsystemValue"
                    size="small"
                    class="pg-subsystem-input"
                    :placeholder="$t('pm.roadmap.guide.feature.subsystemPlaceholder')"
                  />
                </div>
                <p class="pg-stat-line">
                  {{ $t('pm.roadmap.guide.stats.tree', { subsystems: stats.subsystems, modules: stats.modules, features: stats.features, operations: stats.operations }) }}
                </p>
                <div v-if="featureTree.length" class="pg-tree">
                  <div
                    v-for="moduleNode in featureTree"
                    :key="`${moduleNode.subsystem}/${moduleNode.module}`"
                    class="pg-tree-module"
                  >
                    <div class="pg-tree-module-name">
                      <span v-if="moduleNode.subsystem" class="pg-tree-subsystem">{{ moduleNode.subsystem }}</span>
                      {{ moduleNode.module }}
                    </div>
                    <div v-for="featureNode in moduleNode.features" :key="featureNode.name" class="pg-tree-feature">
                      <div class="pg-tree-feature-name">{{ featureNode.name }}</div>
                      <div class="pg-tree-ops">
                        <t-tag
                          v-for="operation in featureNode.operations"
                          :key="operation.path"
                          size="small"
                          variant="light"
                        >
                          {{ operation.name }}
                        </t-tag>
                      </div>
                    </div>
                  </div>
                </div>
                <p v-else class="pg-empty">{{ $t('pm.roadmap.guide.feature.empty') }}</p>
              </div>
            </t-tab-panel>

            <!-- 核心流程图 -->
            <t-tab-panel value="flows" :label="$t('pm.roadmap.guide.tabs.flows')">
              <div class="pg-block">
                <PmMarkdown v-if="flowsMarkdown" :content="flowsMarkdown" />
                <p v-else class="pg-empty">{{ $t('pm.roadmap.guide.flow.empty') }}</p>
              </div>
            </t-tab-panel>

            <!-- 首期范围 -->
            <t-tab-panel value="scope" :label="$t('pm.roadmap.guide.tabs.scope')">
              <div class="pg-block">
                <div class="pg-scope-bar">
                  <span>{{ $t('pm.roadmap.guide.scope.selected', { selected: scopeValue.length, total: stats.operations }) }}</span>
                  <t-button variant="text" size="small" :disabled="!scopeGroups.length" @click="selectAllScope">
                    {{ $t('pm.roadmap.guide.chips.scopeAll') }}
                  </t-button>
                  <t-button variant="text" size="small" :disabled="!scopeValue.length" @click="clearScope">
                    {{ $t('pm.roadmap.guide.scope.clearAll') }}
                  </t-button>
                </div>
                <t-checkbox-group v-model="scopeValue" class="pg-scope">
                  <div v-for="group in scopeGroups" :key="group.key" class="pg-scope-group">
                    <div class="pg-scope-head">{{ group.label }}</div>
                    <div class="pg-scope-items">
                      <t-checkbox v-for="option in group.options" :key="option.value" :value="option.value">
                        {{ option.label }}
                      </t-checkbox>
                    </div>
                  </div>
                </t-checkbox-group>
                <p v-if="!scopeGroups.length" class="pg-empty">{{ $t('pm.roadmap.guide.scope.empty') }}</p>
              </div>
            </t-tab-panel>

            <!-- 整体规划：汇总确认并完成规划 -->
            <t-tab-panel value="plan" :label="$t('pm.roadmap.guide.tabs.plan')">
              <div class="pg-block">
                <div class="pg-summary">
                  <div class="pg-summary-item">
                    <b>{{ stats.modules }}</b>
                    <span>{{ $t('pm.roadmap.guide.summary.modules') }}</span>
                  </div>
                  <div class="pg-summary-item">
                    <b>{{ stats.features }}</b>
                    <span>{{ $t('pm.roadmap.guide.summary.features') }}</span>
                  </div>
                  <div class="pg-summary-item">
                    <b>{{ stats.operations }}</b>
                    <span>{{ $t('pm.roadmap.guide.summary.operations') }}</span>
                  </div>
                  <div class="pg-summary-item">
                    <b>{{ draft.flows?.length ?? 0 }}</b>
                    <span>{{ $t('pm.roadmap.guide.summary.flows') }}</span>
                  </div>
                  <div class="pg-summary-item">
                    <b>{{ stats.scope }}</b>
                    <span>{{ $t('pm.roadmap.guide.summary.scope') }}</span>
                  </div>
                </div>

                <h5>{{ $t('pm.roadmap.guide.blocks.batch') }}</h5>
                <dl class="pg-info">
                  <dt>{{ $t('pm.roadmap.guide.batch.name') }}</dt>
                  <dd>{{ batch.batch_name }}</dd>
                  <dt>{{ $t('pm.roadmap.guide.batch.type') }}</dt>
                  <dd>{{ $t(`pm.roadmap.planType.${batch.plan_type}`) }}</dd>
                  <dt>{{ $t('pm.roadmap.guide.batch.version') }}</dt>
                  <dd>{{ batch.version_id || '—' }}</dd>
                  <dt>{{ $t('pm.roadmap.guide.batch.docPath') }}</dt>
                  <dd class="pg-path">{{ docPath }}</dd>
                  <dt>{{ $t('pm.roadmap.guide.batch.snapshotPath') }}</dt>
                  <dd class="pg-path">{{ snapshotPath }}</dd>
                </dl>

                <h5>{{ $t('pm.roadmap.guide.blocks.sync') }}</h5>
                <t-checkbox v-model="syncFeatures">{{ $t('pm.roadmap.guide.sync.hint') }}</t-checkbox>
                <p class="pg-note">{{ $t('pm.roadmap.guide.sync.note') }}</p>

                <div class="pg-plan-foot">
                  <t-button theme="primary" block :loading="submitting" @click="finish">
                    {{ $t('pm.roadmap.guide.finish') }}
                  </t-button>
                  <p class="pg-note">{{ $t('pm.roadmap.guide.plan.hint') }}</p>
                </div>
              </div>
            </t-tab-panel>
          </t-tabs>
        </div>
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  activatePlanBatch,
  generatePlanBatch,
  savePlanDraft,
  startPlanGuideSession,
  type PmPlanBatch,
  type PmRoadmapCognition,
  type PmRoadmapDraft,
  type PmRoadmapFlow,
} from '@/api/pm';
import { useStream } from '@/api/chat/streame';
import { AGENT_MODE_SMART_REASONING, listAgents, type CustomAgent } from '@/api/agent';
import {
  ROADMAP_STEP_COUNT,
  buildFeatureTree,
  canAdvanceStep,
  featurePath,
  isValidMermaidFlow,
  makeDraftFeature,
  normalizeScope,
  summarizeDraft,
} from './pmRoadmapState';
import { formatChatBlocks } from './pmChatFormat';
import PmMarkdown from './components/PmMarkdown.vue';

interface GuideMessage {
  role: 'ai' | 'user' | 'system';
  text: string;
}

interface FeatureRow {
  subsystem: string;
  module: string;
  feature: string;
  operation: string;
}

const props = defineProps<{
  projectId: string;
  projectName: string;
  batch: PmPlanBatch;
  seedDraft?: PmRoadmapDraft | null;
  activeBatchName?: string;
}>();
const emit = defineEmits<{ close: []; completed: [] }>();

const { t } = useI18n();
// 引导对话复用通用 SSE 通道：agent_id 为请求级参数，中途切换智能体无需重建会话
const { isStreaming, error, onChunk, startStream, stopStream } = useStream();

// 右侧产出物页签：前四项为 AI 逐步补齐的产物，最后一项用于汇总确认
const TAB_KEYS = ['cognition', 'features', 'flows', 'scope', 'plan'] as const;
type OutputTab = (typeof TAB_KEYS)[number];
const activeTab = ref<OutputTab>('cognition');

const COGNITION_FIELDS = [
  { key: 'target_users', labelKey: 'pm.roadmap.guide.fields.targetUsers' },
  { key: 'core_value', labelKey: 'pm.roadmap.guide.fields.coreValue' },
  { key: 'scenarios', labelKey: 'pm.roadmap.guide.fields.scenarios' },
  { key: 'background', labelKey: 'pm.roadmap.guide.fields.background' },
] as const;

// AI 回复里可识别的认知字段行，例如「目标用户：一线运营」
const COGNITION_PATTERNS: Array<{ key: keyof PmRoadmapCognition; re: RegExp }> = [
  { key: 'target_users', re: /(?:目标用户|目标人群|面向用户)\s*[:：]\s*(.+)/ },
  { key: 'core_value', re: /(?:核心价值|价值主张)\s*[:：]\s*(.+)/ },
  { key: 'scenarios', re: /(?:关键场景|核心场景|主要场景|使用场景)\s*[:：]\s*(.+)/ },
  { key: 'background', re: /(?:规划背景|背景说明|项目背景|本次背景)\s*[:：]\s*(.+)/ },
];

// 功能清单表头（feature-list 四列规范 + 兼容旧三级表头），解析时跳过
const HEADER_WORDS = new Set([
  '子系统', '一级模块', '二级模块', '功能说明',
  '模块', '功能点', '操作项',
  'subsystem', 'module', 'feature', 'operation',
]);

// Mermaid 连线/声明行与表格分隔行都不是功能清单，按行解析时必须排除
const MERMAID_LINE_RE = /-->|==>|-\.->|~~~|^\s*(?:flowchart|graph|subgraph|end)\b/i;
const MERMAID_SHAPE_RE = /[[\]{}]/;
// 纯符号段（如表格分隔行的 ---）与整句说明都不是合法的三级名称
const NON_FEATURE_PART_RE = /^[-:=_*~.\s]+$/;
const SENTENCE_PART_RE = /[。；;！!？?]/;

function isFeaturePart(part: string): boolean {
  if (!part || NON_FEATURE_PART_RE.test(part)) return false;
  // 说明句通常很长且带句读，避免把 AI 的总结文字当成模块/功能点
  return part.length <= 40 && !SENTENCE_PART_RE.test(part);
}

// ===== 草稿状态 =====

function emptyCognition(): PmRoadmapCognition {
  return { target_users: '', core_value: '', scenarios: '', background: '' };
}

// 以空草稿为底合并，避免历史草稿缺字段导致渲染报错
function normalizeDraft(input?: PmRoadmapDraft | null): PmRoadmapDraft {
  return {
    cognition: { ...emptyCognition(), ...(input?.cognition ?? {}) },
    // 子系统是批次级默认值，行级为空时由它兜底
    subsystem: input?.subsystem ?? '',
    features: (input?.features ?? []).map((item) => ({ ...item })),
    flows: (input?.flows ?? []).map((item) => ({ ...item })),
    scope: normalizeScope(input?.scope),
  };
}

const draft = ref<PmRoadmapDraft>(normalizeDraft(
  props.seedDraft === undefined ? props.batch.draft : props.seedDraft,
));
// 已被人工编辑的认知字段：后续 AI 回填跳过这些字段，避免覆盖用户手写内容
const manualCognition = ref<Set<keyof PmRoadmapCognition>>(new Set());
const input = ref('');
const syncFeatures = ref(true);
const submitting = ref(false);
const saveState = ref<'idle' | 'saving' | 'saved' | 'error'>('idle');
// 单一对话流：不再按步骤分组
const messages = ref<GuideMessage[]>([]);
const chatRef = ref<HTMLElement | null>(null);

// ===== AI 引导会话 =====
// 会话由后端创建（带隐藏标记，不出现在首页对话列表），失败时降级为本地引导
const sessionId = ref('');
const guidePrompt = ref('');
const starting = ref(false);
const firstQuerySent = ref(false);
let answerBuffer = '';

// 智能体：仅列出租户内自建（非内置）智能体，留空即默认通用引导
const agents = ref<CustomAgent[]>([]);
const agentsLoading = ref(false);
const agentsLoaded = ref(false);
const selectedAgentId = ref('');
const selectedAgent = computed(
  () => agents.value.find((item) => item.id === selectedAgentId.value) || null,
);
const agentOptions = computed(() => agents.value.map((item) => ({ label: item.name, value: item.id })));

const cognition = computed<PmRoadmapCognition>(() => draft.value.cognition ?? emptyCognition());
const stats = computed(() => summarizeDraft(draft.value));
const featureTree = computed(() => buildFeatureTree(draft.value.features, draft.value.subsystem));

// 子系统由用户指定（PC端 / 移动端 / 运营端…），保存为批次级默认值
const subsystemValue = computed<string>({
  get: () => draft.value.subsystem ?? '',
  set: (value) => {
    draft.value.subsystem = (value ?? '').trim();
    scheduleSave();
  },
});

// 流程图预览：按模块拼成 Markdown，交给 PmMarkdown 渲染为 SVG
const flowsMarkdown = computed(() => (draft.value.flows ?? [])
  .filter((flow) => (flow.mermaid ?? '').trim())
  .map((flow) => `### ${(flow.module ?? '').trim() || '—'}\n\n\`\`\`mermaid\n${(flow.mermaid ?? '').trim()}\n\`\`\``)
  .join('\n\n'));

// 首期范围勾选项：模块 / 功能点 / 操作项 的三级路径作为唯一值；
// 不同子系统的同名模块需要分别展示，分组 key 带上子系统
const scopeGroups = computed(() => featureTree.value.map((moduleNode) => ({
  key: `${moduleNode.subsystem}/${moduleNode.module}`,
  label: moduleNode.subsystem
    ? `${moduleNode.subsystem} / ${moduleNode.module}`
    : moduleNode.module,
  options: moduleNode.features.flatMap((featureNode) => featureNode.operations.map((operation) => ({
    value: operation.path,
    label: `${featureNode.name} / ${operation.name}`,
  }))),
})));

const scopeValue = computed<string[]>({
  get: () => normalizeScope(draft.value.scope),
  set: (value) => {
    draft.value.scope = value;
    scheduleSave();
  },
});

const planTypesInitial = computed(() => props.batch.plan_type);

// 生成后的知识库落库路径（仅展示；实际写入由后端完成）
const docPath = computed(() => `需求/产品路线图-${props.projectName || props.batch.batch_name}.md`);
const snapshotPath = computed(
  () => `需求/规划历史/产品路线图-${props.projectName || props.batch.batch_name}-${props.batch.batch_name}.md`,
);

// ===== 文案 =====

const promptParams = computed(() => ({
  name: props.projectName || props.batch.batch_name,
  subsystems: stats.value.subsystems,
  modules: stats.value.modules,
  features: stats.value.features,
  operations: stats.value.operations,
  flows: draft.value.flows?.length ?? 0,
  selected: stats.value.scope,
  total: stats.value.operations,
}));

// 单条开场白 + 后续对话
const renderedMessages = computed(() => [
  { role: 'ai' as const, text: t('pm.roadmap.guide.prompts.overall', promptParams.value) },
  ...messages.value,
].map((message) => ({
  role: message.role,
  text: message.text,
  blocks: message.role === 'ai' ? formatChatBlocks(message.text) : [],
})));

const saveLabel = computed(() => {
  if (saveState.value === 'saving') return t('pm.roadmap.guide.saving');
  if (saveState.value === 'saved') return t('pm.roadmap.guide.saved');
  if (saveState.value === 'error') return t('pm.roadmap.guide.saveError');
  return t('pm.roadmap.guide.autoSave');
});

const headDescription = computed(() => {
  if (planTypesInitial.value === 'replan') return t('pm.roadmap.guide.descReplan');
  if (planTypesInitial.value === 'iteration') {
    return t('pm.roadmap.guide.descIncrement', { name: props.activeBatchName || '—' });
  }
  return t('pm.roadmap.guide.descInitial');
});

function avatarOf(role: GuideMessage['role']): string {
  if (role === 'user') return t('pm.roadmap.guide.me');
  return role === 'system' ? '!' : 'AI';
}

// ===== 消息 =====

async function scrollChat() {
  await nextTick();
  const element = chatRef.value;
  if (element) element.scrollTop = element.scrollHeight;
}

function pushMessage(message: GuideMessage) {
  messages.value = [...messages.value, message];
  void scrollChat();
}

// 追加流式片段到最后一条 AI 消息（整体替换以触发渲染）
function appendToLast(chunk: string) {
  const list = messages.value;
  const last = list[list.length - 1];
  if (!last || last.role !== 'ai') return;
  messages.value = [...list.slice(0, -1), { ...last, text: last.text + chunk }];
  void scrollChat();
}

// 只取 answer 正文，忽略思考过程等其他事件
onChunk((data) => {
  if (data?.response_type !== 'answer') return;
  const text = typeof data.content === 'string' ? data.content : '';
  if (!text) return;
  answerBuffer += text;
  appendToLast(text);
});

// 流式错误只在一次请求内提示一次
watch(error, (value) => {
  if (value) MessagePlugin.error(t('pm.roadmap.guide.streamFailed'));
});

async function loadAgents() {
  if (agentsLoaded.value || agentsLoading.value) return;
  agentsLoading.value = true;
  try {
    const res = await listAgents();
    agents.value = (res?.data ?? []).filter((item) => !item.is_builtin);
    agentsLoaded.value = true;
    if (selectedAgentId.value && !selectedAgent.value) selectedAgentId.value = '';
    // 未手动选择时默认取第一个自建智能体，进入引导即可直接对话
    if (!selectedAgentId.value && agents.value.length) selectedAgentId.value = agents.value[0].id;
  } catch {
    MessagePlugin.error(t('pm.roadmap.guide.agentLoadFailed'));
  } finally {
    agentsLoading.value = false;
  }
}

// 切换智能体只影响后续请求：agent_id 是请求级参数，已有会话无需重建
function onAgentChange(value: unknown) {
  selectedAgentId.value = typeof value === 'string' ? value : '';
}

// 懒创建隐藏引导会话；失败返回 false，调用方降级为纯本地引导
async function ensureSession(): Promise<boolean> {
  if (sessionId.value) return true;
  starting.value = true;
  try {
    const res = await startPlanGuideSession(props.batch.id);
    sessionId.value = res.session_id;
    guidePrompt.value = res.prompt;
    return true;
  } catch {
    MessagePlugin.warning(t('pm.roadmap.guide.agentStartFailed'));
    return false;
  } finally {
    starting.value = false;
  }
}

// ===== 持久化：串行队列保证最后一次写入的是最新草稿 =====

let saveTimer: ReturnType<typeof setTimeout> | null = null;
let saveChain: Promise<boolean> = Promise.resolve(true);
let dirty = false;

function cloneDraft(value: PmRoadmapDraft): PmRoadmapDraft {
  return JSON.parse(JSON.stringify(value)) as PmRoadmapDraft;
}

// 完成度推导步骤：供批次列表展示与草稿落库使用
function inferStep(): number {
  for (const step of [1, 2, 3, 4]) {
    if (!canAdvanceStep(step, draft.value)) return step;
  }
  return ROADMAP_STEP_COUNT;
}

function scheduleSave() {
  dirty = true;
  if (saveTimer) clearTimeout(saveTimer);
  saveTimer = setTimeout(() => {
    saveTimer = null;
    void flush(inferStep());
  }, 400);
}

function flush(targetStep: number, notify = false): Promise<boolean> {
  if (saveTimer) {
    clearTimeout(saveTimer);
    saveTimer = null;
  }
  saveChain = saveChain.then(async () => {
    saveState.value = 'saving';
    try {
      await savePlanDraft(props.batch.id, cloneDraft(draft.value), targetStep);
      saveState.value = 'saved';
      dirty = false;
      return true;
    } catch {
      saveState.value = 'error';
      if (notify) MessagePlugin.error(t('pm.roadmap.guide.saveFailed'));
      return false;
    }
  });
  return saveChain;
}

// ===== 草稿回填：用户输入与 AI 回复共用一套解析 =====

// 回填产品认知：已被用户手工编辑的字段保持不动
function patchCognition(patch: Partial<PmRoadmapCognition>): boolean {
  const next: Partial<PmRoadmapCognition> = {};
  for (const [key, value] of Object.entries(patch) as Array<[keyof PmRoadmapCognition, string]>) {
    if (manualCognition.value.has(key)) continue;
    if ((cognition.value[key] ?? '') === value) continue;
    next[key] = value;
  }
  if (!Object.keys(next).length) return false;
  draft.value.cognition = { ...cognition.value, ...next };
  return true;
}

// 人工编辑产品认知：记录字段后自动保存，右侧预览即时生效
function updateCognition(key: keyof PmRoadmapCognition, value: string) {
  const text = value ?? '';
  if ((cognition.value[key] ?? '') === text) return;
  // 清空表示放弃手填内容，重新允许 AI 回填该字段
  if (text.trim()) manualCognition.value.add(key);
  else manualCognition.value.delete(key);
  draft.value.cognition = { ...cognition.value, [key]: text };
  scheduleSave();
}

// 识别「目标用户：…」等字段行
function extractCognition(text: string): Partial<PmRoadmapCognition> | null {
  const result: Partial<PmRoadmapCognition> = {};
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.replace(/^[\s>*\-+]+/, '').replace(/\*\*/g, '').trim();
    if (!line) continue;
    for (const { key, re } of COGNITION_PATTERNS) {
      if (result[key]) continue;
      const matched = line.match(re);
      const value = matched?.[1]?.trim();
      if (value) result[key] = value;
    }
  }
  return Object.keys(result).length ? result : null;
}

// 「子系统：PC端」单独一行给出，多个用顿号分隔；取首值作为本次默认子系统
const SUBSYSTEM_RE = /^(?:本次)?子系统\s*[:：]\s*(.+)$/;

function extractSubsystem(text: string): string {
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.replace(/^[\s>*\-+]+/, '').replace(/\*\*/g, '').trim();
    if (!line) continue;
    const matched = line.match(SUBSYSTEM_RE);
    if (!matched) continue;
    const value = matched[1]
      .split(/[、,，/｜|]/)
      .map((item) => item.trim())
      .find((item) => isFeaturePart(item));
    if (value) return value;
  }
  return '';
}

// 「子系统 / 一级模块 / 二级模块 / 功能说明」每行一条，分隔符兼容 / ｜ |
// 代码块、Mermaid 连线、表格分隔行与说明句不参与解析，避免产生脏功能项
function parseFeatureLines(text: string): FeatureRow[] {
  const rows: FeatureRow[] = [];
  let inFence = false;
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim();
    if (line.startsWith('```')) {
      inFence = !inFence;
      continue;
    }
    if (inFence || !line) continue;
    if (MERMAID_LINE_RE.test(line) || MERMAID_SHAPE_RE.test(line)) continue;
    const parts = line.split(/\s*[/｜|]\s*/).map((item) => item.trim()).filter(Boolean);
    if (parts.length < 3) continue;
    if (HEADER_WORDS.has(parts[0].toLowerCase())) continue;
    if (!parts.every(isFeaturePart)) continue;
    if (parts.length >= 4) {
      // 四列规范：多段说明合并回功能说明列
      rows.push({
        subsystem: parts[0],
        module: parts[1],
        feature: parts[2],
        operation: parts.slice(3).join('/'),
      });
    } else {
      // 兼容旧的三级写法：模块 / 功能点 / 操作项
      rows.push({ subsystem: '', module: parts[0], feature: parts[1], operation: parts[2] });
    }
  }
  return rows;
}

// 兼容 AI 输出的「| 子系统 | 一级模块 | 二级模块 | 功能说明 |」表格，也兼容旧的三列表格
function parseFeatureTable(text: string): FeatureRow[] {
  const rows: FeatureRow[] = [];
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim();
    if (!line.includes('|')) continue;
    // Mermaid 的「A -->|是| B[...]」也含竖线，必须先排除
    if (MERMAID_LINE_RE.test(line) || MERMAID_SHAPE_RE.test(line)) continue;
    const cells = line.split('|').map((cell) => cell.replace(/\*\*/g, '').trim());
    while (cells.length && !cells[0]) cells.shift();
    while (cells.length && !cells[cells.length - 1]) cells.pop();
    if (cells.length < 3) continue;
    // 四列时首列是子系统，否则按旧三列解析
    const hasSubsystem = cells.length >= 4;
    const subsystemName = hasSubsystem ? cells[0] : '';
    const moduleName = hasSubsystem ? cells[1] : cells[0];
    const featureName = hasSubsystem ? cells[2] : cells[1];
    const rest = hasSubsystem ? cells.slice(3) : cells.slice(2);
    if (!moduleName || !featureName) continue;
    if (HEADER_WORDS.has(moduleName.toLowerCase())) continue;
    if (subsystemName && !isFeaturePart(subsystemName)) continue;
    const operationName = rest.join(' / ').trim();
    if (!isFeaturePart(moduleName) || !isFeaturePart(featureName) || !isFeaturePart(operationName)) continue;
    rows.push({ subsystem: subsystemName, module: moduleName, feature: featureName, operation: operationName });
  }
  return rows;
}

// 去重后追加，返回新增数量；行级子系统为空时用批次默认值兜底
function addFeatureRows(rows: FeatureRow[]): number {
  const existing = new Set(draft.value.features.map(featurePath));
  const fallbackSubsystem = draft.value.subsystem ?? '';
  let added = 0;
  for (const row of rows) {
    const subsystem = row.subsystem || fallbackSubsystem;
    const feature = makeDraftFeature(props.projectId, subsystem, row.module, row.feature, row.operation);
    const path = featurePath(feature);
    if (existing.has(path)) continue;
    existing.add(path);
    draft.value.features.push(feature);
    added += 1;
  }
  return added;
}

// 识别 ```mermaid 代码块，用上方最近的 ### 标题作为模块名
function extractMermaidFlows(text: string): PmRoadmapFlow[] {
  const blocks = text.matchAll(/```mermaid[^\n]*\n([\s\S]*?)```/g);
  const flows: PmRoadmapFlow[] = [];
  for (const block of blocks) {
    const code = (block[1] ?? '').trim();
    if (!isValidMermaidFlow(code)) continue;
    const before = text.slice(0, block.index);
    const headings = before.match(/^###\s+(.+)$/gm);
    const moduleName = headings?.length
      ? headings[headings.length - 1].replace(/^###\s+/, '').trim()
      : '';
    if (!moduleName) continue;
    flows.push({ module: moduleName, mermaid: code });
  }
  return flows;
}

function mermaidFromChain(nodes: string[]): string {
  const lines = ['flowchart LR'];
  nodes.forEach((node, index) => {
    lines.push(`  N${index}["${node.replace(/"/g, '')}"]`);
  });
  nodes.slice(1).forEach((_, index) => {
    lines.push(`  N${index} --> N${index + 1}`);
  });
  return lines.join('\n');
}

// 识别「模块: 节点1 -> 节点2」行
function extractInlineFlows(text: string): PmRoadmapFlow[] {
  const flows: PmRoadmapFlow[] = [];
  for (const raw of text.split(/\r?\n/)) {
    const line = raw.trim().replace(/^[-*+]\s+/, '');
    const matched = line.match(/^([^:：]{1,40})[:：]\s*(.+)$/);
    if (!matched) continue;
    const moduleName = matched[1].trim();
    const nodes = matched[2].split(/\s*(?:->|→|=>)\s*/).map((item) => item.trim()).filter(Boolean);
    if (nodes.length < 2) continue;
    flows.push({ module: moduleName, mermaid: mermaidFromChain(nodes) });
  }
  return flows;
}

// 同模块流程整体替换，返回变更数量
function mergeFlows(flows: PmRoadmapFlow[]): number {
  if (!flows.length) return 0;
  const byModule = new Map<string, PmRoadmapFlow>();
  (draft.value.flows ?? []).forEach((flow) => {
    const name = (flow.module ?? '').trim();
    if (name) byModule.set(name, { ...flow });
  });
  let changed = 0;
  flows.forEach((flow) => {
    const current = byModule.get(flow.module);
    if (current && current.mermaid === flow.mermaid) return;
    byModule.set(flow.module, { ...flow });
    changed += 1;
  });
  if (changed) draft.value.flows = Array.from(byModule.values());
  return changed;
}

interface ApplyResult {
  notes: string[];
  recognized: boolean;
}

// 解析一段文本并回填草稿；recognized 表示是否识别到结构化内容
function applyText(text: string): ApplyResult {
  const notes: string[] = [];
  // 子系统只在草稿尚未指定时采用识别结果，避免覆盖用户手填的值
  const subsystem = extractSubsystem(text);
  if (subsystem && !(draft.value.subsystem ?? '').trim()) {
    draft.value.subsystem = subsystem;
    notes.push(t('pm.roadmap.guide.acks.subsystem', { name: subsystem }));
  }
  const cognitionPatch = extractCognition(text);
  if (cognitionPatch && patchCognition(cognitionPatch)) {
    notes.push(t('pm.roadmap.guide.acks.cognition'));
  }
  const added = addFeatureRows([...parseFeatureTable(text), ...parseFeatureLines(text)]);
  if (added) notes.push(t('pm.roadmap.guide.acks.features', { count: added }));
  const flowCount = mergeFlows([...extractMermaidFlows(text), ...extractInlineFlows(text)]);
  if (flowCount) notes.push(t('pm.roadmap.guide.acks.flows', { count: flowCount }));
  return { notes, recognized: notes.length > 0 };
}

// 用户输入即时回填；未识别到结构时按补充说明写入规划背景
function applyLocalInput(text: string): string {
  const result = applyText(text);
  if (!result.recognized) {
    // 用户自己的补充说明直接追加到背景，即使该字段已被手工修订过
    const merged = cognition.value.background
      ? `${cognition.value.background}\n${text}`
      : text;
    draft.value.cognition = { ...cognition.value, background: merged };
  }
  scheduleSave();
  return result.notes.join('；') || t('pm.roadmap.guide.acks.cognition');
}

// AI 回复结束后自动回填，保证右侧预览与对话同步
function applyAiOutput(text: string) {
  const result = applyText(text);
  if (result.recognized) scheduleSave();
}

// 首轮携带后端下发的技能指令，后续仅带产出物进度与本次输入
function buildQuery(text: string, applied: string): string {
  const parts: string[] = [];
  if (!firstQuerySent.value) parts.push(guidePrompt.value);
  parts.push([
    '【当前产出物进度】',
    `产品认知：已完成 ${COGNITION_FIELDS.filter((field) => (cognition.value[field.key] ?? '').trim()).length}/4 项`,
    `本次子系统：${(draft.value.subsystem ?? '').trim() || '未指定（请先询问用户）'}`,
    `功能结构：${stats.value.subsystems} 个子系统 / ${stats.value.modules} 个模块 / ${stats.value.features} 个功能点 / ${stats.value.operations} 个操作项`,
    `核心流程：${draft.value.flows?.length ?? 0} 个`,
    `首期范围：已选 ${stats.value.scope}/${stats.value.operations}`,
  ].join('\n'));
  parts.push(`【用户输入】${text}`);
  if (applied) parts.push(`【已自动回填】${applied}`);
  parts.push([
    '请基于当前产出物补齐缺口，只输出可直接回填的结构化内容：',
    '- 认知字段逐行给出，格式「目标用户：…」「核心价值：…」「关键场景：…」「规划背景：…」',
    '- 子系统单独一行给出，格式「子系统：PC端」（多个用顿号分隔）；用户未指定时先询问，不要自行假定',
    '- 功能清单用 Markdown 表格「| 子系统 | 一级模块 | 二级模块 | 功能说明 |」，同一模块每行重复填写前两列，',
    '  不同一级模块之间插入一行 | --- | --- | --- | --- |',
    '- 流程用 ```mermaid 代码块，并在代码块上方用「### 模块名」标注所属模块',
    '除以上内容外可加简短说明，但不要重复已确认的信息。',
  ].join('\n'));
  return parts.filter(Boolean).join('\n\n');
}

async function askAgent(text: string, applied: string) {
  const ready = await ensureSession();
  if (!ready) {
    // 会话不可用时保留确定性回执，引导流程不中断
    pushMessage({ role: 'ai', text: applied || t('pm.roadmap.guide.emptyReply') });
    return;
  }
  const query = buildQuery(text, applied);
  firstQuerySent.value = true;

  // smart-reasoning 智能体走 Agent 管线，其余走 RAG 管线并携带 agent_id
  const agent = selectedAgent.value;
  const agentMode = agent?.config?.agent_mode === AGENT_MODE_SMART_REASONING;
  pushMessage({ role: 'ai', text: '' });
  answerBuffer = '';
  await startStream({
    session_id: sessionId.value,
    query,
    agent_enabled: agentMode,
    ...(agent ? { agent_id: agent.id } : {}),
    method: 'POST',
    url: agentMode ? '/api/v1/agent-chat' : '/api/v1/knowledge-chat',
  });
  if (!answerBuffer) appendToLast(t('pm.roadmap.guide.emptyReply'));
  else applyAiOutput(answerBuffer);
}

function send() {
  const text = input.value.trim();
  if (!text || isStreaming.value) return;
  input.value = '';
  pushMessage({ role: 'user', text });
  // 先做确定性回填，再请 AI 顺着当前产出物继续引导
  const applied = applyLocalInput(text);
  void askAgent(text, applied);
}

// ===== 首期范围 =====

function selectAllScope() {
  draft.value.scope = scopeGroups.value.flatMap((group) => group.options.map((option) => option.value));
  scheduleSave();
}

function clearScope() {
  draft.value.scope = [];
  scheduleSave();
}

// ===== 完成规划 =====

// 完成流程的阶段提示：生成与生效失败原因不同，需分别提示
const FINISH_STAGE_KEYS = {
  generate: 'pm.roadmap.guide.finishGenerateFailed',
  activate: 'pm.roadmap.guide.finishActivateFailed',
} as const;

// 完成 = 保存草稿 → 生成路线图与快照（可选同步功能清单）→ 新批次生效
async function finish() {
  const blocked = inferStep();
  if (blocked < ROADMAP_STEP_COUNT) {
    // 未就绪时跳到缺口所在的产出物页签提示补齐
    activeTab.value = TAB_KEYS[blocked - 1];
    MessagePlugin.warning(t(`pm.roadmap.guide.warnings.step${blocked}`));
    return;
  }
  submitting.value = true;
  let stage: keyof typeof FINISH_STAGE_KEYS = 'generate';
  try {
    const saved = await flush(ROADMAP_STEP_COUNT, true);
    // 保存失败已由 flush 内部提示，这里不再重复报错
    if (!saved) return;
    const task = await generatePlanBatch(props.batch.id, syncFeatures.value);
    if (task.status === 'failed') throw new Error(task.error_message || 'failed');
    stage = 'activate';
    await activatePlanBatch(props.batch.id);
    MessagePlugin.success(t('pm.roadmap.guide.finishSuccess'));
    emit('completed');
  } catch (err) {
    // 带上后端返回的 message，便于定位是校验、AI 生成还是生效环节出错
    const detail =
      (err as { message?: string } | null)?.message || String(err);
    MessagePlugin.error(t(FINISH_STAGE_KEYS[stage], { detail }));
  } finally {
    submitting.value = false;
  }
}

async function requestClose() {
  if (submitting.value) return;
  await flush(inferStep());
  emit('close');
}

// 新建批次时把继承的草稿立即落库，避免退出后丢失基线
onMounted(() => {
  void loadAgents();
  if (props.seedDraft) void flush(inferStep());
  void scrollChat();
});

onBeforeUnmount(() => {
  if (saveTimer) {
    clearTimeout(saveTimer);
    saveTimer = null;
  }
  // 组件卸载前补一次保存（路由切换等场景）
  if (dirty) void flush(inferStep());
  stopStream();
});
</script>

<style lang="less" scoped>
.pg {
  position: absolute;
  inset: 0;
  z-index: 20;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  padding: var(--app-space-5) var(--app-space-6);
  background: var(--td-bg-color-page);
}

.pg-crumb {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  margin-bottom: var(--app-space-2);
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
}

.pg-link {
  padding: 0;
  border: 0;
  background: none;
  color: var(--td-brand-color);
  font-size: inherit;
  cursor: pointer;
}

.pg-head {
  display: flex;
  align-items: center;
  gap: var(--app-space-3);
  padding-bottom: var(--app-space-3);
  margin-bottom: var(--app-space-3);
  border-bottom: 1px solid var(--td-component-stroke);
}

.pg-head-title {
  font-size: var(--app-text-2xl);
  font-weight: 700;
  color: var(--td-text-color-primary);
}

.pg-head-desc {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--app-text-md);
  color: var(--td-text-color-secondary);
}

.pg-head-ops {
  margin-left: auto;
  display: flex;
  gap: var(--app-space-2);
  flex-shrink: 0;
}

.pg-grid {
  flex: 1;
  min-height: 0;
  display: grid;
  grid-template-columns: minmax(0, 1.45fr) minmax(0, 1fr);
  gap: var(--app-space-3);
}

.pg-card {
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-container);
}

.pg-card-head {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  padding: 10px 12px;
  border-bottom: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-secondarycontainer);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.pg-card-meta {
  margin-left: auto;
  font-size: var(--app-text-sm);
  font-weight: 400;
  color: var(--td-text-color-placeholder);

  &.is-error {
    color: var(--td-error-color);
  }
}

.pg-agent {
  flex: 1;
  min-width: 0;
  max-width: 200px;
  margin-left: var(--app-space-2);
}

.pg-chat {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: var(--app-space-3);
}

.pg-msg {
  display: flex;
  gap: var(--app-space-2);
  margin-bottom: 10px;

  &.is-user {
    flex-direction: row-reverse;

    .pg-avatar {
      background: var(--td-gray-color-6);
    }

    .pg-bubble {
      background: color-mix(in srgb, var(--td-brand-color) 10%, transparent);
    }
  }

  &.is-system .pg-bubble {
    background: color-mix(in srgb, var(--td-warning-color) 12%, transparent);
    color: var(--td-warning-color);
  }
}

.pg-avatar {
  width: 24px;
  height: 24px;
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  background: var(--td-brand-color);
  color: #fff;
  font-size: var(--app-text-2xs);
}

.pg-bubble {
  max-width: 90%;
  min-width: 0;
  padding: 10px 12px;
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-primary);
  font-size: var(--app-text-md);
  line-height: 1.7;
  white-space: pre-line;
  word-break: break-word;

  // 块渲染时按块纵向排布，取消纯文本的预换行
  &.is-blocks {
    display: flex;
    flex-direction: column;
    gap: 8px;
    white-space: normal;
  }
}

// 小标题：与下方内容成组，段间距略大
.pg-bubble-heading {
  margin-top: 2px;
  font-size: var(--app-text-md);
  font-weight: 600;
  color: var(--td-text-color-primary);

  &:not(:first-child) {
    margin-top: 4px;
  }
}

.pg-bubble-para {
  margin: 0;
  line-height: 1.75;
  white-space: pre-line;
}

.pg-bubble-list {
  margin: 0;
  padding-left: 20px;
  line-height: 1.7;
  list-style: disc;

  &.is-ordered {
    list-style: decimal;
  }

  li + li {
    margin-top: 4px;
  }
}

// 表格：横向可滚动，表头浅底、行间细分隔线
.pg-bubble-table {
  max-width: 100%;
  overflow-x: auto;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-container);

  table {
    width: 100%;
    border-collapse: collapse;
    font-size: var(--app-text-sm);
    line-height: 1.6;
  }

  th,
  td {
    padding: 6px 10px;
    text-align: left;
    vertical-align: top;
    border-bottom: 1px solid var(--td-component-stroke);
  }

  th {
    background: var(--td-bg-color-secondarycontainer);
    font-weight: 600;
    color: var(--td-text-color-primary);
    white-space: nowrap;
  }

  tbody tr:last-child td {
    border-bottom: 0;
  }
}

// 键值信息卡：标签列定宽，值列自适应换行
.pg-bubble-kv {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  gap: 4px 12px;
  margin: 0;
  padding: 8px 10px;
  border: 1px dashed var(--td-component-stroke);
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-container);
  font-size: var(--app-text-sm);
  line-height: 1.7;

  dt {
    color: var(--td-text-color-placeholder);
    white-space: nowrap;
  }

  dd {
    margin: 0;
    color: var(--td-text-color-primary);
    word-break: break-word;
  }
}

.pg-bubble-typing {
  color: var(--td-text-color-placeholder);
}

.pg-input {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  padding: 10px 12px;
  border-top: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-secondarycontainer);
}

.pg-output {
  flex: 1;
  min-height: 0;
  display: flex;
  overflow: hidden;
}

// 页签头部固定，内容区独立滚动
.pg-tabs {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;

  :deep(.t-tabs__header) {
    flex-shrink: 0;
    margin-bottom: 0;
  }

  :deep(.t-tabs__content) {
    flex: 1;
    min-height: 0;
    overflow: hidden;
  }

  :deep(.t-tab-panel) {
    height: 100%;
    overflow-y: auto;
    padding: var(--app-space-3);
    box-sizing: border-box;
  }
}

.pg-block {
  h5 {
    display: flex;
    align-items: center;
    gap: var(--app-space-2);
    margin: 0 0 var(--app-space-2);
    color: var(--td-text-color-secondary);
    font-size: var(--app-text-md);

    .r {
      margin-left: auto;
      font-weight: 400;
      font-size: var(--app-text-sm);
      color: var(--td-text-color-placeholder);
    }
  }
}

.pg-empty {
  margin: 0 0 var(--app-space-2);
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
}

.pg-stat-line {
  margin: 0 0 var(--app-space-3);
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
}

// 产品认知：字段纵向排布，标签在上、输入框在下，便于长文本编辑
.pg-cognition {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-3);
}

.pg-cognition-item {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-1);
}

.pg-cognition-label {
  font-size: var(--app-text-sm);
  font-weight: 600;
  color: var(--td-text-color-secondary);
}

// 子系统输入：由用户指定，作为功能清单第一列的默认值
.pg-subsystem-bar {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  margin-bottom: var(--app-space-2);
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);

  > label {
    white-space: nowrap;
  }
}

.pg-subsystem-input {
  flex: 1;
  min-width: 0;
}

.pg-tree-subsystem {
  margin-right: 6px;
  padding: 0 6px;
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-secondarycontainer);
  font-size: var(--app-text-xs);
  font-weight: 400;
  color: var(--td-text-color-secondary);
}

.pg-tree-module {
  margin-bottom: var(--app-space-3);
  padding: var(--app-space-2) var(--app-space-3);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-sm);
}

.pg-tree-module-name {
  margin-bottom: var(--app-space-2);
  font-size: var(--app-text-md);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.pg-tree-feature {
  padding-left: var(--app-space-3);
  margin-bottom: var(--app-space-2);
}

.pg-tree-feature-name {
  margin-bottom: 4px;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
}

.pg-tree-ops {
  display: flex;
  flex-wrap: wrap;
  gap: var(--app-space-1);
}

.pg-scope-bar {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  margin-bottom: var(--app-space-2);
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);

  > span {
    margin-right: auto;
  }
}

.pg-scope-group {
  margin-bottom: var(--app-space-3);
}

.pg-scope-head {
  margin-bottom: var(--app-space-1);
  font-size: var(--app-text-md);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.pg-scope-items {
  display: flex;
  flex-wrap: wrap;
  gap: var(--app-space-2);
}

.pg-summary {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(72px, 1fr));
  gap: var(--app-space-1);
  margin-bottom: var(--app-space-3);
}

.pg-summary-item {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 2px;
  padding: var(--app-space-2) 0;
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-secondarycontainer);

  b {
    font-size: var(--app-text-xl);
    color: var(--td-brand-color);
  }

  span {
    font-size: var(--app-text-xs);
    color: var(--td-text-color-placeholder);
  }
}

.pg-info {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  gap: var(--app-space-1) var(--app-space-3);
  margin: 0 0 var(--app-space-3);
  padding: var(--app-space-2) var(--app-space-3);
  border: 1px dashed var(--td-component-stroke);
  border-radius: var(--app-radius-sm);
  font-size: var(--app-text-sm);

  dt {
    color: var(--td-text-color-placeholder);
    white-space: nowrap;
  }

  dd {
    margin: 0;
    color: var(--td-text-color-primary);
    word-break: break-all;
  }
}

.pg-path {
  font-family: var(--td-font-family-mono, monospace);
}

.pg-note {
  margin: var(--app-space-1) 0 0;
  font-size: var(--app-text-sm);
  line-height: 1.6;
  color: var(--td-text-color-placeholder);
}

.pg-plan-foot {
  margin-top: var(--app-space-4);
  padding-top: var(--app-space-3);
  border-top: 1px solid var(--td-component-stroke);

  .pg-note {
    text-align: center;
  }
}
</style>
