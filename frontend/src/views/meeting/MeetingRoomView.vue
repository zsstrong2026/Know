<template>
  <div class="meeting-room">
    <header class="room-header">
      <span class="title">{{ store.currentRoom?.title }}</span>
      <span class="topic">{{ store.currentRoom?.description }}</span>
      <t-button size="small" theme="primary" :loading="store.solutionGenerating"
        :disabled="store.discussing || isArchived" @click="generateSolution">
        {{ $t('meeting.generateSolution') }}
      </t-button>
      <t-button size="small" theme="default" :disabled="!store.discussing" @click="interrupt">
        {{ $t('meeting.interrupt') }}
      </t-button>
      <t-button size="small" theme="default" @click="showArtifacts = true">
        {{ $t('meeting.artifacts') }}
      </t-button>
    </header>

    <!-- 归档只读态提示条 -->
    <t-alert v-if="isArchived" :message="$t('meeting.archivedBanner')" theme="info" class="archived-banner" />

    <main ref="scrollRef" class="message-list">
      <div v-if="store.guardNotice" class="guard-notice">
        <span>{{ $t('meeting.guardNotice') }}</span>
        <t-button size="small" variant="text" @click="store.clearGuardNotice()">✕</t-button>
      </div>
      <div v-if="store.hasMore" class="load-earlier" @click="store.loadEarlier()">
        {{ $t('meeting.loadEarlier') }}
      </div>
      <div v-for="m in store.messages" :key="m.id" class="msg" :class="m.sender_kind">
        <div class="sender">
          {{ senderLabel(m) }}
          <!-- 方案消息：版本标记 -->
          <t-tag v-if="m.is_solution" size="small" theme="primary" variant="dark" class="solution-tag">
            {{ $t('meeting.solution') }} v{{ m.solution_version }}
          </t-tag>
        </div>
        <div class="bubble">{{ m.content }}</div>
        <!-- 评审操作条：仅智能体讨论消息可评审，方案消息不参与；归档后禁用 -->
        <div v-if="m.sender_kind === 'agent' && !m.is_solution" class="review-bar">
          <t-button size="small" theme="success" :variant="m.review_state === 'adopted' ? 'base' : 'outline'"
            :disabled="isArchived" @click="review(m, 'adopted')">{{ $t('meeting.adopt') }}</t-button>
          <t-button size="small" theme="danger" :variant="m.review_state === 'rejected' ? 'base' : 'outline'"
            :disabled="isArchived" @click="review(m, 'rejected')">{{ $t('meeting.reject') }}</t-button>
          <t-button size="small" theme="warning" :variant="m.review_state === 'pending' ? 'base' : 'outline'"
            :disabled="isArchived" @click="review(m, 'pending')">{{ $t('meeting.pending') }}</t-button>
        </div>
      </div>
      <div v-if="store.discussing" class="discussing">{{ $t('meeting.discussing') }}</div>
    </main>

    <ArtifactDrawer v-model:visible="showArtifacts" :room-id="roomId"
      :current-solution-message-id="store.currentRoom?.current_solution_message_id ?? ''" />

    <footer class="input-bar">
      <!-- 已选 @ 的智能体 chip，可点关闭移除 -->
      <div v-if="mentions.length" class="mention-chips">
        <t-tag v-for="m in mentions" :key="m.agent_id" size="small" closable theme="primary" variant="light"
          @close="removeMention(m.agent_id)">
          @{{ m.display_name }}
        </t-tag>
      </div>
      <!-- t-textarea 的 keyup 会以组件参数回调（非原生事件），故在其外层原生 div 上监听并借冒泡触发 -->
      <div class="editor" @keyup.ctrl.enter="send">
        <t-textarea v-model="draft" :autosize="{ minRows: 2, maxRows: 6 }"
          :disabled="store.discussing || isArchived" :placeholder="$t('meeting.inputPlaceholder')" />
      </div>
      <div class="input-actions">
        <MentionPicker @pick="addMention" />
        <t-button theme="primary" :disabled="store.discussing || isArchived || !draft.trim()" @click="send">
          {{ $t('meeting.send') }}
        </t-button>
      </div>
    </footer>
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
import MentionPicker from './components/MentionPicker.vue';
import ArtifactDrawer from './ArtifactDrawer.vue';

const route = useRoute();
const { t } = useI18n();
const store = useMeetingStore();
const draft = ref('');
// 本次发言 @ 提及的智能体（仅视图本地状态，发送时取其 agent_id 传给后端）
const mentions = ref<MeetingParticipant[]>([]);
const showArtifacts = ref(false); // 历史产物抽屉可见性（视图本地状态，不入 store）
const scrollRef = ref<HTMLElement>();
let closeStream: (() => void) | null = null;

const roomId = route.params.roomId as string;

// 归档只读态：输入/发送/评审/生成方案全部禁用，SSE 仍保持接收（归档系统消息可推送到场）
const isArchived = computed(() => store.currentRoom?.status === 'archived');

onMounted(async () => {
  // 进入房间失败时给出提示，且仅在成功后才建立 SSE 连接，避免空白页
  try {
    await store.enterRoom(roomId);
  } catch (e) {
    MessagePlugin.error(t('meeting.loadFailed'));
    return;
  }
  await nextTick();
  scrollToBottom();
  closeStream = openMeetingStream(roomId, {
    onEvent: (event, data) => store.applyStreamEvent(event, data),
    onError: () => { /* M1：视图显示离线提示即可，重连留给 M2 */ },
  });
});

onBeforeUnmount(() => {
  closeStream?.();
  store.leaveRoom();
});

// 监听最后一条消息 id：prepend 加载更早消息时末条 id 不变，仅追加时滚动到底部
watch(() => store.messages[store.messages.length - 1]?.id, () => nextTick(scrollToBottom));

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

// 点击候选智能体加入 chip（按 agent_id 去重）
function addMention(p: MeetingParticipant) {
  if (!mentions.value.some(m => m.agent_id === p.agent_id)) {
    mentions.value.push(p);
  }
}

function removeMention(agentId: string) {
  mentions.value = mentions.value.filter(m => m.agent_id !== agentId);
}

async function send() {
  const content = draft.value.trim();
  if (!content) return;
  const ids = mentions.value.map(m => m.agent_id);
  draft.value = '';
  mentions.value = [];                            // 发送后清空已选 @ 的智能体
  try {
    await store.speak(content, ids);
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
    await interruptMeeting(roomId);
  } catch (e) {
    toastError(e);
  }
}
</script>

<style scoped>
/* 评审操作条：视觉轻量，紧贴气泡下方 */
.review-bar {
  display: flex;
  gap: 6px;
  margin-top: 4px;
}

.solution-tag {
  margin-left: 6px;
}

.mention-chips {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-bottom: 6px;
}

.input-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 8px;
}

.guard-notice {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 8px 12px;
  margin-bottom: 8px;
  border-radius: 6px;
  background: #fff8e1; /* 淡黄底：主持人介入提示 */
  color: #8d6e00;
  font-size: 13px;
}

.archived-banner {
  margin-bottom: 8px;
}
</style>
