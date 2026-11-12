// ===== 类型定义（与后端 internal/types/meeting.go 对齐）=====

// 广播模式：顺序发言 / 并行发言
export type BroadcastMode = 'sequential' | 'parallel';
// 发言权路由模式：主持人指派 / 提及驱动 / 自由抢麦
export type RoutingMode = 'host-assign' | 'mention' | 'free';
// 参会者类型：主持人（唯一）/ 智能体
export type ParticipantKind = 'host' | 'agent';
// 消息发送者类型
export type SenderKind = 'user' | 'agent' | 'host' | 'system';
// 会议房间状态：待确认（开场流程未确认）/ 进行中 / 已归档（只读终态）
export type MeetingStatus = 'active' | 'awaiting_confirmation' | 'archived';
// 发言准入模式：关闭 / 只记录 / 未通过要求重写一次
export type AdmissionMode = 'off' | 'observe' | 'enforce';
// 议程项状态
export type AgendaItemStatus = 'pending' | 'active' | 'done' | 'skipped';
// 产物类别：solution = 评审汇总方案；generated = 用户指定形态生成
export type ArtifactKind = 'solution' | 'generated';
// 产物生成状态机：generating → ready | failed
export type ArtifactStatus = 'generating' | 'ready' | 'failed';
// 8 种产物形态（与后端 types/meeting.go 常量一致）
export type ArtifactKindType =
  | 'report_markdown' | 'minutes' | 'todo_list'
  | 'docx' | 'xlsx' | 'pptx' | 'html_report' | 'chart_image';

export interface MeetingRoom {
  id: string;
  title: string;
  description: string;
  // 收敛后的会议主题：主持人开场澄清后落库；为空表示未澄清（沿用原始 title/description）
  topic_brief: string;
  // 会议级联网总闸：开启后全部参会智能体在检索知识库之外可联网搜索
  web_search_enabled: boolean;
  // awaiting_confirmation：创建后待确认开场流程（M1 起新建会议默认进入该态）
  status: MeetingStatus;
  broadcast_mode: BroadcastMode;
  routing_mode: RoutingMode;
  // 护栏阈值：单轮内最多允许的发言交换次数
  guard_max_exchange: number;
  // 主持人介入间隔（子轮数）：0 表示不介入
  host_every_n_rounds: number;
  // 裁判 + 共识检测：开启后每轮结束由独立裁判判定是否收敛
  consensus_detection_enabled: boolean;
  // 结构性治理总闸：开启后主持人开场拆解子议题、讨论按议题聚焦
  agenda_enabled: boolean;
  // 发言准入模式：off / observe / enforce
  admission_mode: AdmissionMode;
  knowledge_base_ids: string[];
  created_by: string;
  created_at: string;
  updated_at: string;
  // 当前方案消息指针（空串表示尚未生成过方案）
  current_solution_message_id: string;
  // 详情接口会一并返回参会者
  participants?: MeetingParticipant[];
}

export interface MeetingParticipant {
  id: string;
  meeting_room_id: string;
  agent_id: string; // host 参与者为空字符串
  kind: ParticipantKind;
  display_name: string; // 冗余展示名（主持人固定为“主持人”）
}

export interface MeetingMessage {
  id: string;
  meeting_room_id: string;
  sender_kind: SenderKind;
  sender_id: string; // user UUID / agent UUID / ''
  content: string;
  mentioned_agent_ids: string[];
  is_solution: boolean;
  solution_version: number;
  // 裁判判定的共识消息标记
  is_consensus: boolean;
  // 发言归属的议程项（空 = 未归属）
  agenda_item_id: string;
  // 发言准入判定结果（空 = 未判定）
  admission_state: '' | 'passed' | 'observed' | 'rewritten';
  admission_note?: string;
  // jsonb 载荷：开场消息为 { stage: 'opening' }（其余消息为 null/缺省）
  payload?: Record<string, unknown> | null;
  created_at: string;
}

