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

    <!-- 议程条：开启结构性治理且已拆解议程时展示，回答"讨论到哪、还剩什么" -->
    <div v-if="store.currentRoom?.agenda_enabled && store.agenda.length" class="agenda-bar">
      <div class="agenda-summary">
        <span class="agenda-progress">
          {{ $t('meeting.agendaProgress', { current: agendaCurrentIndex, total: store.agenda.length }) }}
        </span>
        <span v-if="store.activeAgendaItem" class="agenda-current">
          {{ store.activeAgendaItem.title }}
        </span>
      </div>
      <div class="agenda-items">
        <t-tag v-for="item in store.agenda" :key="item.id" size="small" variant="light"
          :theme="agendaTagTheme(item.status)" :title="item.goal">
          {{ item.seq + 1 }}. {{ item.title }}
        </t-tag>
      </div>
      <t-button size="small" theme="default" variant="outline" :loading="advancingAgenda"
        :disabled="store.discussing || isArchived || !store.activeAgendaItem" @click="nextAgenda">
        {{ $t('meeting.endAgenda') }}
      </t-button>
    </div>

    <div ref="scrollRef" class="msg-scroll">
      <div class="msg-list">
        <div v-if="store.guardNotice" class="guard-notice">
          <span>{{ $t('meeting.guardNotice') }}</span>
          <t-button size="small" variant="text" @click="store.clearGuardNotice()">✕</t-button>
        </div>
        <!-- 裁判判定收敛：讨论结束，由输入区上方的「下一步」决策坞引导会议走向 -->
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
                <!-- 发言准入结果：observed=仅记录、rewritten=按未通过原因重写过，便于观察治理效果 -->
                <t-tag v-if="m.admission_state === 'observed' || m.admission_state === 'rewritten'"
                  size="small" variant="outline" theme="warning" :title="m.admission_note">
                  {{ m.admission_state === 'rewritten' ? $t('meeting.admissionRewritten') : $t('meeting.admissionObserved') }}
                </t-tag>
              </div>
              <div class="bubble markdown-content"
                :class="{ 'is-solution': m.is_solution, 'is-consensus': m.is_consensus }"
                v-html="renderMarkdown(m)" />
            </div>
          </div>
        </template>

        <div v-if="store.discussing" class="discussing">
          <t-loading size="small" />
          <span>{{ $t('meeting.discussing') }}</span>
        </div>

        <!-- 主持人分析议题中：开场消息尚未回流，先在对话框内给出明确等待提示（避免空白无反馈） -->
        <div v-if="openingWaiting" class="discussing">
          <t-loading size="small" />
          <span>{{ $t('meeting.openingAnalyzing') }}</span>
        </div>
      </div>
    </div>

    <ArtifactDrawer v-model:visible="showArtifacts" :room-id="roomId"
      :current-solution-message-id="store.currentRoom?.current_solution_message_id ?? ''" />

    <ArtifactGenerateDialog v-model:visible="showGenerate" @requested="showArtifacts = true" />

    <!-- 开场确认坞：待确认态常驻输入区上方，免去回滚消息找按钮；两种语义（澄清 / 确认）共用一处 -->
    <div v-if="isAwaiting" class="confirm-dock">
      <div class="confirm-dock__main">
        <div class="confirm-dock__title">
          <t-icon name="chat-bubble-smile" />
          <span>{{ needsClarification ? $t('meeting.confirmDockClarifyTitle') : $t('meeting.confirmDockTitle') }}</span>
        </div>
        <p class="confirm-dock__desc">
          {{ needsClarification ? $t('meeting.confirmDockClarifyDesc') : $t('meeting.confirmDockDesc') }}
        </p>
        <!-- 已收敛主题：让用户先核对主持人理解的是否为他要讨论的议题 -->
        <p v-if="topicBrief" class="confirm-dock__topic">
          <span class="confirm-dock__topic-label">{{ $t('meeting.confirmDockTopic') }}</span>
          <span>{{ topicBrief }}</span>
        </p>
      </div>
      <div class="confirm-dock__actions">
        <t-button theme="primary" :loading="confirming" :disabled="adjustMode" @click="startMeeting">
          {{ needsClarification ? $t('meeting.skipClarifyStart') : $t('meeting.startMeeting') }}
        </t-button>
        <t-button theme="default" variant="outline" :disabled="confirming" @click="toggleAdjustMode">
          {{ adjustMode ? $t('meeting.cancelAdjustFlow') : $t('meeting.adjustFlow') }}
        </t-button>
      </div>
    </div>

    <!-- 共识后的「下一步」：收敛即引导会议走向（生成方案 / 补充讨论 / 追加议题 / 结束会议），替代旧的采纳评审 -->
    <div v-if="showNextStepDock" class="next-step-dock">
      <div class="next-step-dock__main">
        <div class="next-step-dock__title">
          <t-icon name="check-circle" />
          <span>{{ $t('meeting.nextStepTitle') }}</span>
        </div>
        <p class="next-step-dock__desc">{{ $t('meeting.nextStepDesc') }}</p>
        <!-- 追加新议题：就地输入标题与目标，确认后才创建并开题 -->
        <div v-if="appendingAgenda" class="next-step-dock__form">
          <t-input v-model="newAgendaTitle" size="small" :maxlength="40"
            :placeholder="$t('meeting.newAgendaTitlePlaceholder')" />
          <t-input v-model="newAgendaGoal" size="small" :maxlength="120"
            :placeholder="$t('meeting.newAgendaGoalPlaceholder')" />
        </div>
      </div>
      <div class="next-step-dock__actions">
        <template v-if="appendingAgenda">
          <t-button size="small" theme="primary" :loading="agendaActing" :disabled="!newAgendaTitle.trim()"
            @click="submitNewAgenda">{{ $t('meeting.confirmAppendAgenda') }}</t-button>
          <t-button size="small" theme="default" variant="outline" :disabled="agendaActing" @click="cancelAppendAgenda">
            {{ $t('meeting.cancel') }}
          </t-button>
        </template>
        <template v-else>
          <t-button size="small" theme="primary" :loading="store.solutionGenerating" @click="generateSolution">
            {{ $t('meeting.nextStepGenerate') }}
          </t-button>
          <t-button v-if="store.agenda.length" size="small" theme="default" variant="outline" :loading="agendaActing"
            :disabled="!supplementItemId" @click="continueAgenda">{{ $t('meeting.nextStepSupplement') }}</t-button>
          <t-button size="small" theme="default" variant="outline" :disabled="agendaActing" @click="openAppendAgenda">
            {{ $t('meeting.nextStepAppend') }}
          </t-button>
          <t-button size="small" theme="default" variant="text" @click="endMeeting">
            {{ $t('meeting.nextStepEnd') }}
          </t-button>
        </template>
      </div>
    </div>

    <!-- 智能体在讨论中向你提问：讨论已暂停，直接回复即可继续 -->
    <div v-if="store.awaitingUser && !isArchived" class="await-user-dock">
      <div class="await-user-dock__title">
        <t-icon name="chat-bubble-smile" />
        <span>{{ $t('meeting.awaitingUserTitle', { name: senderLabel(store.awaitingUser) }) }}</span>
      </div>
      <p class="await-user-dock__desc">{{ $t('meeting.awaitingUserDesc') }}</p>
    </div>

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
        <!-- 澄清态提示：告知用户当前输入用于补充会议主题（回答后主持人重出开场） -->
        <div v-else-if="needsClarification" class="adjust-chip">
          <t-tag size="small" theme="primary" variant="light">
            {{ $t('meeting.clarifyMode') }}
          </t-tag>
        </div>
        <!-- t-textarea 的 keyup 会以组件参数回调（非原生事件），故在其外层原生 div 上监听并借冒泡触发 -->
        <div class="editor" @keyup.ctrl.enter="send">
          <t-textarea ref="draftRef" v-model="draft" :autosize="{ minRows: 2, maxRows: 6 }"
            :disabled="isArchived" :placeholder="inputPlaceholder" />
        </div>
        <div class="input-actions">
          <MentionPicker @pick="addMention" />
          <t-button theme="primary" :disabled="isArchived || !draft.trim()" @click="send">
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
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next';
import { useMeetingStore } from '@/stores/meeting';
import { interruptMeeting, openMeetingStream } from '@/api/meeting';
import type { MeetingMessage, MeetingParticipant } from '@/api/meeting';
import { createChatMarkdownRenderer, renderChatMarkdown } from '@/utils/chatMarkdownRenderer';
import { safeMarkdownToHTML, sanitizeMarkdownHTML } from '@/utils/security';
import { useChatCitationPopover } from '@/composables/useChatCitationPopover';
import { useChatReferencesDrawer } from '@/composables/useChatReferencesDrawer';
import { getChunkByIdOnly } from '@/api/knowledge-base';
import type { KnowledgeReferenceLike, ReferenceHighlightTarget } from '@/utils/referenceSources';
import ChatCitationFloat from '@/components/ChatCitationFloat.vue';
import MentionPicker from './components/MentionPicker.vue';
import ArtifactDrawer from './ArtifactDrawer.vue';
import ArtifactGenerateDialog from './components/ArtifactGenerateDialog.vue';

