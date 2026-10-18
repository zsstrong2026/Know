<template>
  <div class="meeting-room">
    <!-- 顶栏：会议标题 + 元信息 + 操作按钮（排版对齐「新对话」的 topbar） -->
    <header class="room-topbar">
      <div class="tb-main">
        <div class="tb-title">{{ store.currentRoom?.title }}</div>
        <div v-if="store.currentRoom?.description" class="tb-desc">{{ store.currentRoom.description }}</div>
      </div>
      <div class="tb-actions">
        <t-button size="small" theme="primary" :loading="store.solutionGenerating"
          :disabled="store.discussing || isArchived" @click="generateSolution">
          {{ $t('meeting.generateSolution') }}
        </t-button>
        <t-button size="small" theme="default" variant="outline" :disabled="!store.discussing" @click="interrupt">
          {{ $t('meeting.interrupt') }}
        </t-button>
        <t-button size="small" theme="default" variant="outline" :disabled="isArchived" @click="showGenerate = true">
          {{ $t('meeting.generateArtifact') }}
        </t-button>
        <t-button size="small" theme="default" variant="outline" @click="showArtifacts = true">
          {{ $t('meeting.artifacts') }}
        </t-button>
      </div>
    </header>

    <!-- 归档只读态提示条 -->
    <t-alert v-if="isArchived" :message="$t('meeting.archivedBanner')" theme="info" class="archived-banner" />

    <!-- 待确认提示条：开场流程尚未确认 -->
    <t-alert v-if="isAwaiting" :message="$t('meeting.awaitingBanner')" theme="warning" class="archived-banner" />

    <div ref="scrollRef" class="msg-scroll">
      <div class="msg-list">
        <div v-if="store.guardNotice" class="guard-notice">
          <span>{{ $t('meeting.guardNotice') }}</span>
          <t-button size="small" variant="text" @click="store.clearGuardNotice()">✕</t-button>
        </div>
        <!-- 裁判判定收敛提示：讨论已结束，提示用户可继续追问或生成方案 -->
        <div v-if="store.consensusNotice" class="consensus-notice">
          <span>{{ $t('meeting.consensusNotice') }}</span>
          <t-button size="small" variant="text" @click="store.clearConsensusNotice()">✕</t-button>
        </div>
        <div v-if="store.hasMore" class="load-earlier" @click="store.loadEarlier()">
          {{ $t('meeting.loadEarlier') }}
        </div>

        <template v-for="m in store.messages" :key="m.id">
          <!-- 系统消息：居中弱化胶囊 -->
          <div v-if="m.sender_kind === 'system'" class="sys-msg">
            <span>{{ m.content }}</span>
          </div>

          <!-- 用户消息：右对齐纯文本（不做 Markdown 解析，避免曲解输入） -->
          <div v-else-if="m.sender_kind === 'user'" class="msg is-user">
            <div class="msg-body">
              <div class="sender is-user">{{ $t('meeting.senderMe') }}</div>
              <div class="bubble is-plain">{{ m.content }}</div>
            </div>
          </div>

          <!-- 智能体 / 主持人消息：左对齐头像 + Markdown 渲染（引用标签转 chip） -->
          <div v-else class="msg">
            <div class="avatar" :style="{ background: avatarColor(m.sender_id) }">{{ avatarText(m) }}</div>
            <div class="msg-body">
              <div class="sender">
                <span class="sender-name">{{ senderLabel(m) }}</span>
                <t-tag v-if="m.is_solution" size="small" theme="primary" variant="light">
                  {{ $t('meeting.solution') }} v{{ m.solution_version }}
                </t-tag>
                <t-tag v-else-if="m.is_consensus" size="small" theme="success" variant="light">
                  {{ $t('meeting.consensus') }}
                </t-tag>
                <t-tag v-else-if="m.sender_kind === 'agent'" size="small" variant="light">
                  {{ $t('meeting.senderAgent') }}
                </t-tag>
              </div>
              <div class="bubble markdown-content"
                :class="{ 'is-solution': m.is_solution, 'is-consensus': m.is_consensus }"
                v-html="renderMarkdown(m)" />
              <!-- 评审操作条：仅智能体讨论消息可评审，方案消息不参与；归档后禁用 -->
              <div v-if="m.sender_kind === 'agent' && !m.is_solution" class="review-bar">
                <t-button size="small" theme="success" :variant="m.review_state === 'adopted' ? 'base' : 'outline'"
                  :disabled="isArchived" @click="review(m, 'adopted')">{{ $t('meeting.adopt') }}</t-button>
                <t-button size="small" theme="danger" :variant="m.review_state === 'rejected' ? 'base' : 'outline'"
                  :disabled="isArchived" @click="review(m, 'rejected')">{{ $t('meeting.reject') }}</t-button>
                <t-button size="small" theme="warning" :variant="m.review_state === 'pending' ? 'base' : 'outline'"
                  :disabled="isArchived" @click="review(m, 'pending')">{{ $t('meeting.pending') }}</t-button>
              </div>
              <!-- 开场确认按钮组：仅待确认状态下、最近一条开场消息下方展示 -->
              <div v-if="m.sender_kind === 'host' && isLatestOpening(m) && isAwaiting" class="opening-actions">
                <t-button size="small" theme="primary" :loading="confirming" :disabled="adjustMode"
                  @click="startMeeting">
                  {{ $t('meeting.startMeeting') }}
                </t-button>
                <t-button size="small" theme="default" variant="outline" :class="{ 'is-active': adjustMode }"
                  @click="adjustMode = !adjustMode">
                  {{ $t('meeting.adjustFlow') }}
                </t-button>
              </div>
            </div>
          </div>
        </template>

        <div v-if="store.discussing" class="discussing">
          <t-loading size="small" />
          <span>{{ $t('meeting.discussing') }}</span>
        </div>
      </div>
    </div>

    <ArtifactDrawer v-model:visible="showArtifacts" :room-id="roomId"
      :current-solution-message-id="store.currentRoom?.current_solution_message_id ?? ''" />

    <ArtifactGenerateDialog v-model:visible="showGenerate" @requested="showArtifacts = true" />

    <footer class="input-bar">
      <div class="input-inner">
        <!-- 已选 @ 的智能体 chip，可点关闭移除 -->
        <div v-if="mentions.length" class="mention-chips">
          <t-tag v-for="m in mentions" :key="m.agent_id" size="small" closable theme="primary" variant="light"
            @close="removeMention(m.agent_id)">
            @{{ m.display_name }}
          </t-tag>
        </div>
        <div v-if="adjustMode" class="adjust-chip">
          <t-tag size="small" theme="warning" variant="light" closable @close="adjustMode = false">
            {{ $t('meeting.adjustFlowMode') }}
          </t-tag>
        </div>
        <!-- t-textarea 的 keyup 会以组件参数回调（非原生事件），故在其外层原生 div 上监听并借冒泡触发 -->
        <div class="editor" @keyup.ctrl.enter="send">
          <t-textarea v-model="draft" :autosize="{ minRows: 2, maxRows: 6 }"
            :disabled="store.discussing || isArchived" :placeholder="inputPlaceholder" />
        </div>
        <div class="input-actions">
          <MentionPicker @pick="addMention" />
          <t-button theme="primary" :disabled="store.discussing || isArchived || !draft.trim()" @click="send">
            {{ $t('meeting.send') }}
          </t-button>
        </div>
      </div>
    </footer>

    <!-- 引用 chip 的 chunk 原文浮层（组件内部已 Teleport 到 body） -->
    <ChatCitationFloat :float="citationFloat" :on-enter="cancelCitationClose" :on-leave="scheduleCitationClose" />
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute } from 'vue-router';
import { MessagePlugin } from 'tdesign-vue-next';
import { useMeetingStore } from '@/stores/meeting';
import { interruptMeeting, openMeetingStream } from '@/api/meeting';
import type { MeetingMessage, MeetingParticipant } from '@/api/meeting';
import { createChatMarkdownRenderer, renderChatMarkdown } from '@/utils/chatMarkdownRenderer';
import { safeMarkdownToHTML, sanitizeMarkdownHTML } from '@/utils/security';
import { useChatCitationPopover } from '@/composables/useChatCitationPopover';
import ChatCitationFloat from '@/components/ChatCitationFloat.vue';
import MentionPicker from './components/MentionPicker.vue';
import ArtifactDrawer from './ArtifactDrawer.vue';
import ArtifactGenerateDialog from './components/ArtifactGenerateDialog.vue';

