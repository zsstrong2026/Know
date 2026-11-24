// SOP 工作台的 SSE 客户端：运行直播（GET）与 AI 助手（POST）共用同一套解析。
//
// 为什么不用 axios：必须逐块读 ReadableStream。鉴权头复用 request.ts 的
// buildAuthHeaders，避免出现第二套 Header 拼接逻辑。
import { buildAuthHeaders } from '@/utils/request';

/** 后端 executor.StreamEvent 的 JSON 形状（字段名即契约，不得改名）。 */
export interface SopStreamEvent {
  type: string;
  text?: string;
  tool_name?: string;
  payload?: unknown;
  at?: string;
  [key: string]: unknown;
}

async function consume(resp: Response, onEvent: (ev: SopStreamEvent) => void): Promise<void> {
  if (!resp.ok || !resp.body) throw new Error(`SSE ${resp.status}`);
  const reader = resp.body.getReader();
  const dec = new TextDecoder();
  let buf = '';
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buf += dec.decode(value, { stream: true });
    let idx: number;
    while ((idx = buf.indexOf('\n\n')) >= 0) {
      const frame = buf.slice(0, idx);
      buf = buf.slice(idx + 2);
      for (const line of frame.split('\n')) {
        if (!line.startsWith('data: ')) continue; // ": ping" / ": connected" 注释帧
        try {
          onEvent(JSON.parse(line.slice(6)) as SopStreamEvent);
        } catch {
          // 非 JSON 帧忽略：心跳与未知扩展帧不该打断订阅
        }
      }
    }
  }
}

export async function streamSSE(
  url: string,
  body: unknown,
  onEvent: (ev: SopStreamEvent) => void,
  signal?: AbortSignal,
): Promise<void> {
  const resp = await fetch(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...buildAuthHeaders() },
    body: JSON.stringify(body),
    signal,
  });
  await consume(resp, onEvent);
}

export async function streamGet(
  url: string,
  onEvent: (ev: SopStreamEvent) => void,
  signal?: AbortSignal,
): Promise<void> {
  await consume(await fetch(url, { headers: buildAuthHeaders(), signal }), onEvent);
}