const route = useRoute();
const { t } = useI18n();
const store = useMeetingStore();

const draft = ref('');
const draftRef = ref<{ focus?: () => void } | null>(null); // 「调整流程」时把光标送进输入框
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

// 主持人分析议题中的等待态；超时后不再阻塞确认坞，避免 SSE 断流时用户被永久卡在等待中
const openingWaitTimedOut = ref(false);
const openingWaiting = computed(() => store.openingAnalyzing && !openingWaitTimedOut.value);
let openingWaitTimer: ReturnType<typeof setTimeout> | null = null;
function clearOpeningWaitTimer() {
  if (openingWaitTimer) clearTimeout(openingWaitTimer);
  openingWaitTimer = null;
}
// 只观察 store 侧的等待态：新一次分析开始时重置超时标记并重新计时
watch(() => store.openingAnalyzing, (analyzing) => {
  clearOpeningWaitTimer();
  openingWaitTimedOut.value = false;
  if (analyzing) openingWaitTimer = setTimeout(() => { openingWaitTimedOut.value = true; }, 120000);
}, { immediate: true });
onBeforeUnmount(clearOpeningWaitTimer);

// 待确认态：等待用户确认开场流程（确认后转 active）
// 主持人仍在分析议题（openingWaiting）时不展示确认坞，避免「分析未完成就先让用户开始会议」
const isAwaiting = computed(() => store.currentRoom?.status === 'awaiting_confirmation' && !openingWaiting.value);
const confirming = ref(false); // 「开始会议」按钮 loading
const adjustMode = ref(false); // 「调整流程」输入模式