const route = useRoute();
const { t } = useI18n();
const store = useMeetingStore();

const draft = ref('');
// 本次发言 @ 提及的智能体（仅视图本地状态，发送时取其 agent_id 传给后端）
const mentions = ref<MeetingParticipant[]>([]);
const showArtifacts = ref(false); // 历史产物抽屉可见性（视图本地状态，不入 store）
const showGenerate = ref(false); // 产物生成弹窗可见性（视图本地状态）
const scrollRef = ref<HTMLElement | null>(null);
let closeStream: (() => void) | null = null;

// 一体化页面下切换会议只变路由参数、组件实例复用，故 roomId 需为响应式
const roomId = computed(() => (route.params.roomId as string) || '');

// 归档只读态：输入/发送/评审/生成方案全部禁用，SSE 仍保持接收（归档系统消息可推送到场）
const isArchived = computed(() => store.currentRoom?.status === 'archived');

// 待确认态：等待用户确认开场流程（确认后转 active）
const isAwaiting = computed(() => store.currentRoom?.status === 'awaiting_confirmation');
const confirming = ref(false); // 「开始会议」按钮 loading
const adjustMode = ref(false); // 「调整流程」输入模式

const inputPlaceholder = computed(() =>
  adjustMode.value ? t('meeting.adjustFlowPlaceholder') : t('meeting.inputPlaceholder'));