// 议程项：主持人开场拆解出的子议题
export interface MeetingAgendaItem {
  id: string;
  meeting_room_id: string;
  seq: number;
  title: string;
  goal: string;
  owner_agent_ids: string[];
  knowledge_base_ids: string[];
  status: AgendaItemStatus;
  summary: string;
  created_at: string;
  updated_at: string;
}

// 会议产物（solution 与 generated 统一模型；列表接口不保证 content，详情接口带正文）
export interface MeetingArtifact {
  id: string;
  meeting_room_id: string;
  // solution 产物指向会议方案消息；generated 为空串
  solution_message_id: string;
  version: number;
  adopted_count: number;
  rejected_count: number;
  created_by: string;
  created_at: string;
  kind: ArtifactKind;
  kind_type: ArtifactKindType | '';
  title: string;
  requirement: string;
  file_name: string;
  file_type: string;
  file_size: number;
  url: string;
  content_hash: string;
  status: ArtifactStatus;
  error: string;
  // generated 产物的后台会话消息 ID（下载/详情寻址）
  source_message_id: string;
  content: string;
}

// 更新会议请求体：PATCH 白名单字段，均可选（局部更新）
export interface UpdateMeetingRequest {
  title?: string;
  description?: string;
  broadcast_mode?: BroadcastMode;
  routing_mode?: RoutingMode;
  guard_max_exchange?: number;
  host_every_n_rounds?: number;
  consensus_detection_enabled?: boolean;
  agenda_enabled?: boolean;
  admission_mode?: AdmissionMode;
}

export interface CreateMeetingInput {
  title: string;
  description: string;
  agentIds: string[];
  knowledgeBaseIds: string[];
  broadcastMode: BroadcastMode;
  routingMode: RoutingMode;
  // 主持人介入间隔：0（关闭，默认）或 1..10（后端超范围一律按关闭处理）
  hostEveryNRounds: number;
  // 裁判 + 共识检测开关：默认关闭
  consensusDetectionEnabled: boolean;
  // 结构性治理：议程拆解 + 发言准入（默认关闭，保持旧行为）
  agendaEnabled: boolean;
  admissionMode: AdmissionMode;
  // 会议级联网开关：默认关闭；开启后强制全部参会智能体联网（需系统已配置搜索服务）
  webSearchEnabled: boolean;
}

// ===== 纯函数：请求体组装（便于单测，不依赖任何浏览器 API）=====

// 组装创建会议请求体：标题去首尾空格且非空，智能体至少一个
export function buildCreatePayload(input: CreateMeetingInput) {
  const title = (input.title ?? '').trim();
  if (!title) throw new Error('title is required');
  const agentIds = input.agentIds ?? [];
  if (agentIds.length === 0) throw new Error('at least one agent is required');
  return {
    title,
    description: (input.description ?? '').trim(),
    agent_ids: agentIds,
    knowledge_base_ids: input.knowledgeBaseIds ?? [],
    broadcast_mode: input.broadcastMode,
    routing_mode: input.routingMode,
    host_every_n_rounds: input.hostEveryNRounds ?? 0,
    consensus_detection_enabled: input.consensusDetectionEnabled ?? false,
    agenda_enabled: input.agendaEnabled ?? false,
    admission_mode: input.admissionMode ?? 'off',
    web_search_enabled: input.webSearchEnabled ?? false,
  };
}

// 专家异构引导：判断已选智能体是否"模型同质"
// 同质 = 所选至少 2 个，且其模型标识去重后仅剩 1 种（含"全部未指定→同用默认模型"）
export function isModelHomogeneous(modelKeys: Array<string | undefined | null>): boolean {
  if (modelKeys.length < 2) return false;
  const distinct = new Set(modelKeys.map((k) => (k ?? '').trim() || '__default__'));
  return distinct.size === 1;
}