// 切换调整模式：进入时聚焦输入框，让「去哪输入」这件事无需猜测
async function toggleAdjustMode() {
  adjustMode.value = !adjustMode.value;
  if (!adjustMode.value) return;
  await nextTick();
  draftRef.value?.focus?.();
}

// 待确认态展示的主持人收敛主题（未澄清时为空，仅展示原始标题语义）
const topicBrief = computed(() => (store.currentRoom?.topic_brief || '').trim());

// 澄清态：待确认 + 最近开场标记 needs_clarification，此时输入框语义为「补充说明主题」
const needsClarification = computed(() => isAwaiting.value && store.needsClarification);

// ---- 议程条 ----
const advancingAgenda = ref(false);
// 当前进度序号：已完成数 + 1（全部完成时停在总数）
const agendaCurrentIndex = computed(() =>
  Math.min(store.agendaDoneCount + 1, store.agenda.length || 1));
// 状态 → tdesign tag 主题：待讨论中性、讨论中主色、已覆盖成功、已跳过弱化
function agendaTagTheme(status: string): 'default' | 'primary' | 'success' | 'warning' {
  switch (status) {
    case 'active': return 'primary';
    case 'done': return 'success';
    case 'skipped': return 'warning';
    default: return 'default';
  }
}
async function nextAgenda() {
  if (!store.activeAgendaItem) return;
  advancingAgenda.value = true;
  try {
    await store.advanceAgenda(store.activeAgendaItem.id);
  } catch (e) {
    toastError(e);
  } finally {
    advancingAgenda.value = false;
  }
}

// ---- 共识后的「下一步」决策坞 ----
const appendingAgenda = ref(false);   // 追加议题内联表单是否展开
const newAgendaTitle = ref('');
const newAgendaGoal = ref('');
const agendaActing = ref(false);      // 追加/恢复议题请求进行中