// Markdown 渲染器与结果缓存：同一消息内容未变时直接复用 HTML，避免重复 parse
const markdownRenderer = createChatMarkdownRenderer();
const renderCache = new Map<string, { content: string; html: string }>();

function renderMarkdown(m: MeetingMessage): string {
  const raw = m.content || '';
  const hit = renderCache.get(m.id);
  if (hit && hit.content === raw) return hit.html;
  const html = renderChatMarkdown(raw, {
    renderer: markdownRenderer,
    escapeMarkdown: safeMarkdownToHTML,
    sanitizeHtml: sanitizeMarkdownHTML,
    streaming: false,
  });
  renderCache.set(m.id, { content: raw, html });
  return html;
}

// 引用 chip 的 hover/click 交互：根容器上的事件代理，消息动态新增无需重新绑定
const { float: citationFloat, cancelClose: cancelCitationClose, scheduleClose: scheduleCitationClose }
  = useChatCitationPopover(scrollRef);

onMounted(() => {
  void openRoom(roomId.value);
});

// 侧栏切换会议：重新拉取房间与消息并重建 SSE 连接
watch(roomId, (id) => {
  void openRoom(id);
});

onBeforeUnmount(() => {
  closeStream?.();
  store.leaveRoom();
});

// 监听最后一条消息 id：prepend 加载更早消息时末条 id 不变，仅追加时滚动到底部
watch(() => store.messages[store.messages.length - 1]?.id, () => nextTick(scrollToBottom));

async function openRoom(id: string) {
  if (!id) return;
  // 切换房间前先断开旧连接并清空视图本地状态，避免串台
  closeStream?.();
  closeStream = null;
  renderCache.clear();
  mentions.value = [];
  draft.value = '';
  showArtifacts.value = false;
  showGenerate.value = false;
  adjustMode.value = false;
  confirming.value = false;
  store.leaveRoom();
  try {
    await store.enterRoom(id);
  } catch {
    MessagePlugin.error(t('meeting.loadFailed'));
    return; // 仅在加载成功后才建立 SSE，避免空白页
  }
  await nextTick();
  scrollToBottom();
  closeStream = openMeetingStream(id, {
    onEvent: (event, data) => store.applyStreamEvent(event, data),
    onError: () => { /* M1：视图显示离线提示即可，重连留给 M2 */ },
  });
}

function scrollToBottom() {
  const el = scrollRef.value;
  if (el) el.scrollTop = el.scrollHeight;
}

// 统一错误提示：后端对归档房间的写操作返回 403（message 含 archived），映射为只读提示
function toastError(e: unknown) {
  const msg = (e as Error)?.message || '';
  if (msg.includes('archived')) {
    MessagePlugin.warning(t('meeting.archivedForbidden'));
    return;
  }
  MessagePlugin.error(msg || t('meeting.loadFailed'));
}

