import { ref } from 'vue';
import { defineStore } from 'pinia';
import {
  listMeetings, getMeeting, listMessages, sendMessage,
  updateReviewState, generateSolution, rollbackSolution,
  type MeetingRoom, type MeetingMessage,
} from '@/api/meeting';

export const useMeetingStore = defineStore('meetingStore', () => {
  const rooms = ref<MeetingRoom[]>([]);
  const currentRoom = ref<MeetingRoom | null>(null);
  const messages = ref<MeetingMessage[]>([]);
  const hasMore = ref(false);      // 顶部是否还有更早消息
  const discussing = ref(false);   // 是否有讨论轮在进行（禁用输入用）
  const guardNotice = ref<unknown>(null); // 护栏触发提示（主持人介入）
  const consensusNotice = ref<unknown>(null); // 裁判判定收敛提示（consensus.created）
  const solutionGenerating = ref(false);  // 方案生成中（按钮 loading 用）

  async function fetchRooms(status = '') {
    rooms.value = await listMeetings(status);
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

  // 评审状态流转：HTTP 成功后本地直接更新（SSE review_updated 回流为幂等覆盖）
  async function setReviewState(messageId: string, state: string) {
    if (!currentRoom.value) return;
    const updated = await updateReviewState(currentRoom.value.id, messageId, state);
    const m = messages.value.find(x => x.id === messageId);
    if (m) m.review_state = updated.review_state;
  }

  // 方案消息经 SSE message.created 回流，这里只控制 loading；
  // LLM 失败时 HTTP 报错同样走 finally 复位（solution.generating 无配对终态事件）
  async function generate() {
    if (!currentRoom.value) return;
    solutionGenerating.value = true;
    try {
      await generateSolution(currentRoom.value.id);
    } finally {
      solutionGenerating.value = false;
    }
  }

  // 回滚方案版本：HTTP 成功后重拉房间（current_solution_message_id 变化）与消息列表，
  // 回滚落一条系统消息，SSE 亦会回流，HTTP 刷新是兜底（同时去重）
  async function rollback(version: number) {
    if (!currentRoom.value) return;
    await rollbackSolution(currentRoom.value.id, version);
    await enterRoom(currentRoom.value.id);
  }

  // SSE 事件入口：由视图层订阅后调用
  function applyStreamEvent(event: string, data: unknown) {
    if (event === 'message.created' && data) {
      appendMessage(data as MeetingMessage);
    } else if (event === 'message.review_updated' && data) {
      const m = messages.value.find(x => x.id === (data as MeetingMessage).id);
      if (m) m.review_state = (data as MeetingMessage).review_state;
    } else if (event === 'solution.generating') {
      solutionGenerating.value = true;
    } else if (event === 'solution.created') {
      solutionGenerating.value = false;
    } else if (event === 'discussion.turn_started') {
      discussing.value = true;
      guardNotice.value = null; // 新一轮开始清除旧的护栏提示
      consensusNotice.value = null; // 新一轮开始清除旧的共识提示
    } else if (event === 'discussion.turn_ended') {
      discussing.value = false;
    } else if (event === 'guard.triggered') {
      discussing.value = false;
      guardNotice.value = data;
    } else if (event === 'consensus.created') {
      // 裁判判定收敛：讨论轮终止，展示共识提示（共识消息本体由 message.created 回流）
      discussing.value = false;
      consensusNotice.value = data;
    }
  }

  function clearGuardNotice() {
    guardNotice.value = null;
  }

  function clearConsensusNotice() {
    consensusNotice.value = null;
  }

  function leaveRoom() {
    currentRoom.value = null;
    messages.value = [];
    hasMore.value = false;
    discussing.value = false;
    guardNotice.value = null;
    consensusNotice.value = null;
  }

  return { rooms, currentRoom, messages, hasMore, discussing, guardNotice, consensusNotice, solutionGenerating,
    fetchRooms, enterRoom, loadEarlier, speak, setReviewState, generate, rollback, applyStreamEvent, clearGuardNotice, clearConsensusNotice, leaveRoom };
});