// 组装发言请求体：@ 提及可选；adjust_flow 仅待确认状态下的「调整流程」模式为 true
export function buildMessagePayload(content: string, mentionAgentIds: string[] = [], adjustFlow = false) {
  return { content, mentioned_agent_ids: mentionAgentIds, adjust_flow: adjustFlow };
}

// 判断主持人是否仍在分析议题（开场消息尚未回流）。
// 仅待确认态成立：无任何消息（刚进房）、或末条是用户消息（刚提交调整意见/澄清回答）时视为分析中；
// 末条是主持人开场或系统兜底消息则不再是分析中（避免 SSE 断流时永远等待）。
export function isOpeningAnalyzing(
  status: MeetingStatus | undefined,
  messages: Array<Pick<MeetingMessage, 'sender_kind' | 'payload'>>,
): boolean {
  if (status !== 'awaiting_confirmation') return false;
  const last = messages[messages.length - 1];
  if (!last) return true;
  if (last.sender_kind === 'system') return false;
  const stage = (last.payload as { stage?: string } | null | undefined)?.stage;
  if (stage === 'opening') return false;
  return last.sender_kind === 'user';
}

// 会议室 SSE 订阅地址（相对路径，实际请求时拼接 API base）
export function streamUrl(roomId: string): string {
  return `/api/v1/meetings/${roomId}/stream`;
}

// ===== 网络层按需加载 =====
// request.ts 的依赖链（@/i18n 顶层读取 localStorage、api-base 读取 import.meta.env）
// 依赖浏览器环境，静态 import 会让本模块无法在 Node/tsx 下被单测加载，
// 因此这里统一改为函数内动态加载。

async function loadRequest() {
  return import('../../utils/request');
}

// ===== HTTP 接口 =====
// 注意：request.ts 的 get/post 解析出的是后端整个响应体 `{ success, data }`，
// 这里统一解包为业务对象，调用方（Store）直接拿到数组/实体。

export async function listMeetings(status = '') {
  const { get } = await loadRequest();
  const qs = status ? `?status=${encodeURIComponent(status)}` : '';
  return get<{ data: MeetingRoom[] }>(`/api/v1/meetings${qs}`).then((r) => r.data);
}

export async function createMeeting(input: CreateMeetingInput) {
  const { post } = await loadRequest();
  return post<{ data: MeetingRoom }>('/api/v1/meetings', buildCreatePayload(input)).then((r) => r.data);
}

export async function getMeeting(roomId: string) {
  const { get } = await loadRequest();
  return get<{ data: { room: MeetingRoom; participants?: MeetingParticipant[] } }>(
    `/api/v1/meetings/${roomId}`,
  ).then((r) => ({ ...r.data.room, participants: r.data.participants ?? r.data.room.participants }));
}

// 游标分页：beforeId 为空表示取最新一页
// 后端契约：查询参数名为 `before`（见 internal/handler/meeting.go ListMessages）
export async function listMessages(roomId: string, beforeId = '', limit = 100) {
  const { get } = await loadRequest();
  const qs = new URLSearchParams({ limit: String(limit) });
  if (beforeId) qs.set('before', beforeId);
  return get<{ data: MeetingMessage[] }>(`/api/v1/meetings/${roomId}/messages?${qs.toString()}`)
    .then((r) => r.data);
}

export async function sendMessage(roomId: string, content: string, mentionAgentIds: string[] = [], adjustFlow = false) {
  const { post } = await loadRequest();
  // 后端固定 202 Accepted，讨论结果经 SSE 推送
  return post<{ data: MeetingMessage }>(
    `/api/v1/meetings/${roomId}/messages`,
    buildMessagePayload(content, mentionAgentIds, adjustFlow),
  ).then((r) => r.data);
}

// 确认开场流程（幂等置 active + 后端自动开跑首轮）
export async function confirmMeetingFlow(roomId: string): Promise<void> {
  const { post } = await loadRequest();
  await post(`/api/v1/meetings/${roomId}/confirm`, {});
}