function senderLabel(m: MeetingMessage): string {
  if (m.sender_kind === 'host') return t('meeting.senderHost');
  if (m.sender_kind === 'system') return t('meeting.senderSystem');
  if (m.sender_kind === 'user') return t('meeting.senderMe');
  const p = store.currentRoom?.participants?.find(
    x => x.kind === 'agent' && x.agent_id === m.sender_id);
  return p?.display_name ?? m.sender_id.slice(0, 8);
}

// 头像取发送者首字；系统/主持人/智能体均适配
function avatarText(m: MeetingMessage): string {
  const label = senderLabel(m).trim();
  return label ? label.charAt(0).toUpperCase() : '?';
}

// 由 sender_id 稳定哈希出头像底色，保证同一智能体颜色固定
function avatarColor(seed: string): string {
  let hash = 0;
  for (let i = 0; i < seed.length; i += 1) hash = (hash * 31 + seed.charCodeAt(i)) % 360;
  return `hsl(${hash} 58% 52%)`;
}

// 点击候选智能体加入 chip（按 agent_id 去重）
function addMention(p: MeetingParticipant) {
  if (!mentions.value.some(m => m.agent_id === p.agent_id)) {
    mentions.value.push(p);
  }
}

function removeMention(agentId: string) {
  mentions.value = mentions.value.filter(m => m.agent_id !== agentId);
}

// 开场消息判定与「最近一条」限定（调整流程会产生多条 opening，只渲染最后一组的按钮）
function isOpening(m: MeetingMessage): boolean {
  return (m.payload as { stage?: string } | null | undefined)?.stage === 'opening';
}

function isLatestOpening(m: MeetingMessage): boolean {
  if (!isOpening(m)) return false;
  const list = store.messages;
  for (let i = list.length - 1; i >= 0; i -= 1) {
    if (isOpening(list[i])) return list[i].id === m.id;
  }
  return false;
}

async function startMeeting() {
  confirming.value = true;
  try {
    await store.confirm();
  } catch (e) {
    toastError(e);
  } finally {
    confirming.value = false;
  }
}

async function send() {
  const content = draft.value.trim();
  if (!content) return;
  const ids = mentions.value.map(m => m.agent_id);
  draft.value = '';
  mentions.value = [];                            // 发送后清空已选 @ 的智能体
  try {
    await store.speak(content, ids, adjustMode.value);
    adjustMode.value = false;
  } catch (e) {
    draft.value = content;                          // 失败时回填，避免丢内容
    toastError(e);
  }
}

// 评审操作：错误 toast；状态由 store 本地更新 + SSE 幂等覆盖
async function review(m: MeetingMessage, state: string) {
  try {
    await store.setReviewState(m.id, state);
  } catch (e) {
    toastError(e);
  }
}

// 生成方案：错误提示在视图层（如无采纳消息的后端 400）；loading 复位在 store 的 finally 中完成
async function generateSolution() {
  try {
    await store.generate();
  } catch (e) {
    toastError(e);
  }
}

// 主动打断进行中的讨论轮
async function interrupt() {
  try {
    await interruptMeeting(roomId.value);
  } catch (e) {
    toastError(e);
  }
}
</script>

<style lang="less" scoped>
// Markdown 排版与引用 chip 样式复用「新对话」的单一来源，会议室内保持一致观感
@import '../../components/css/chat-markdown.less';
@import '../../components/css/chat-citations.less';

.meeting-room {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  background: var(--td-bg-color-container);
}

/* ---------- 顶栏 ---------- */
.room-topbar {
  display: flex;
  align-items: center;
  gap: var(--app-space-3);
  flex: 0 0 56px;
  height: 56px;
  padding: 0 var(--app-space-4) 0 var(--app-space-5);
  border-bottom: 1px solid var(--td-component-stroke);
}

.tb-main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  justify-content: center;
}

.tb-title {
  font-size: var(--app-text-lg);
  font-weight: 600;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tb-desc {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tb-actions {
  flex-shrink: 0;
  display: flex;
  gap: var(--app-space-2);
}

.archived-banner {
  flex: 0 0 auto;
  margin: var(--app-space-2) var(--app-space-5) 0;
}

/* ---------- 消息滚动区 ---------- */
.msg-scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  overscroll-behavior-y: contain;
  scrollbar-gutter: stable;
  scrollbar-width: thin;
  padding: var(--app-space-4) 0 var(--app-space-2);
}

.msg-list {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-4);
  width: 100%;
  max-width: 820px;
  margin: 0 auto;
  padding: 0 var(--app-space-5);
}

