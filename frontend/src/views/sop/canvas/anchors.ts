// 端口注册表：节点卡挂载时登记真实 DOM，边据此按实测圆心绘制。
export type PortKind = 'in' | 'success' | 'reject';

const registry = new Map<string, HTMLElement>();

function key(nodeId: string, kind: PortKind): string {
  return `${nodeId}:${kind}`;
}

export const portRegistry = {
  register(nodeId: string, kind: PortKind, el: HTMLElement): void {
    registry.set(key(nodeId, kind), el);
  },
  unregister(nodeId: string, kind: PortKind): void {
    registry.delete(key(nodeId, kind));
  },
  get(nodeId: string, kind: PortKind): HTMLElement | undefined {
    return registry.get(key(nodeId, kind));
  },
  clear(): void {
    registry.clear();
  },
};

export interface Point {
  x: number;
  y: number;
}

export interface Viewport {
  x: number;
  y: number;
  zoom: number;
}

/**
 * 由端口 DOM 实测圆心换算成画布坐标：
 * 先减容器 rect（屏幕→容器局部），再除以 zoom 并减平移（局部→画布）。
 */
export function computeEdgeAnchors(
  flowEl: HTMLElement,
  from: { nodeId: string; kind: PortKind },
  to: { nodeId: string; kind: PortKind },
  viewport: Viewport,
): { source: Point; target: Point } | null {
  const fromEl = portRegistry.get(from.nodeId, from.kind);
  const toEl = portRegistry.get(to.nodeId, to.kind);
  if (!fromEl || !toEl) return null;

  const base = flowEl.getBoundingClientRect();
  const zoom = viewport.zoom || 1;
  const toPoint = (el: HTMLElement): Point => {
    const rect = el.getBoundingClientRect();
    return {
      x: (rect.left + rect.width / 2 - base.left) / zoom - viewport.x,
      y: (rect.top + rect.height / 2 - base.top) / zoom - viewport.y,
    };
  };
  return { source: toPoint(fromEl), target: toPoint(toEl) };
}

export interface MiniBox {
  minX: number;
  minY: number;
  width: number;
  height: number;
}

/**
 * 小地图归一化：按节点 bbox（含卡尺寸）算视口框，并给出把画布坐标
 * 映射到缩略图坐标的换算函数。纯函数，便于单独校验。
 */
export function minimapTransform(
  nodes: { position?: { x: number; y: number } }[],
  cardWidth: number,
  cardHeight: number,
): MiniBox {
  if (!nodes.length) return { minX: 0, minY: 0, width: 1, height: 1 };
  const xs = nodes.map((node) => node.position?.x || 0);
  const ys = nodes.map((node) => node.position?.y || 0);
  const minX = Math.min(...xs) - 40;
  const minY = Math.min(...ys) - 40;
  const width = Math.max(...xs) + cardWidth + 40 - minX;
  const height = Math.max(...ys) + cardHeight + 40 - minY;
  return { minX, minY, width: Math.max(width, 1), height: Math.max(height, 1) };
}

/** 画布坐标 → 小地图 viewBox 坐标（同一坐标系，直接可用）。 */
export function canvasToMinimap(point: Point, box: MiniBox): Point {
  return { x: point.x - box.minX, y: point.y - box.minY };
}
