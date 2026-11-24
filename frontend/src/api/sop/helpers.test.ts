import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  EXEC_STATES,
  EXEC_STATE_LABELS,
  SOURCE_LABELS,
  ITEM_STATUS_LABELS,
  RUN_STATUS_LABELS,
  deriveExecState,
  applyNodeOps,
  gateCheckSummary,
  intervalSummary,
  validateProjectCode,
  validateRepoURL,
  repoDisplayName,
  matchExecOptions,
  KANBAN_COLUMNS,
  canTransitItem,
  mergeActivities,
  outcomeSummary,
  upsertBreakdownBlock,
  parseBreakdownCard,
  formatDiffCards,
  diffDefinitions,
  progressText,
  shortSha,
  formatBytes,
} from './helpers';

test('五态标签与枚举全覆盖', () => {
  assert.equal(EXEC_STATES.length, 5);
  for (const s of EXEC_STATES) assert.ok(EXEC_STATE_LABELS[s]);
  assert.equal(EXEC_STATE_LABELS.intervene, '需要介入');
  assert.equal(EXEC_STATE_LABELS.pr, '待处理 PR');
});

test('执行情况派生与后端同口径', () => {
  assert.equal(deriveExecState('todo', '', false), 'none');
  assert.equal(deriveExecState('in_progress', 'running', true), 'running');
  assert.equal(deriveExecState('in_review', 'succeeded', true), 'pr');
  assert.equal(deriveExecState('in_progress', 'failed', true), 'intervene');
  assert.equal(deriveExecState('done', '', false), 'closed');
  assert.equal(deriveExecState('closed', '', false), 'closed');
  assert.equal(deriveExecState('in_progress', 'paused', true), 'running');
  assert.equal(deriveExecState('in_progress', 'cancelled', true), 'none');
  assert.equal(deriveExecState('todo', 'queued', true), 'none');
});

test('来源六键中文映射', () => {
  assert.equal(SOURCE_LABELS.customer, '客户反馈');
  assert.equal(SOURCE_LABELS.competitor, '竞品分析');
  assert.equal(Object.keys(SOURCE_LABELS).length, 6);
});

test('状态映射覆盖 8 事项态与 7 运行态', () => {
  const itemLabels: Record<string, string> = ITEM_STATUS_LABELS;
  const runLabels: Record<string, string> = RUN_STATUS_LABELS;
  for (const k of ['backlog', 'todo', 'in_progress', 'in_review', 'blocked', 'done', 'closed', 'cancelled']) {
    assert.ok(itemLabels[k], `missing item status ${k}`);
  }
  for (const k of ['queued', 'running', 'waiting', 'paused', 'succeeded', 'failed', 'cancelled']) {
    assert.ok(runLabels[k], `missing run status ${k}`);
  }
});

test('applyNodeOps 确定性应用与引用清理', () => {
  const def: any = {
    nodes: [
      { id: 'n1', name: '分析', task_refs: [], reject_to: '' },
      { id: 'n2', name: '实现', task_refs: ['n1'], reject_to: 'n1' },
    ],
    edges: [{ from: 'n1', to: 'n2' }],
  };
  const out = applyNodeOps(def, [
    { op: 'set', node_id: 'n1', fields: { name: '需求分析' } },
    { op: 'remove_node', node_id: 'n1' },
  ] as any);
  assert.equal(out.nodes.length, 1);
  assert.deepEqual(out.nodes[0].task_refs, []);
  assert.equal(out.nodes[0].reject_to, '');
  assert.deepEqual(out.edges, []);
  // 输入不被打点
  assert.equal(def.nodes.length, 2);
});