/* 系统消息：居中弱化胶囊 */
.sys-msg {
  text-align: center;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);

  span {
    display: inline-block;
    padding: 3px var(--app-space-3);
    border-radius: var(--app-radius-pill);
    background: var(--td-bg-color-page);
  }
}

.load-earlier {
  align-self: center;
  cursor: pointer;
  font-size: var(--app-text-sm);
  color: var(--td-brand-color);
}

.guard-notice {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  padding: var(--app-space-2) var(--app-space-3);
  border-radius: var(--app-radius-md);
  background: #fff8e1; /* 淡黄底：主持人介入提示 */
  color: #8d6e00;
  font-size: var(--app-text-md);
}

/* 淡绿底：裁判判定收敛提示，与护栏提示区分 */
.consensus-notice {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  padding: var(--app-space-2) var(--app-space-3);
  border-radius: var(--app-radius-md);
  background: #e8f5e9;
  color: #1b6b3a;
  font-size: var(--app-text-md);
}

.discussing {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--app-space-2);
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);
}

/* ---------- 消息气泡 ---------- */
.msg {
  display: flex;
  gap: var(--app-space-3);

  &.is-user {
    flex-direction: row-reverse;
  }

  &.is-user .msg-body {
    align-items: flex-end;
    max-width: 70%;
  }
}

.avatar {
  flex: 0 0 32px;
  width: 32px;
  height: 32px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: var(--app-radius-md);
  color: #fff;
  font-size: var(--app-text-md);
  font-weight: 500;
  user-select: none;
}

.msg-body {
  min-width: 0;
  max-width: 100%;
  display: flex;
  flex-direction: column;
  gap: var(--app-space-1);
}

.sender {
  display: flex;
  align-items: center;
  gap: var(--app-space-1);
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);

  &.is-user {
    justify-content: flex-end;
  }
}

.sender-name {
  font-weight: 500;
}

.bubble {
  padding: var(--app-space-3) var(--app-space-4);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
  background: var(--td-bg-color-container);
  font-size: var(--app-text-base);
  line-height: 1.7;
  word-break: break-word;

  /* 用户气泡：纯文本 + 品牌色实底 */
  &.is-plain {
    white-space: pre-wrap;
    background: var(--td-brand-color);
    border-color: var(--td-brand-color);
    color: #fff;
  }

  /* 方案气泡：品牌色浅底 + 描边，与讨论消息区分 */
  &.is-solution {
    background: color-mix(in srgb, var(--td-brand-color) 6%, transparent);
    border-color: color-mix(in srgb, var(--td-brand-color) 22%, transparent);
  }

  /* 共识气泡：成功色浅底 + 描边，标识裁判判定的收敛结论 */
  &.is-consensus {
    background: color-mix(in srgb, var(--td-success-color) 6%, transparent);
    border-color: color-mix(in srgb, var(--td-success-color) 24%, transparent);
  }
}

// Markdown 排版 + 引用 chip（body 字号按气泡 14px，避免气泡内文字过大）
.markdown-content {
  .chat-markdown-typography();
  .chat-citation-pills();

  font-size: var(--app-text-base);
}

.review-bar {
  display: flex;
  gap: var(--app-space-1);
}

.opening-actions {
  display: flex;
  gap: var(--app-space-2);
}

.adjust-chip {
  display: flex;
  margin-bottom: var(--app-space-2);
}

/* ---------- 输入区 ---------- */
.input-bar {
  flex: 0 0 auto;
  padding: var(--app-space-3) var(--app-space-5) var(--app-space-4);
  border-top: 1px solid var(--td-component-stroke);
}

.input-inner {
  max-width: 820px;
  margin: 0 auto;
}

.mention-chips {
  display: flex;
  flex-wrap: wrap;
  gap: var(--app-space-1);
  margin-bottom: var(--app-space-2);
}

.input-actions {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  margin-top: var(--app-space-2);
}
</style>
