import { ref } from 'vue';
import { defineStore } from 'pinia';
import {
  listMeetings, getMeeting, listMessages, sendMessage,
  type MeetingRoom, type MeetingMessage,
} from '@/api/meeting';

export const useMeetingStore = defineStore('meetingStore', () => {
  const rooms = ref<MeetingRoom[]>([]);
  const currentRoom = ref<MeetingRoom | null>(null);
  const messages = ref<MeetingMessage[]>([]);
  const hasMore = ref(false);      // 顶部是否还有更早消息
  const discussing = ref(false);   // 是否有讨论轮在进行（禁用输入用）
  const guardNotice = ref<unknown>(null); // 护栏触发提示（主持人介入）

  async function fetchRooms() {
    rooms.value = await listMeetings();
  }

  async function enterRoom(roomId: string) {
    currentRoom.value = await getMeeting(roomId);
    const page = await listMessages(roomId);
    messages.value = page;
    hasMore.value = page.length >= 100;
  }

  // 顶部“加载更早”：以当前最旧一条为游标向前翻
  async function loadEarlier() {
    if (!currentRoom.value || !messages.value.length) return;
    const oldest = messages.value[0];
    const page = await listMessages(currentRoom.value.id, oldest.id);
    messages.value = [...page, ...messages.value];
    hasMore.value = page.length >= 100;
  }

  // 去重插入：本地乐观插入与 SSE 回流可能命中同一条，按 id 去重
  function appendMessage(msg: MeetingMessage | null | undefined) {
    if (!msg || messages.value.some((m) => m.id === msg.id)) return;
    messages.value.push(msg);
  }

  async function speak(content: string, mentionAgentIds: string[] = []) {
    if (!currentRoom.value) return;
    // 后端不会通过 SSE 回推用户自身的消息，这里本地插入返回值，避免自己发言不显示
    const msg = await sendMessage(currentRoom.value.id, content, mentionAgentIds);
    appendMessage(msg);
  }

  // SSE 事件入口：由视图层订阅后调用
  function applyStreamEvent(event: string, data: unknown) {
    if (event === 'message.created' && data) {
      appendMessage(data as MeetingMessage);
    } else if (event === 'discussion.turn_started') {
      discussing.value = true;
      guardNotice.value = null; // 新一轮开始清除旧的护栏提示
    } else if (event === 'discussion.turn_ended') {
      discussing.value = false;
    } else if (event === 'guard.triggered') {
      discussing.value = false;
      guardNotice.value = data;
    }
  }

  function clearGuardNotice() {
    guardNotice.value = null;
  }

  function leaveRoom() {
    currentRoom.value = null;
    messages.value = [];
    hasMore.value = false;
    discussing.value = false;
    guardNotice.value = null;
  }

  return { rooms, currentRoom, messages, hasMore, discussing, guardNotice,
    fetchRooms, enterRoom, loadEarlier, speak, applyStreamEvent, clearGuardNotice, leaveRoom };
});