test('applyNodeOps 支持增删节点与边', () => {
  const def: any = { nodes: [{ id: 'a', task_refs: [], reject_to: '' }], edges: [] };
  const out = applyNodeOps(def, [
    { op: 'add_node', node: { id: 'b', name: '新增' } },
    { op: 'add_edge', edge: { from: 'a', to: 'b' } },
  ] as any);
  assert.equal(out.nodes.length, 2);
  assert.deepEqual(out.edges, [{ from: 'a', to: 'b' }]);
  const removed = applyNodeOps(out, [{ op: 'remove_edge', edge: { from: 'a', to: 'b' } }] as any);
  assert.deepEqual(removed.edges, []);
});

test('校验三档摘要与调度摘要', () => {
  assert.equal(gateCheckSummary({ type: 'exists' } as any), '文件交了就行');
  assert.equal(gateCheckSummary({ type: 'contains', keyword: '验收' } as any), '必须包含关键词：验收');
  assert.equal(gateCheckSummary({ type: 'command', command: 'make test' } as any), '自定义命令');
  assert.equal(intervalSummary({ mode: 'interval', interval_key: 'daily9' } as any), '每天 09:00');
  assert.equal(intervalSummary({ mode: 'interval', interval_key: 'weekly1' } as any), '每周一 09:00');
  assert.equal(
    intervalSummary({ mode: 'cron', cron: '0 9 * * *', timezone: 'Asia/Shanghai' } as any),
    'Cron：0 9 * * *（Asia/Shanghai）',
  );
});

test('项目前缀与仓库地址校验', () => {
  assert.ok(validateProjectCode('TP'));
  assert.ok(!validateProjectCode('1bad'));
  assert.ok(!validateProjectCode('a'));
  assert.ok(!validateProjectCode('ABCDEFGHIJK'));
  assert.ok(validateRepoURL('https://github.com/a/b.git'));
  assert.ok(validateRepoURL('git@github.com:a/b.git'));
  assert.ok(validateRepoURL('ssh://git@host/a/b'));
  assert.ok(!validateRepoURL('ftp://x/y'));
  assert.ok(!validateRepoURL('https://'));
});

test('repoDisplayName 取 basename 去 .git', () => {
  assert.equal(repoDisplayName('https://github.com/a/b.git'), 'b');
  assert.equal(repoDisplayName('git@github.com:a/my-repo.git'), 'my-repo');
  assert.equal(repoDisplayName('https://github.com/a/trailing/'), 'trailing');
  assert.equal(repoDisplayName(''), '');
});

test('执行情况搜索只匹配选项名不匹配计数', () => {
  const opts = EXEC_STATES.map((s, i) => ({ value: s, label: EXEC_STATE_LABELS[s], count: 3 + i }));
  assert.deepEqual(matchExecOptions(opts, '介入').map((o) => o.value), ['intervene']);
  assert.deepEqual(matchExecOptions(opts, '3').map((o) => o.value), []); // 数字不匹配
  assert.deepEqual(matchExecOptions(opts, '').map((o) => o.value), EXEC_STATES);
  assert.deepEqual(matchExecOptions(opts, '  ').map((o) => o.value), EXEC_STATES);
  assert.deepEqual(matchExecOptions(opts, '待处理').map((o) => o.value), ['pr']);
});

test('进度、短 sha 与字节格式化', () => {
  assert.equal(progressText(0, 0), '—');
  assert.equal(progressText(2, 5), '2/5');
  assert.equal(shortSha('0123456789abcdef'.repeat(4)), '01234567');
  assert.equal(shortSha(''), '');
  assert.equal(formatBytes(512), '512 B');
  assert.equal(formatBytes(2048), '2.0 KB');
});

test('看板列与状态机镜像', () => {
  assert.deepEqual(KANBAN_COLUMNS, ['backlog', 'todo', 'in_progress', 'in_review', 'blocked', 'done', 'closed']);
  assert.ok(canTransitItem('backlog', 'todo'));
  assert.ok(!canTransitItem('backlog', 'done')); // 非法跨跃与后端一致
  assert.ok(canTransitItem('in_progress', 'blocked'));
  assert.ok(canTransitItem('done', 'closed'));
  assert.ok(canTransitItem('closed', 'todo'));
  assert.ok(canTransitItem('blocked', 'cancelled'));
  assert.ok(!canTransitItem('done', 'in_progress'));
  assert.ok(!canTransitItem('', 'todo'));
  assert.ok(canTransitItem('todo', 'todo')); // 同状态幂等放行
});