// 打断进行中的讨论轮
export async function interruptMeeting(roomId: string): Promise<void> {
  const { post } = await loadRequest();
  await post(`/api/v1/meetings/${roomId}/interrupt`, {});
}

// 生成主持人方案消息（后端同步等待 LLM 结果；消息本体经 SSE message.created 回流）
export async function generateSolution(roomId: string) {
  const { post } = await loadRequest();
  return post<{ data: MeetingMessage }>(`/api/v1/meetings/${roomId}/solution`, {}).then((r) => r.data);
}

// 历史方案产物列表（含正文），后端按 version 升序返回
export async function listArtifacts(roomId: string) {
  const { get } = await loadRequest();
  return get<{ data: MeetingArtifact[] }>(`/api/v1/meetings/${roomId}/artifacts`).then((r) => r.data);
}

// 把当前方案指针回滚到指定历史版本（成功后落一条系统消息，经 SSE 回流）
export async function rollbackSolution(roomId: string, version: number): Promise<void> {
  const { post } = await loadRequest();
  await post(`/api/v1/meetings/${roomId}/solution/rollback`, { version });
}

// 会议议程项列表（按 seq 升序）
export async function listAgenda(roomId: string) {
  const { get } = await loadRequest();
  return get<{ data: MeetingAgendaItem[] }>(`/api/v1/meetings/${roomId}/agenda`).then((r) => r.data);
}

// 人工推进议程：itemId 为空表示推进当前活跃项；返回最新议程
export async function advanceAgenda(roomId: string, itemId = '') {
  const { post } = await loadRequest();
  return post<{ data: MeetingAgendaItem[] }>(
    `/api/v1/meetings/${roomId}/agenda/advance`,
    { item_id: itemId },
  ).then((r) => r.data);
}

// 追加新议题并开启讨论（后端同时落一条主持人开题消息，经 SSE 回流）；返回最新议程
export async function appendAgenda(roomId: string, title: string, goal = '') {
  const { post } = await loadRequest();
  return post<{ data: MeetingAgendaItem[] }>(
    `/api/v1/meetings/${roomId}/agenda/append`,
    { title, goal },
  ).then((r) => r.data);
}

// 恢复指定议题继续讨论（其余 active 回退 pending）；返回最新议程
export async function reopenAgenda(roomId: string, itemId: string) {
  const { post } = await loadRequest();
  return post<{ data: MeetingAgendaItem[] }>(
    `/api/v1/meetings/${roomId}/agenda/reopen`,
    { item_id: itemId },
  ).then((r) => r.data);
}

// 局部更新会议（标题/描述/发言模式/护栏上限/结构性治理）
export async function updateMeeting(roomId: string, data: UpdateMeetingRequest) {
  const { patch } = await loadRequest();
  return patch<{ data: MeetingRoom }>(`/api/v1/meetings/${roomId}`, data).then((r) => r.data);
}

// 全量替换参会智能体（至少一个；讨论进行中后端 409）
export async function replaceParticipants(roomId: string, agentIds: string[]) {
  const { put } = await loadRequest();
  return put<{ data: MeetingParticipant[] }>(
    `/api/v1/meetings/${roomId}/participants`,
    { agent_ids: agentIds },
  ).then((r) => r.data);
}

// 归档会议（幂等）：归档后进入只读态，写操作被后端 403 拒绝
export async function archiveMeeting(roomId: string): Promise<void> {
  const { post } = await loadRequest();
  await post(`/api/v1/meetings/${roomId}/archive`, {});
}

// 删除会议（后端级联删除消息/参会者/产物，不可恢复）
export async function deleteMeeting(roomId: string): Promise<void> {
  const { del } = await loadRequest();
  await del(`/api/v1/meetings/${roomId}`);
}

