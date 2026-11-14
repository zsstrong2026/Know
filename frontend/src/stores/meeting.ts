import { computed, ref } from 'vue';
import { defineStore } from 'pinia';
import {
  listMeetings, getMeeting, listMessages, sendMessage,
  generateSolution, rollbackSolution, confirmMeetingFlow,
  listArtifacts, requestArtifact as apiRequestArtifact, regenerateArtifact as apiRegenerateArtifact,
  listAgenda, advanceAgenda as apiAdvanceAgenda,
  appendAgenda as apiAppendAgenda, reopenAgenda as apiReopenAgenda,
  listMeetingAttachments, uploadMeetingAttachment, deleteMeetingAttachment,
  archiveMeeting, isOpeningAnalyzing,
  type MeetingRoom, type MeetingMessage, type MeetingArtifact, type MeetingAgendaItem,
  type MeetingAttachment, type ArtifactKindType,
} from '@/api/meeting';

export const useMeetingStore = defineStore('meetingStore', () => {
  const rooms = ref<MeetingRoom[]>([]);
  const currentRoom = ref<MeetingRoom | null>(null);
  const messages = ref<MeetingMessage[]>([]);
  const hasMore = ref(false);      // 顶部是否还有更早消息
  const discussing = ref(false);   // 是否有讨论轮在进行（可插话，不再禁用输入）
  // 进房/切换会议时加载房间与消息的占位状态，避免对话框出现短暂空白
  const loadingRoom = ref(false);
  // 当前正在组织发言的智能体（discussion.speaker_thinking）；消息落库后清除
  const thinkingSpeaker = ref<{ agent_id: string; agent_name: string } | null>(null);
  const awaitingUser = ref<MeetingMessage | null>(null); // 智能体 @用户 提问后，等待用户回应
  const guardNotice = ref<unknown>(null); // 护栏触发提示（主持人介入）
  const consensusNotice = ref<unknown>(null); // 裁判判定收敛提示（consensus.created）
  const solutionGenerating = ref(false);  // 方案生成中（按钮 loading 用）
  const artifacts = ref<MeetingArtifact[]>([]); // 当前房间产物（solution + generated 统一）
  const agenda = ref<MeetingAgendaItem[]>([]);  // 当前房间议程（结构性治理；未启用时为空）
  const attachments = ref<MeetingAttachment[]>([]); // 当前房间文件（解析状态轮询）
  const attachmentUploading = ref(false);        // 文件上传中
  let attachmentPollTimer: ReturnType<typeof setTimeout> | null = null;

  // 当前活跃议题（仅 status=active；用于提示条与"结束议题"按钮）
  const activeAgendaItem = computed(() => agenda.value.find((it) => it.status === 'active') ?? null);
  // 已覆盖议题数（用于进度提示）
  const agendaDoneCount = computed(() => agenda.value.filter((it) => it.status === 'done').length);

  // 开场生成中：待确认态下开场消息尚未回流（含用户刚发出调整意见/澄清回答后的重出）。
  // 视图据此展示「正在分析议题」占位，并把「开始会议」确认坞推迟到开场回流之后。
  const openingAnalyzing = computed(() => isOpeningAnalyzing(currentRoom.value?.status, messages.value));

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
    loadingRoom.value = true;
    try {
      currentRoom.value = await getMeeting(roomId);
      const [page, items, files] = await Promise.all([
        listMessages(roomId),
        listAgenda(roomId).catch(() => [] as MeetingAgendaItem[]), // 议程为可选能力，失败不阻断进房
        listMeetingAttachments(roomId).catch(() => [] as MeetingAttachment[]), // 会议文件同理
      ]);
      // SSE 可能先于本次分页返回：保留拉取期间已到达的新消息，避免被整页覆盖
      const seen = new Set(page.map((m) => m.id));
      const arrived = messages.value.filter((m) => !seen.has(m.id));
      messages.value = arrived.length ? [...page, ...arrived] : page;
      agenda.value = items;
      attachments.value = files;
      hasMore.value = page.length >= 100;
      ensureAttachmentPolling();
    } finally {
      loadingRoom.value = false;
    }
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
    awaitingUser.value = null; // 用户已回应，等待提示随之收起
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

  // 方案消息经 SSE message.created 回流，这里只控制 loading；
  // LLM 失败时 HTTP 报错同样走 finally 复位（solution.generating 无配对终态事件）
  async function generate() {
    if (!currentRoom.value) return;
    solutionGenerating.value = true;
    try {
      await generateSolution(currentRoom.value.id);
      consensusNotice.value = null; // 已进入方案生成，下一步决策坞随之收起
    } finally {
      solutionGenerating.value = false;
    }
  }

  // 结束会议：归档后转只读，下一步决策坞与输入区一并禁用
  async function archive() {
    if (!currentRoom.value) return;
    await archiveMeeting(currentRoom.value.id);
    currentRoom.value = { ...currentRoom.value, status: 'archived' };
    consensusNotice.value = null;
  }

  // 拉取当前房间全部产物（抽屉打开/回滚后刷新）
  async function fetchArtifacts(roomId: string) {
    artifacts.value = await listArtifacts(roomId);
  }

  // 人工推进议程（跳过/确认当前议题），以服务端返回的最新列表为准
  async function advanceAgenda(itemId = '') {
    if (!currentRoom.value) return;
    agenda.value = await apiAdvanceAgenda(currentRoom.value.id, itemId);
    consensusNotice.value = null; // 议题状态已变，旧的收敛提示不再适用
  }

  // 追加新议题并开启讨论：后端落开题消息（SSE 回流）并异步跑一轮讨论
  async function appendAgenda(title: string, goal = '') {
    if (!currentRoom.value) return;
    agenda.value = await apiAppendAgenda(currentRoom.value.id, title, goal);
    consensusNotice.value = null;
  }

  // 恢复指定议题继续讨论：收敛后用户选择「还要补充」时使用
  async function reopenAgenda(itemId: string) {
    if (!currentRoom.value) return;
    agenda.value = await apiReopenAgenda(currentRoom.value.id, itemId);
    consensusNotice.value = null;
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

  // 拉取当前房间文件列表；解析中的文件会继续轮询直到就绪
  async function fetchAttachments() {
    if (!currentRoom.value) return;
    try {
      attachments.value = await listMeetingAttachments(currentRoom.value.id);
      ensureAttachmentPolling();
    } catch {
      // 附件为可选能力，失败不阻断会议
    }
  }

  // 解析是后台异步任务：只在存在 uploaded/processing 时每 2 秒刷新一次，全部就绪后停止
  function ensureAttachmentPolling() {
    const pending = attachments.value.some(
      (a) => a.status === 'uploaded' || a.status === 'processing',
    );
    if (!pending) {
      stopAttachmentPolling();
      return;
    }
    if (attachmentPollTimer) return;
    attachmentPollTimer = setTimeout(() => {
      attachmentPollTimer = null;
      void fetchAttachments();
    }, 2000);
  }

  function stopAttachmentPolling() {
    if (attachmentPollTimer) {
      clearTimeout(attachmentPollTimer);
      attachmentPollTimer = null;
    }
  }

  // 上传会议文件：解析完成后自动进入后续智能体发言上下文
  async function uploadAttachment(file: File) {
    if (!currentRoom.value) return null;
    attachmentUploading.value = true;
    try {
      const doc = await uploadMeetingAttachment(currentRoom.value.id, file);
      if (doc && !attachments.value.some((a) => a.id === doc.id)) attachments.value.push(doc);
      ensureAttachmentPolling();
      return doc;
    } finally {
      attachmentUploading.value = false;
    }
  }

  async function removeAttachment(attachmentId: string) {
    if (!currentRoom.value) return;
    await deleteMeetingAttachment(currentRoom.value.id, attachmentId);
    attachments.value = attachments.value.filter((a) => a.id !== attachmentId);
  }

  // SSE 事件入口：由视图层订阅后调用
  function applyStreamEvent(event: string, data: unknown) {
    if (event === 'message.created' && data) {
      appendMessage(data as MeetingMessage);
      thinkingSpeaker.value = null; // 有消息落库，说明上一位已说完
    } else if (event === 'discussion.speaker_thinking') {
      const d = data as { agent_id?: string; agent_name?: string } | null;
      if (d?.agent_id) {
        thinkingSpeaker.value = { agent_id: d.agent_id, agent_name: d.agent_name ?? '' };
        discussing.value = true;
      }
    } else if (event === 'solution.generating') {
      solutionGenerating.value = true;
    } else if (event === 'solution.created') {
      solutionGenerating.value = false;
    } else if (event === 'discussion.turn_started') {
      discussing.value = true;
      thinkingSpeaker.value = null;
      // 开场完成后后端会自动开始讨论：本地同步为 active，避免待确认坞残留
      if (currentRoom.value?.status === 'awaiting_confirmation') {
        currentRoom.value = { ...currentRoom.value, status: 'active' };
      }
      awaitingUser.value = null; // 用户发言后讨论已继续，旧的等待提示失效
      guardNotice.value = null; // 新一轮开始清除旧的护栏提示
      consensusNotice.value = null; // 新一轮开始清除旧的共识提示
    } else if (event === 'discussion.turn_ended') {
      discussing.value = false;
      thinkingSpeaker.value = null;
    } else if (event === 'discussion.awaiting_user') {
      // 智能体 @用户 提问：讨论暂停，输入区提示「待你回应」
      discussing.value = false;
      thinkingSpeaker.value = null;
      awaitingUser.value = (data as { message?: MeetingMessage } | null)?.message ?? null;
    } else if (event === 'guard.triggered') {
      discussing.value = false;
      thinkingSpeaker.value = null;
      guardNotice.value = data;
    } else if (event === 'consensus.created') {
      // 裁判判定收敛：讨论轮终止，展示共识提示（共识消息本体由 message.created 回流）
      discussing.value = false;
      thinkingSpeaker.value = null;
      consensusNotice.value = data;
    } else if (event === 'artifact.generating' || event === 'artifact.created' || event === 'artifact.failed') {
      upsertArtifact(artifactFromEvent(event, data));
    } else if (event === 'agenda.updated') {
      // 议程拆解/推进：后端推完整列表，直接覆盖；讨论推进后前端提示条随之更新
      const items = (data as { items?: MeetingAgendaItem[] } | null)?.items;
      if (Array.isArray(items)) agenda.value = items;
    } else if (event === 'meeting.attachment_updated') {
      // 文件上传/删除/解析状态变化：拉取最新列表（解析完成由轮询兜底）
      void fetchAttachments();
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
    stopAttachmentPolling();
    currentRoom.value = null;
    messages.value = [];
    hasMore.value = false;
    discussing.value = false;
    loadingRoom.value = false;
    thinkingSpeaker.value = null;
    awaitingUser.value = null;
    guardNotice.value = null;
    consensusNotice.value = null;
    artifacts.value = [];
    agenda.value = [];
    attachments.value = [];
    attachmentUploading.value = false;
  }

  return { rooms, currentRoom, messages, hasMore, discussing, loadingRoom, thinkingSpeaker, awaitingUser, guardNotice, consensusNotice, solutionGenerating, artifacts,
    agenda, activeAgendaItem, agendaDoneCount, attachments, attachmentUploading,
    needsClarification, openingAnalyzing,
    fetchRooms, enterRoom, loadEarlier, speak, confirm, generate, archive, rollback, applyStreamEvent, clearGuardNotice, clearConsensusNotice, leaveRoom,
    fetchArtifacts, requestArtifact, regenerateArtifact, advanceAgenda, appendAgenda, reopenAgenda,
    fetchAttachments, uploadAttachment, removeAttachment };
});
