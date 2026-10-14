<template>
  <div class="meeting-room">
    <header class="room-header">
      <span class="title">{{ store.currentRoom?.title }}</span>
      <span class="topic">{{ store.currentRoom?.description }}</span>
    </header>

    <main ref="scrollRef" class="message-list">
      <div v-if="store.hasMore" class="load-earlier" @click="store.loadEarlier()">
        {{ $t('meeting.loadEarlier') }}
      </div>
      <div v-for="m in store.messages" :key="m.id" class="msg" :class="m.sender_kind">
        <div class="sender">{{ senderLabel(m) }}</div>
        <div class="bubble">{{ m.content }}</div>
      </div>
      <div v-if="store.discussing" class="discussing">{{ $t('meeting.discussing') }}</div>
    </main>

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
        <t-textarea v-model="draft" :autosize="{ minRows: 2, maxRows: 6 }" :disabled="store.discussing"
          :placeholder="$t('meeting.inputPlaceholder')" />
      </div>
      <div class="input-actions">
        <MentionPicker @pick="addMention" />
        <t-button theme="primary" :disabled="store.discussing || !draft.trim()" @click="send">
          {{ $t('meeting.send') }}
        </t-button>
      </div>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute } from 'vue-router';
import { MessagePlugin } from 'tdesign-vue-next';
import { useMeetingStore } from '@/stores/meeting';
import { openMeetingStream } from '@/api/meeting';
import type { MeetingMessage, MeetingParticipant } from '@/api/meeting';
import MentionPicker from './components/MentionPicker.vue';

const route = useRoute();
const { t } = useI18n();
const store = useMeetingStore();
const draft = ref('');
// 本次发言 @ 提及的智能体（仅视图本地状态，发送时取其 agent_id 传给后端）
const mentions = ref<MeetingParticipant[]>([]);
const scrollRef = ref<HTMLElement>();
let closeStream: (() => void) | null = null;

const roomId = route.params.roomId as string;

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
    MessagePlugin.error((e as Error)?.message || t('meeting.loadFailed'));
  }
}
</script>

<style scoped>
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
</style>
