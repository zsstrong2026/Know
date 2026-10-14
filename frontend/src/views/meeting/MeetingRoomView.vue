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
      <!-- t-textarea 的 keyup 会以组件参数回调（非原生事件），故在其外层原生 div 上监听并借冒泡触发 -->
      <div class="editor" @keyup.ctrl.enter="send">
        <t-textarea v-model="draft" :autosize="{ minRows: 2, maxRows: 6 }" :disabled="store.discussing"
          :placeholder="$t('meeting.inputPlaceholder')" />
      </div>
      <t-button theme="primary" :disabled="store.discussing || !draft.trim()" @click="send">
        {{ $t('meeting.send') }}
      </t-button>
    </footer>
  </div>
</template>

<script setup lang="ts">
import { nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import { useRoute } from 'vue-router';
import { useMeetingStore } from '@/stores/meeting';
import { openMeetingStream } from '@/api/meeting';
import type { MeetingMessage } from '@/api/meeting';

const route = useRoute();
const store = useMeetingStore();
const draft = ref('');
const scrollRef = ref<HTMLElement>();
let closeStream: (() => void) | null = null;

const roomId = route.params.roomId as string;

onMounted(async () => {
  await store.enterRoom(roomId);
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

watch(() => store.messages.length, () => nextTick(scrollToBottom));

function scrollToBottom() {
  const el = scrollRef.value;
  if (el) el.scrollTop = el.scrollHeight;
}

function senderLabel(m: MeetingMessage): string {
  if (m.sender_kind === 'host') return '主持人';
  if (m.sender_kind === 'system') return '系统';
  if (m.sender_kind === 'user') return '我';
  const p = store.currentRoom?.participants?.find(
    x => x.kind === 'agent' && x.agent_id === m.sender_id);
  return p?.display_name ?? m.sender_id.slice(0, 8);
}

async function send() {
  const content = draft.value.trim();
  if (!content) return;
  draft.value = '';
  await store.speak(content);
}
</script>
