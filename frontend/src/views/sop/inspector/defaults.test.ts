import { test } from 'node:test';
import assert from 'node:assert/strict';
import { inspectorDefaults, rejectCandidates, failureHelpText } from './defaults';

test('新节点默认值与 spec §16.15 一致', () => {
  const n = inspectorDefaults('agent');
  assert.equal(n.skills, null);
  assert.deepEqual(n.mcp, ['know-kb']);
  assert.equal(n.carry_over, true);
  assert.equal(n.max_attempts, 3);
  assert.equal(n.on_failure, 'loop');
  assert.equal(n.executor.kind, 'platform_agent');
  assert.equal(inspectorDefaults('script').executor.kind, 'script');
  assert.equal(inspectorDefaults('approval').executor.kind, 'platform_agent');
  assert.equal(inspectorDefaults('agent').artifact_outputs.length, 0);
  assert.equal(inspectorDefaults('agent').timeout_minutes, 60);
});

test('打回候选=上游祖先且文案随策略切换', () => {
  const def: any = {
    nodes: [{ id: 'a' }, { id: 'b' }, { id: 'c' }],
    edges: [{ source: 'a', target: 'b' }, { source: 'b', target: 'c' }],
  };
  assert.deepEqual(rejectCandidates(def, 'c').map((n: any) => n.id), ['a', 'b']);
  assert.deepEqual(rejectCandidates(def, 'a').map((n: any) => n.id), []);
  assert.notEqual(failureHelpText('loop'), failureHelpText('reject'));
  assert.ok(failureHelpText('human').includes('人'));
  assert.notEqual(failureHelpText('fail'), failureHelpText('loop'));
});
