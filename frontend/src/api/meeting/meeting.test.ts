import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildCreatePayload, buildMessagePayload, streamUrl, isModelHomogeneous, artifactDownloadUrl, type CreateMeetingInput } from './index';

test('buildCreatePayload 组装创建会议请求体', () => {
  const p = buildCreatePayload({
    title: '  新客服方案评审  ',
    description: '客服机器人选型',
    agentIds: ['a1', 'a2'],
    knowledgeBaseIds: ['kb1'],
    broadcastMode: 'sequential',
    routingMode: 'host-assign',
    hostEveryNRounds: 3,
    consensusDetectionEnabled: true,
    webSearchEnabled: true,
  });
  assert.equal(p.title, '新客服方案评审'); // 标题去首尾空格
  assert.equal(p.description, '客服机器人选型');
  assert.deepEqual(p.agent_ids, ['a1', 'a2']);
  assert.deepEqual(p.knowledge_base_ids, ['kb1']);
  assert.equal(p.broadcast_mode, 'sequential');
  assert.equal(p.routing_mode, 'host-assign');
  assert.equal(p.host_every_n_rounds, 3); // 主持人介入间隔透传
  assert.equal(p.consensus_detection_enabled, true); // 共识检测开关透传
  assert.equal(p.web_search_enabled, true); // 会议级联网开关透传
});

test('buildCreatePayload 拒绝空标题/空智能体', () => {
  // 显式标注类型而非 as const，避免 readonly 数组无法赋给可变字段类型
  const base: CreateMeetingInput = {
    title: 't', description: '', agentIds: ['a1'], knowledgeBaseIds: [],
    broadcastMode: 'sequential', routingMode: 'free', hostEveryNRounds: 0,
    consensusDetectionEnabled: false, webSearchEnabled: false,
  };
  assert.throws(() => buildCreatePayload({ ...base, title: ' ' }), /title/);
  assert.throws(() => buildCreatePayload({ ...base, agentIds: [] }), /agent/);
  assert.equal(buildCreatePayload(base).host_every_n_rounds, 0); // 默认不介入
  assert.equal(buildCreatePayload(base).consensus_detection_enabled, false); // 默认关闭共识检测
  assert.equal(buildCreatePayload(base).web_search_enabled, false); // 默认关闭联网
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

test('streamUrl 指向会议室 SSE 端点', () => {
  assert.equal(streamUrl('room-1'), '/api/v1/meetings/room-1/stream');
});

test('artifactDownloadUrl 指向会议产物下载端点', () => {
  assert.equal(artifactDownloadUrl('room-1', 'art-9'), '/api/v1/meetings/room-1/artifacts/art-9/download');
});