// 下载方案 .md（服务端返回 Content-Disposition attachment 的 markdown 正文）
export async function exportArtifact(roomId: string, artifactId: string): Promise<Blob> {
  const { getDown } = await loadRequest();
  return getDown(`/api/v1/meetings/${roomId}/artifacts/${artifactId}/export`);
}

// 发起产物生成（后端 202 异步；终态经 SSE artifact.created / artifact.failed 推送）
export async function requestArtifact(roomId: string, kindType: ArtifactKindType, title: string, requirement: string) {
  const { post } = await loadRequest();
  return post<{ data: MeetingArtifact }>(`/api/v1/meetings/${roomId}/artifacts`, {
    kind_type: kindType, title, requirement,
  }).then((r) => r.data);
}

// 按原参数重新生成（202 异步，沿用原产物行）
export async function regenerateArtifact(roomId: string, artifactId: string) {
  const { post } = await loadRequest();
  return post<{ data: MeetingArtifact }>(
    `/api/v1/meetings/${roomId}/artifacts/${artifactId}/regenerate`, {},
  ).then((r) => r.data);
}

// 产物详情（含正文；generated 正文为生成时 agent 的最终回答）
export async function getArtifactDetail(roomId: string, artifactId: string) {
  const { get } = await loadRequest();
  return get<{ data: MeetingArtifact }>(`/api/v1/meetings/${roomId}/artifacts/${artifactId}`)
    .then((r) => r.data);
}

// generated 产物下载地址（服务端 302 到会话产物下载端点，fetch 自动跟随时携带鉴权头）
export function artifactDownloadUrl(roomId: string, artifactId: string): string {
  return `/api/v1/meetings/${roomId}/artifacts/${artifactId}/download`;
}

// ===== SSE 订阅 =====

export interface MeetingStreamHandlers {
  onEvent: (event: string, data: unknown) => void;
  onError?: (err: unknown) => void;
}

// 打开会议室 SSE 流；返回关闭函数。
// fetch-event-source 不会自动带鉴权头，需手动补齐（模式参考 api/chat/streame.ts）。
// 依赖同样按需加载，保持返回值为同步的关闭函数。
export function openMeetingStream(roomId: string, handlers: MeetingStreamHandlers): () => void {
  const controller = new AbortController();

  void (async () => {
    try {
      const [{ fetchEventSource }, { getApiBaseUrl }] = await Promise.all([
        import('@microsoft/fetch-event-source'),
        import('../../utils/api-base'),
      ]);
      if (controller.signal.aborted) return; // 加载期间已被关闭

      const token = localStorage.getItem('weknora_token') || '';
      const tenantId = localStorage.getItem('weknora_selected_tenant_id') || '';

      fetchEventSource(`${getApiBaseUrl()}${streamUrl(roomId)}`, {
        method: 'GET',
        headers: {
          Accept: 'text/event-stream',
          ...(token ? { Authorization: `Bearer ${token}` } : {}),
          ...(tenantId ? { 'X-Tenant-ID': tenantId } : {}),
        },
        signal: controller.signal,
        openWhenHidden: true, // 页面切到后台时不因 hidden 而断开
        onmessage(ev) {
          // 后端 c.SSEvent：ev.event 为事件名，ev.data 为 JSON 字符串
          let data: unknown = null;
          try {
            data = ev.data ? JSON.parse(ev.data) : null;
          } catch {
            data = ev.data; // 非 JSON 载荷原样透传
          }
          handlers.onEvent(ev.event || 'message', data);
        },
        onerror(err) {
          handlers.onError?.(err);
          throw err; // 抛出以终止 fetch-event-source 的自动重连
        },
      }).catch(() => {
        // 主动 abort 或 onerror 抛错都会走到这里；错误已通过 handlers.onError 上报
      });
    } catch (err) {
      handlers.onError?.(err);
    }
  })();

  return () => controller.abort();
}
