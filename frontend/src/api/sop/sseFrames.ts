// SSE 帧解析：纯逻辑，不 import 任何带 `@/` 别名的模块，
// 以便 `node:test` 直接加载（fetch/鉴权留在 sse.ts）。
export interface SopStreamEvent {
  type: string;
  text?: string;
  tool_name?: string;
  payload?: unknown;
  at?: string;
  [key: string]: unknown;
}

/** 把单个 SSE 帧里的 `data:` 行解析成事件；注释帧与非 JSON 帧一律忽略。 */
export function emitFrame(frame: string, onEvent: (ev: SopStreamEvent) => void): void {
  for (const line of frame.split('\n')) {
    if (!line.startsWith('data:')) continue; // ": ping" / ": connected"
    const raw = line.slice(5).trim();
    if (!raw) continue;
    try {
      onEvent(JSON.parse(raw) as SopStreamEvent);
    } catch {
      // 非 JSON 帧：心跳与未知扩展帧不该打断订阅
    }
  }
}

/**
 * 增量解析器：喂入任意切分的文本块，按空行切帧。
 * `flush()` 处理流结束但末尾没有空行的情况（最后一帧不能丢）。
 */
export function createSSEParser(onEvent: (ev: SopStreamEvent) => void) {
  let buf = '';
  return {
    push(chunk: string): void {
      buf += chunk;
      let idx: number;
      while ((idx = buf.indexOf('\n\n')) >= 0) {
        const frame = buf.slice(0, idx);
        buf = buf.slice(idx + 2);
        emitFrame(frame, onEvent);
      }
    },
    flush(): void {
      if (buf.trim()) emitFrame(buf, onEvent);
      buf = '';
    },
  };
}
