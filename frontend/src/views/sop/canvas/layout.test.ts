import { test } from 'node:test';
import assert from 'node:assert/strict';
import { topoLayers, autoLayout } from './layout';

test('拓扑分层上游恒在左', () => {
  const def: any = { nodes: [{ id: 'c' }, { id: 'a' }, { id: 'b' }], edges: [{ source: 'a', target: 'b' }, { source: 'b', target: 'c' }] };
  assert.deepEqual(topoLayers(def), [['a'], ['b'], ['c']]);
  const out = autoLayout(def);
  const x = (id: string) => out.nodes.find((n: any) => n.id === id)!.position.x;
  assert.ok(x('a') < x('b') && x('b') < x('c'));
  assert.equal(x('b') - x('a'), 280);
});

test('同层行距与环检测', () => {
  const def: any = { nodes: [{ id: 'a' }, { id: 'b' }, { id: 'c' }], edges: [{ source: 'a', target: 'c' }, { source: 'b', target: 'c' }] };
  const out = autoLayout(def);
  const ys = out.nodes
    .filter((n: any) => n.id !== 'c')
    .map((n: any) => n.position.y)
    .sort((p: number, q: number) => p - q);
  assert.ok(ys[1] - ys[0] >= 110);
  assert.throws(
    () => topoLayers({ nodes: [{ id: 'a' }, { id: 'b' }], edges: [{ source: 'a', target: 'b' }, { source: 'b', target: 'a' }] } as any),
    /环/,
  );
});

test('孤立节点单独成层且不改入参', () => {
  const def: any = { nodes: [{ id: 'a', position: { x: 5, y: 5 } }, { id: 'z' }], edges: [] };
  const out = autoLayout(def);
  assert.equal(out.nodes.length, 2);
  assert.notEqual(out, def);
  assert.deepEqual(def.nodes[0].position, { x: 5, y: 5 });
});
