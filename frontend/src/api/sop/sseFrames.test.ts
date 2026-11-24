import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createSSEParser, emitFrame, type SopStreamEvent } from './sseFrames';

function collect() {
  const events: SopStreamEvent[] = [];
  return { events, onEvent: (ev: SopStreamEvent) => events.push(ev) };
}

test('按空行切帧并跳过注释心跳', () => {
  const { events, onEvent } = collect();
  const parser = createSSEParser(onEvent);
  parser.push(': connected\n\n');
  parser.push('data: {"type":"text_delta","text":"你"}\n\n');
  parser.push('data: {"type":"result"}\n\n');
  parser.flush();
  assert.deepEqual(events.map((e) => e.type), ['text_delta', 'result']);
  assert.equal(events[0].text, '你');
});

test('帧被拆到多个网络块时仍能还原', () => {
  const { events, onEvent } = collect();
  const parser = createSSEParser(onEvent);
  parser.push('data: {"type":"tool_call","tool_');
  parser.push('name":"search"}\n');
  parser.push('\n');
  assert.equal(events.length, 1);
  assert.equal(events[0].tool_name, 'search');
});

test('兼容 data: 无空格', () => {
  const { events, onEvent } = collect();
  emitFrame('data:{"type":"text_delta","text":"a"}', onEvent);
  assert.equal(events.length, 1);
  assert.equal(events[0].type, 'text_delta');
});

test('末尾没有空行时 flush 仍交付最后一帧', () => {
  const { events, onEvent } = collect();
  const parser = createSSEParser(onEvent);
  parser.push('data: {"type":"text_delta","text":"a"}\n\n');
  parser.push('data: {"type":"result"}');
  parser.flush();
  assert.deepEqual(events.map((e) => e.type), ['text_delta', 'result']);
});

test('非 JSON 帧忽略且不抛错', () => {
  const { events, onEvent } = collect();
  emitFrame('data: not-json', onEvent);
  emitFrame('data: {"type":"error","text":"boom"}', onEvent);
  assert.deepEqual(events.map((e) => e.type), ['error']);
});

test('多 data 行的帧逐行交付', () => {
  const { events, onEvent } = collect();
  emitFrame('data: {"type":"a"}\ndata: {"type":"b"}', onEvent);
  assert.deepEqual(events.map((e) => e.type), ['a', 'b']);
});