// 收敛提示到达即展开决策坞；但只有"全部议题都已完成"时才引导会议走向，
// 议题中途收束由主持人自动过渡到下一议题，不再打断用户节奏。
const allAgendaSettled = computed(() =>
  !store.agenda.length || store.agenda.every(it => it.status === 'done' || it.status === 'skipped'));
const showNextStepDock = computed(() => !!store.consensusNotice && allAgendaSettled.value && !isArchived.value);

// 「补充讨论」目标议题：优先最近一个已结束议题（收敛通常发生在其结束后），无则退回活跃议题
const supplementItemId = computed(() => {
  const list = store.agenda;
  for (let i = list.length - 1; i >= 0; i -= 1) {
    if (list[i].status === 'done') return list[i].id;
  }
  return store.activeAgendaItem?.id ?? '';
});

function openAppendAgenda() {
  appendingAgenda.value = true;
  newAgendaTitle.value = '';
  newAgendaGoal.value = '';
}

function cancelAppendAgenda() {
  appendingAgenda.value = false;
}

// 议题操作统一收口：loading 开关 + 错误提示，避免多处重复
async function runAgendaAction(action: () => Promise<void>) {
  agendaActing.value = true;
  try {
    await action();
  } catch (e) {
    toastError(e);
  } finally {
    agendaActing.value = false;
  }
}

// 追加议题：后端落开题消息并经 SSE 回流，成功后收起表单（store 同时清掉收敛提示）
async function submitNewAgenda() {
  const title = newAgendaTitle.value.trim();
  if (!title) return;
  await runAgendaAction(async () => {
    await store.appendAgenda(title, newAgendaGoal.value.trim());
    appendingAgenda.value = false;
  });
}

// 继续讨论本议题：恢复最近完成议题为 active，后端重新开题并跑一轮讨论
async function continueAgenda() {
  const itemId = supplementItemId.value;
  if (!itemId) return;
  await runAgendaAction(() => store.reopenAgenda(itemId));
}

// 结束会议：归档前二次确认，避免误触导致只读
function endMeeting() {
  const dialog = DialogPlugin.confirm({
    header: t('meeting.endMeetingTitle'),
    body: t('meeting.endMeetingDesc'),
    theme: 'warning',
    confirmBtn: t('meeting.endMeetingConfirm'),
    cancelBtn: t('meeting.cancel'),
    onConfirm: async () => {
      dialog.destroy();
      try {
        await store.archive();
      } catch (e) {
        toastError(e);
      }
    },
  });
}

const inputPlaceholder = computed(() => {
  if (adjustMode.value) return t('meeting.adjustFlowPlaceholder');
  if (needsClarification.value) return t('meeting.clarifyPlaceholder');
  // 讨论进行中不再锁输入：用户可随时插话，被 @ 的智能体会优先回应
  if (store.discussing) return t('meeting.speakingPlaceholder');
  return t('meeting.inputPlaceholder');
});

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

// 会议室消息里的引用 chip 只带 chunk id，点击时按需取回 chunk 元信息，
// 合成侧边栏「原位视图」所需的引用对象（与「新对话」的引用结构对齐）
async function loadCitationReferences(target: ReferenceHighlightTarget): Promise<KnowledgeReferenceLike[]> {
  const chunkId = String(target.chunkId || '').trim();
  if (!chunkId) return [];
  try {
    const res = await getChunkByIdOnly(chunkId) as { data?: Record<string, any> };
    const data = res?.data;
    if (!data?.knowledge_id) return [];
    return [{
      id: chunkId,
      knowledge_id: data.knowledge_id,
      knowledge_base_id: data.knowledge_base_id || target.knowledgeBaseId,
      knowledge_title: target.documentTitle,
      chunk_type: data.chunk_type,
      content: data.content,
      source_locators: data.source_locators,
    }];
  } catch {
    return []; // 取不到原文时回退到悬停浮层
  }
}

// 引用 chip 的 hover/click 交互：根容器上的事件代理，消息动态新增无需重新绑定
const { float: citationFloat, cancelClose: cancelCitationClose, scheduleClose: scheduleCitationClose }
  = useChatCitationPopover(scrollRef, { loadReferences: loadCitationReferences });

