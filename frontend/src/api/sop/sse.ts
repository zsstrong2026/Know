// SOP 工作台的 SSE 客户端：运行直播（GET）与 AI 助手（POST）共用同一套帧解析。
//
// 为什么不用 axios：必须逐块读 ReadableStream。鉴权头复用 request.ts 的
// buildAuthHeaders，部署前缀复用 api-base，避免出现第二套 Header/URL 拼接逻辑。
import { buildAuthHeaders } from '@/utils/request';
import { getApiBaseUrl } from '@/utils/api-base';
import { createSSEParser, type SopStreamEvent } from './sseFrames';

export type { SopStreamEvent } from './sseFrames';

const BASE_URL = getApiBaseUrl();

/** 相对地址补上部署前缀（子路径部署时 /api 不在站点根）。 */
function resolveUrl(url: string): string {
  return url.startsWith('/') ? `${BASE_URL}${url}` : url;
}

async function consume(resp: Response, onEvent: (ev: SopStreamEvent) => void): Promise<void> {
  if (!resp.ok || !resp.body) throw new Error(`SSE ${resp.status}`);
  const reader = resp.body.getReader();
  const dec = new TextDecoder();
  const parser = createSSEParser(onEvent);
  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    parser.push(dec.decode(value, { stream: true }));
  }
  parser.push(dec.decode());
  parser.flush();
}

export async function streamSSE(
  url: string,
  body: unknown,
  onEvent: (ev: SopStreamEvent) => void,
  signal?: AbortSignal,
): Promise<void> {
  const resp = await fetch(resolveUrl(url), {
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
  await consume(await fetch(resolveUrl(url), { headers: buildAuthHeaders(), signal }), onEvent);
}
