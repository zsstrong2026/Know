import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildCreatePayload, buildMessagePayload, streamUrl } from './index';

test('buildCreatePayload 组装创建会议请求体', () => {
  const p = buildCreatePayload({
    title: '  新客服方案评审  ',
    description: '客服机器人选型',
    agentIds: ['a1', 'a2'],
    knowledgeBaseIds: ['kb1'],
    broadcastMode: 'sequential',
  });
  assert.equal(p.title, '新客服方案评审'); // 标题去首尾空格
  assert.equal(p.description, '客服机器人选型');
  assert.deepEqual(p.agent_ids, ['a1', 'a2']);
  assert.deepEqual(p.knowledge_base_ids, ['kb1']);
  assert.equal(p.broadcast_mode, 'sequential');
});

test('buildCreatePayload 拒绝空标题/空智能体', () => {
  assert.throws(() => buildCreatePayload({ title: ' ', description: '', agentIds: [], knowledgeBaseIds: [], broadcastMode: 'sequential' }), /title/);
  assert.throws(() => buildCreatePayload({ title: 't', description: '', agentIds: [], knowledgeBaseIds: [], broadcastMode: 'sequential' }), /agent/);
});

test('buildMessagePayload 组装发言请求体（含 mentions）', () => {
  const p = buildMessagePayload('大家好 @法务必答', ['agent-9']);
  assert.equal(p.content, '大家好 @法务必答');
  assert.deepEqual(p.mentioned_agent_ids, ['agent-9']);
});

test('streamUrl 指向会议室 SSE 端点', () => {
  assert.equal(streamUrl('room-1'), '/api/v1/meetings/room-1/stream');
});
