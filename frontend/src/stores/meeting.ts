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

  async function speak(content: string, mentionAgentIds: string[] = []) {
    if (!currentRoom.value) return;
    await sendMessage(currentRoom.value.id, content, mentionAgentIds);
    // 用户消息本体经 SSE message.created 回流，这里不本地乐观插入，避免重复
  }

  // SSE 事件入口：由视图层订阅后调用
  function applyStreamEvent(event: string, data: any) {
    if (event === 'message.created' && data) {
      messages.value.push(data as MeetingMessage);
    } else if (event === 'discussion.turn_started') {
      discussing.value = true;
    } else if (event === 'discussion.turn_ended') {
      discussing.value = false;
    }
  }

  function leaveRoom() {
    currentRoom.value = null;
    messages.value = [];
    hasMore.value = false;
    discussing.value = false;
  }

  return { rooms, currentRoom, messages, hasMore, discussing,
    fetchRooms, enterRoom, loadEarlier, speak, applyStreamEvent, leaveRoom };
});
