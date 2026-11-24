// 画布自动排版：按依赖边做 Kahn 分层，上游恒在左；环直接报白话错误。
import type { SopDefinition, SopNode } from '@/api/sop';

const LAYER_GAP_X = 250;
const MIN_ROW_GAP_Y = 110;
const DEFAULT_ROW_GAP_Y = 170;

interface EdgeLike {
  source?: string;
  target?: string;
  from?: string;
  to?: string;
}

function edgeSource(edge: EdgeLike): string {
  return String(edge.source ?? edge.from ?? '');
}

function edgeTarget(edge: EdgeLike): string {
  return String(edge.target ?? edge.to ?? '');
}

/** 依赖边分层：同一层内的节点互不依赖。有环时抛错（画布不该出现环）。 */
export function topoLayers(def: Pick<SopDefinition, 'nodes' | 'edges'>): string[][] {
  const ids = (def.nodes || []).map((node) => node.id);
  const known = new Set(ids);
  const indegree = new Map<string, number>(ids.map((id) => [id, 0]));
  const next = new Map<string, string[]>(ids.map((id) => [id, []]));

  for (const edge of (def.edges || []) as EdgeLike[]) {
    const source = edgeSource(edge);
    const target = edgeTarget(edge);
    if (!source || !target || source === target) continue;
    if (!known.has(source) || !known.has(target)) continue;
    next.get(source)!.push(target);
    indegree.set(target, (indegree.get(target) || 0) + 1);
  }

  const layers: string[][] = [];
  let frontier = ids.filter((id) => (indegree.get(id) || 0) === 0);
  // 保持定义顺序，避免同层顺序随 Map 迭代抖动
  frontier.sort((a, b) => ids.indexOf(a) - ids.indexOf(b));
  let visited = 0;

  while (frontier.length) {
    layers.push(frontier);
    visited += frontier.length;
    const upcoming: string[] = [];
    for (const id of frontier) {
      for (const target of next.get(id) || []) {
        const left = (indegree.get(target) || 0) - 1;
        indegree.set(target, left);
        if (left === 0) upcoming.push(target);
      }
    }
    upcoming.sort((a, b) => ids.indexOf(a) - ids.indexOf(b));
    frontier = upcoming;
  }

  if (visited !== ids.length) {
    throw new Error('流程存在环：请检查节点依赖，环上的节点无法分层排版');
  }
  return layers;
}

/**
 * 自动排版：返回新定义（不修改入参）。列距固定 250，行距按同层节点数
 * 取 max(110, 170)，保证双出口节点卡（约 170 高）不会重叠。
 */
export function autoLayout(def: SopDefinition): SopDefinition {
  const layers = topoLayers(def);
  const rowGap = layers.some((layer) => layer.length > 1) ? DEFAULT_ROW_GAP_Y : MIN_ROW_GAP_Y;
  const positions = new Map<string, { x: number; y: number }>();

  layers.forEach((layer, layerIndex) => {
    const offset = ((layer.length - 1) * rowGap) / 2;
    layer.forEach((id, rowIndex) => {
      positions.set(id, {
        x: layerIndex * LAYER_GAP_X,
        y: Math.round(rowIndex * rowGap - offset),
      });
    });
  });

  return {
    ...def,
    nodes: (def.nodes || []).map((node) => ({
      ...node,
      position: positions.get(node.id) || node.position || { x: 0, y: 0 },
    })) as SopNode[],
    edges: (def.edges || []).map((edge) => ({ ...edge })),
    input_schema: [...(def.input_schema || [])],
  };
}