test('活动合并降序与 outcome 摘要', () => {
  const merged = mergeActivities(
    [{ at: '2026-11-19T10:00:00Z', kind: 'business', text: '创建' } as any],
    [{ at: '2026-11-19T11:00:00Z', kind: 'audit', text: '编辑' } as any],
  );
  assert.equal(merged[0].text, '编辑');
  assert.equal(outcomeSummary({ value_achieved: 'achieved', summary: '上线达标' } as any), '上线达标');
  assert.equal(outcomeSummary({} as any), '尚未回填');
  assert.equal(outcomeSummary({ summary: '  ' } as any), '尚未回填');
  assert.equal(outcomeSummary({ value_achieved: 'partial' } as any), '部分达成');
});

test('拆解块重复写入为覆盖不重复', () => {
  const block = '目标：X\n子事项：a、b';
  const d1 = upsertBreakdownBlock('原始描述', block);
  assert.ok(d1.includes('原始描述'));
  assert.ok(d1.includes('<!-- sop-ai-breakdown -->'));
  const d2 = upsertBreakdownBlock(d1, '目标：Y');
  assert.equal(d2.match(/<!-- sop-ai-breakdown -->/g)?.length, 1); // 覆盖
  assert.ok(d2.includes('目标：Y'));
  assert.ok(!d2.includes('目标：X'));
  assert.ok(d2.includes('原始描述')); // 用户原文不动
});

test('拆解卡解析容错', () => {
  const card = parseBreakdownCard('前言 ```json\n{"goal":"G","sub_items":[{"title":"a","executor":"platform_agent"}],"acceptance":"A"}\n``` 后语');
  assert.equal(card?.goal, 'G');
  assert.equal(card?.sub_items[0].executor, 'platform_agent');
  assert.equal(parseBreakdownCard('没有 JSON'), null);
  assert.equal(parseBreakdownCard(''), null);
});

test('差异卡原值→新值白话格式化', () => {
  const def: any = {
    nodes: [{ id: 'n1', name: '分析', executor: { kind: 'platform_agent', agent_id: '' }, on_failure: 'loop' }],
    edges: [],
  };
  const cards = formatDiffCards(def, [
    {
      op: 'set',
      node_id: 'n1',
      fields: { executor: { kind: 'platform_agent', agent_id: 'a1' }, on_failure: 'reject' },
      reason: '需要专业角色',
    } as any,
  ]);
  assert.equal(cards.length, 2);
  const exec = cards.find((c) => c.field.includes('执行'))!;
  assert.ok(exec.from.includes('未配置') || exec.from.includes('平台'));
  assert.ok(exec.to.includes('a1') || exec.to.includes('角色'));
  assert.equal(exec.reason, '需要专业角色');
  const fail = cards.find((c) => c.field.includes('失败'))!;
  assert.equal(fail.from, '原地重试');
  assert.equal(fail.to, '打回上游');
});

test('两份定义逐字段比对出差', () => {
  const before: any = { nodes: [{ id: 'n1', name: '分析', on_failure: 'loop', task_prompt: '旧' }], edges: [] };
  const after: any = { nodes: [{ id: 'n1', name: '分析', on_failure: 'reject', task_prompt: '新' }, { id: 'n2', name: '实现' }], edges: [] };
  const cards = diffDefinitions(before, after);
  const fields = cards.map((c) => c.field);
  assert.ok(fields.includes('失败策略'));
  assert.ok(fields.includes('这一步做什么'));
  assert.ok(fields.includes('新节点'));
  assert.equal(cards.find((c) => c.field === '失败策略')!.to, '打回上游');
  const removed = diffDefinitions(after, before);
  assert.ok(removed.some((c) => c.field === '删除节点'));
});
