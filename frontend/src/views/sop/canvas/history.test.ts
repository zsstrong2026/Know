import { test } from 'node:test';
import assert from 'node:assert/strict';
import { SnapshotHistory } from './history';

test('快照历史往返与 redo 清空', () => {
  const h = new SnapshotHistory<number>(50);
  h.push(1);
  h.push(2);
  h.push(3);
  assert.equal(h.undo(), 2);
  assert.equal(h.undo(), 1);
  assert.equal(h.redo(), 2);
  h.push(9); // 新变更清空 redo
  assert.equal(h.canRedo, false);
  assert.equal(h.undo(), 2);
});

test('上限 50 与空栈置灰', () => {
  const h = new SnapshotHistory<number>(50);
  assert.equal(h.canUndo, false);
  for (let i = 0; i < 60; i++) h.push(i);
  let n = 0;
  while (h.canUndo) {
    h.undo();
    n++;
  }
  assert.equal(n, 49); // 60 快照保留最近 50，可回退 49 步
});

test('reset 清空两栈', () => {
  const h = new SnapshotHistory<string>(5);
  h.push('a');
  h.push('b');
  h.undo();
  h.reset();
  assert.equal(h.canUndo, false);
  assert.equal(h.canRedo, false);
});