// 切换会议时关闭引用侧边栏，避免残留上一场会议的引用
const referencesDrawer = useChatReferencesDrawer();

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
  referencesDrawer?.close();
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

/* ---------- 议程条 ---------- */
.agenda-bar {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: var(--app-space-3);
  margin: var(--app-space-2) var(--app-space-5) 0;
  padding: var(--app-space-2) var(--app-space-3);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-page);
}

.agenda-summary {
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  min-width: 88px;
}

.agenda-progress {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
}

.agenda-current {
  font-size: var(--app-text-md);
  font-weight: 600;
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  max-width: 200px;
}

.agenda-items {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-wrap: wrap;
  gap: var(--app-space-1);
  max-height: 48px;
  overflow-y: auto;
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

/* ---------- 开场确认坞 ---------- */
.confirm-dock {
  flex: 0 0 auto;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--app-space-3);
  margin: var(--app-space-2) var(--app-space-5) 0;
  padding: var(--app-space-3) var(--app-space-4);
  border: 1px solid var(--td-brand-color-3);
  border-radius: var(--app-radius-lg);
  background: var(--td-brand-color-light);
}

.confirm-dock__main {
  flex: 1 1 320px;
  min-width: 0;
}

.confirm-dock__title {
  display: flex;
  align-items: center;
  gap: var(--app-space-1);
  font-size: var(--app-text-md);
  font-weight: 500;
  color: var(--td-text-color-primary);
}

.confirm-dock__desc {
  margin: 4px 0 0;
  font-size: 12px;
  line-height: 18px;
  color: var(--td-text-color-secondary);
}

/* 收敛主题：一行内可换行，避免长主题把按钮挤出可视区 */
.confirm-dock__topic {
  margin: 6px 0 0;
  font-size: 12px;
  line-height: 18px;
  color: var(--td-text-color-primary);
  word-break: break-word;
}

.confirm-dock__topic-label {
  margin-right: var(--app-space-1);
  padding: 0 6px;
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-secondary);
}

.confirm-dock__actions {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
}

/* ---------- 共识后的下一步决策坞 ---------- */
.next-step-dock {
  flex: 0 0 auto;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--app-space-3);
  margin: var(--app-space-2) var(--app-space-5) 0;
  padding: var(--app-space-3) var(--app-space-4);
  border: 1px solid var(--td-success-color-3);
  border-radius: var(--app-radius-lg);
  background: color-mix(in srgb, var(--td-success-color) 6%, var(--td-bg-color-container));
}

.next-step-dock__main {
  flex: 1 1 320px;
  min-width: 0;
}

.next-step-dock__title {
  display: flex;
  align-items: center;
  gap: var(--app-space-1);
  font-size: var(--app-text-md);
  font-weight: 500;
  color: var(--td-text-color-primary);
}

.next-step-dock__desc {
  margin: 4px 0 0;
  font-size: 12px;
  line-height: 18px;
  color: var(--td-text-color-secondary);
}

/* 追加议题表单：标题与目标上下排布，避免挤压右侧按钮 */
.next-step-dock__form {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-1);
  margin-top: var(--app-space-2);
}

.next-step-dock__actions {
  flex: 0 0 auto;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--app-space-2);
}

.adjust-chip {
  display: flex;
  margin-bottom: var(--app-space-2);
}

/* ---------- 智能体等待用户回应 ---------- */
.await-user-dock {
  flex: 0 0 auto;
  margin: var(--app-space-2) var(--app-space-5) 0;
  padding: var(--app-space-2) var(--app-space-4);
  border: 1px solid var(--td-brand-color-3);
  border-radius: var(--app-radius-lg);
  background: color-mix(in srgb, var(--td-brand-color) 6%, var(--td-bg-color-container));
}

.await-user-dock__title {
  display: flex;
  align-items: center;
  gap: var(--app-space-1);
  font-size: var(--app-text-md);
  font-weight: 500;
  color: var(--td-text-color-primary);
}

.await-user-dock__desc {
  margin: 2px 0 0;
  font-size: 12px;
  line-height: 18px;
  color: var(--td-text-color-secondary);
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
