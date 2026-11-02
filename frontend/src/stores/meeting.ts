import { computed, ref } from 'vue';
import { defineStore } from 'pinia';
import {
  listMeetings, getMeeting, listMessages, sendMessage,
  updateReviewState, generateSolution, rollbackSolution, confirmMeetingFlow,
  listArtifacts, requestArtifact as apiRequestArtifact, regenerateArtifact as apiRegenerateArtifact,
  listAgenda, advanceAgenda as apiAdvanceAgenda,
  type MeetingRoom, type MeetingMessage, type MeetingArtifact, type MeetingAgendaItem, type ArtifactKindType,
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
  const artifacts = ref<MeetingArtifact[]>([]); // 当前房间产物（solution + generated 统一）
  const agenda = ref<MeetingAgendaItem[]>([]);  // 当前房间议程（结构性治理；未启用时为空）

  // 当前活跃议题（仅 status=active；用于提示条与"结束议题"按钮）
  const activeAgendaItem = computed(() => agenda.value.find((it) => it.status === 'active') ?? null);
  // 已覆盖议题数（用于进度提示）
  const agendaDoneCount = computed(() => agenda.value.filter((it) => it.status === 'done').length);

  // 最近一条开场消息是否处于「等待用户澄清主题」态（与后端 openingNeedsClarification 对齐）；
  // 澄清态下用户直接输入语义为「补充说明」，而非隐式确认开会。
  const needsClarification = computed(() => {
    for (let i = messages.value.length - 1; i >= 0; i -= 1) {
      const p = messages.value[i].payload as { stage?: string; needs_clarification?: boolean } | null | undefined;
      if (p?.stage === 'opening') return p.needs_clarification === true;
    }
    return false;
  });

  async function fetchRooms(status = '') {
    rooms.value = await listMeetings(status);
  }

  async function enterRoom(roomId: string) {
    currentRoom.value = await getMeeting(roomId);
    const [page, items] = await Promise.all([
      listMessages(roomId),
      listAgenda(roomId).catch(() => [] as MeetingAgendaItem[]), // 议程为可选能力，失败不阻断进房
    ]);
    messages.value = page;
    agenda.value = items;
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

  async function speak(content: string, mentionAgentIds: string[] = [], adjustFlow = false) {
    if (!currentRoom.value) return;
    // 后端不会通过 SSE 回推用户自身的消息，这里本地插入返回值，避免自己发言不显示
    const msg = await sendMessage(currentRoom.value.id, content, mentionAgentIds, adjustFlow);
    appendMessage(msg);
    // 直接打字（非调整流程、非澄清回答）等同隐式确认：后端已置 active，同步本地状态以免待确认横幅残留到刷新；
    // 澄清态下后端保持 awaiting_confirmation 并重出开场，前端不得翻转状态
    const clarify = !adjustFlow && needsClarification.value;
    if (!adjustFlow && !clarify && currentRoom.value.status === 'awaiting_confirmation') {
      currentRoom.value = { ...currentRoom.value, status: 'active' };
    }
  }

  // 「开始会议」：确认流程后本地置 active（按钮组/提示条随之消失；讨论经 SSE 回流）
  async function confirm() {
    if (!currentRoom.value) return;
    await confirmMeetingFlow(currentRoom.value.id);
    if (currentRoom.value) {
      currentRoom.value = { ...currentRoom.value, status: 'active' };
    }
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

  // 拉取当前房间全部产物（抽屉打开/回滚后刷新）
  async function fetchArtifacts(roomId: string) {
    artifacts.value = await listArtifacts(roomId);
  }

  // 人工推进议程（跳过/确认当前议题），以服务端返回的最新列表为准
  async function advanceAgenda(itemId = '') {
    if (!currentRoom.value) return;
    agenda.value = await apiAdvanceAgenda(currentRoom.value.id, itemId);
  }

  // 按 id 就地 upsert（HTTP 返回值与 SSE 事件共用；SSE 可能先于 HTTP 到达）
  // 入参放宽为增量补丁：事件只带部分字段时，仅覆盖它实际携带的键，其余键沿用卡片旧值
  function upsertArtifact(a: (Partial<MeetingArtifact> & { id: string }) | null | undefined) {
    if (!a?.id) return;
    const i = artifacts.value.findIndex((x) => x.id === a.id);
    // 增量补丁与全量返回走同一合并路径；断言收窄仅用于表达「缺失键依赖后续补齐」
    if (i >= 0) artifacts.value[i] = { ...artifacts.value[i], ...a } as MeetingArtifact;
    else artifacts.value.push(a as MeetingArtifact);
  }

  // SSE 产物事件 → 卡片增量补丁（generating/failed 事件只带部分字段）
  function artifactFromEvent(event: string, data: unknown): (Partial<MeetingArtifact> & { id: string }) | null {
    const d = data as Record<string, unknown> | null;
    if (!d) return null;
    if (event === 'artifact.created') return d as unknown as MeetingArtifact; // 后端推完整产物对象
    const id = String(d.artifact_id ?? '');
    if (!id) return null;
    return {
      id,
      meeting_room_id: currentRoom.value?.id ?? '',
      kind: 'generated',
      status: event === 'artifact.generating' ? 'generating' : 'failed',
      error: String(d.error ?? ''),
      // 仅当事件确实携带该键时写入，避免用空串把卡片已有的标题/形态清零
      ...(typeof d.kind_type === 'string' ? { kind_type: d.kind_type as ArtifactKindType } : {}),
      ...(typeof d.title === 'string' ? { title: d.title } : {}),
    };
  }

  // 发起产物生成：HTTP 202 返回 generating 行，本地立即插入 loading 占位卡片
  async function requestArtifact(kindType: ArtifactKindType, title: string, requirement: string) {
    if (!currentRoom.value) return;
    const a = await apiRequestArtifact(currentRoom.value.id, kindType, title, requirement);
    upsertArtifact(a);
  }

  // 重新生成：原行重置为 generating
  async function regenerateArtifact(artifactId: string) {
    if (!currentRoom.value) return;
    const a = await apiRegenerateArtifact(currentRoom.value.id, artifactId);
    upsertArtifact(a);
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
    } else if (event === 'artifact.generating' || event === 'artifact.created' || event === 'artifact.failed') {
      upsertArtifact(artifactFromEvent(event, data));
    } else if (event === 'agenda.updated') {
      // 议程拆解/推进：后端推完整列表，直接覆盖；讨论推进后前端提示条随之更新
      const items = (data as { items?: MeetingAgendaItem[] } | null)?.items;
      if (Array.isArray(items)) agenda.value = items;
    } else if (event === 'discussion.flow_confirmation') {
      // 开场确认事件：无独立状态，开场消息本体经 message.created 回流，按钮组由 payload.stage 渲染
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
    artifacts.value = [];
    agenda.value = [];
  }

  return { rooms, currentRoom, messages, hasMore, discussing, guardNotice, consensusNotice, solutionGenerating, artifacts,
    agenda, activeAgendaItem, agendaDoneCount,
    needsClarification,
    fetchRooms, enterRoom, loadEarlier, speak, confirm, setReviewState, generate, rollback, applyStreamEvent, clearGuardNotice, clearConsensusNotice, leaveRoom,
    fetchArtifacts, requestArtifact, regenerateArtifact, advanceAgenda };
});
