// ===== 类型定义（与后端 internal/types/meeting.go 对齐）=====

// 广播模式：顺序发言 / 并行发言
export type BroadcastMode = 'sequential' | 'parallel';
// 参会者类型：主持人（唯一）/ 智能体
export type ParticipantKind = 'host' | 'agent';
// 消息发送者类型
export type SenderKind = 'user' | 'agent' | 'host' | 'system';

export interface MeetingRoom {
  id: string;
  title: string;
  description: string;
  status: 'active' | 'archived';
  broadcast_mode: BroadcastMode;
  knowledge_base_ids: string[];
  created_by: string;
  created_at: string;
  updated_at: string;
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
  review_state: 'none' | 'pending' | 'adopted' | 'rejected';
  is_solution: boolean;
  solution_version: number;
  created_at: string;
}

export interface CreateMeetingInput {
  title: string;
  description: string;
  agentIds: string[];
  knowledgeBaseIds: string[];
  broadcastMode: BroadcastMode;
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
  };
}

// 组装发言请求体：@ 提及的智能体可选
export function buildMessagePayload(content: string, mentionAgentIds: string[] = []) {
  return { content, mentioned_agent_ids: mentionAgentIds };
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

export async function listMeetings() {
  const { get } = await loadRequest();
  return get<{ data: MeetingRoom[] }>('/api/v1/meetings').then((r) => r.data);
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

export async function sendMessage(roomId: string, content: string, mentionAgentIds: string[] = []) {
  const { post } = await loadRequest();
  // 后端固定 202 Accepted，讨论结果经 SSE 推送
  return post<{ data: MeetingMessage }>(
    `/api/v1/meetings/${roomId}/messages`,
    buildMessagePayload(content, mentionAgentIds),
  ).then((r) => r.data);
}

// 打断进行中的讨论轮
export async function interruptMeeting(roomId: string): Promise<void> {
  const { post } = await loadRequest();
  await post(`/api/v1/meetings/${roomId}/interrupt`, {});
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
