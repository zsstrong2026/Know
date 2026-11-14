import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  buildCreatePayload, buildMessagePayload, streamUrl, isModelHomogeneous, isOpeningAnalyzing,
  artifactDownloadUrl, type CreateMeetingInput,
} from './index';

test('buildCreatePayload 只提交名称、补充背景与参会智能体', () => {
  const p = buildCreatePayload({
    title: '  新客服方案评审  ',
    description: '客服机器人选型',
    agentIds: ['a1', 'a2'],
  });
  assert.equal(p.title, '新客服方案评审'); // 标题去首尾空格
  assert.equal(p.description, '客服机器人选型');
  assert.deepEqual(p.agent_ids, ['a1', 'a2']);
  // 其余治理参数由后端统一内置，前端不再提交
  assert.deepEqual(Object.keys(p).sort(), ['agent_ids', 'description', 'title']);
});

test('buildCreatePayload 拒绝空标题/空智能体', () => {
  const base: CreateMeetingInput = { title: 't', description: '', agentIds: ['a1'] };
  assert.throws(() => buildCreatePayload({ ...base, title: ' ' }), /title/);
  assert.throws(() => buildCreatePayload({ ...base, agentIds: [] }), /agent/);
  assert.equal(buildCreatePayload(base).description, '');
});

test('isModelHomogeneous 判定模型同质（≥2 个且模型标识唯一）', () => {
  assert.equal(isModelHomogeneous([]), false);                       // 空选择：不提示
  assert.equal(isModelHomogeneous(['m1']), false);                   // 单个：无同质概念
  assert.equal(isModelHomogeneous([undefined, undefined]), true);    // 均跟随默认模型
  assert.equal(isModelHomogeneous(['', '  ']), true);                // 空白等价于默认
  assert.equal(isModelHomogeneous(['m1', 'm1']), true);              // 同模型
  assert.equal(isModelHomogeneous(['m1', 'm2']), false);             // 异构：不提示
  assert.equal(isModelHomogeneous(['m1', undefined]), false);        // 一绑定一默认：异构
});

test('buildMessagePayload 组装发言请求体（含 mentions）', () => {
  const p = buildMessagePayload('大家好 @法务必答', ['agent-9']);
  assert.equal(p.content, '大家好 @法务必答');
  assert.deepEqual(p.mentioned_agent_ids, ['agent-9']);
});

test('buildMessagePayload 支持 adjust_flow', () => {
  const plain = buildMessagePayload('大家好', ['agent-9']);
  assert.equal(plain.adjust_flow, false); // 默认普通发言
  const adjust = buildMessagePayload('先聊性能', [], true);
  assert.equal(adjust.adjust_flow, true); // 调整流程模式
  assert.deepEqual(adjust.mentioned_agent_ids, []);
});

test('isOpeningAnalyzing 判定主持人是否仍在分析议题', () => {
  const user = { sender_kind: 'user' as const, payload: null };
  const opening = { sender_kind: 'host' as const, payload: { stage: 'opening' } };
  const system = { sender_kind: 'system' as const, payload: null };

  assert.equal(isOpeningAnalyzing('active', []), false); // 已确认的房间不再等待开场
  assert.equal(isOpeningAnalyzing('awaiting_confirmation', []), true); // 刚进房、开场未回流
  assert.equal(isOpeningAnalyzing('awaiting_confirmation', [opening]), false); // 开场已回流
  assert.equal(isOpeningAnalyzing('awaiting_confirmation', [opening, user]), true); // 调整流程后等重出
  assert.equal(isOpeningAnalyzing('awaiting_confirmation', [system]), false); // 开场失败兜底不再等待
  assert.equal(isOpeningAnalyzing(undefined, []), false); // 房间未加载
});

test('streamUrl 指向会议室 SSE 端点', () => {
  assert.equal(streamUrl('room-1'), '/api/v1/meetings/room-1/stream');
});

test('artifactDownloadUrl 指向会议产物下载端点', () => {
  assert.equal(artifactDownloadUrl('room-1', 'art-9'), '/api/v1/meetings/room-1/artifacts/art-9/download');
});
