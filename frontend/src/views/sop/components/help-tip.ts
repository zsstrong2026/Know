// ? 气泡的单例引擎：整个应用只有一个 #sop-help-tip 节点，反复悬停不会堆积。

export interface TipRect {
  top: number;
  left: number;
  width: number;
  height: number;
  bottom: number;
  right: number;
}

export interface TipSize {
  width: number;
  height: number;
}

export interface ViewportSize {
  width: number;
  height: number;
}

export interface TipPosition {
  top: number;
  left: number;
  flipped: boolean;
}

const EDGE_GAP = 8;
const ANCHOR_GAP = 8;

/**
 * 气泡定位（纯函数）：默认在锚点上方居中；上方空间不足时翻到下方；
 * 横向钳位到视口内（左右各留 8px）。
 */
export function clampTipPosition(anchor: TipRect, tip: TipSize, viewport: ViewportSize): TipPosition {
  const aboveTop = anchor.top - tip.height - ANCHOR_GAP;
  const flipped = aboveTop < EDGE_GAP;
  const top = flipped ? anchor.bottom + ANCHOR_GAP : aboveTop;

  const centered = anchor.left + anchor.width / 2 - tip.width / 2;
  const maxLeft = Math.max(EDGE_GAP, viewport.width - tip.width - EDGE_GAP);
  const left = Math.min(Math.max(EDGE_GAP, centered), maxLeft);

  return { top, left, flipped };
}

let tipEl: HTMLElement | null = null;
let listenersBound = false;

function ensureTip(): HTMLElement {
  if (tipEl && document.body.contains(tipEl)) return tipEl;
  const el = document.createElement('div');
  el.id = 'sop-help-tip';
  el.setAttribute('role', 'tooltip');
  Object.assign(el.style, {
    position: 'fixed',
    zIndex: '9999',
    maxWidth: '280px',
    padding: '8px 10px',
    borderRadius: '8px',
    background: 'rgba(24, 24, 24, 0.94)',
    color: '#fff',
    fontSize: '12px',
    lineHeight: '1.6',
    boxShadow: '0 6px 24px rgba(0, 0, 0, 0.24)',
    pointerEvents: 'none',
    opacity: '0',
    transition: 'opacity 120ms ease',
  });
  document.body.appendChild(el);
  tipEl = el;
  return el;
}

function bindGlobalListeners() {
  if (listenersBound) return;
  listenersBound = true;
  // scroll 用 capture 才能收到内部滚动容器的事件
  document.addEventListener('scroll', hideTip, true);
  document.addEventListener('click', hideTip, true);
}

/** 显示气泡（全局单例）。text 为空时等同隐藏。 */
export function showTip(anchor: HTMLElement, text: string): void {
  if (!text) {
    hideTip();
    return;
  }
  const el = ensureTip();
  el.textContent = text;
  el.style.opacity = '1';
  // 先量尺寸再定位：内容不同高度不同
  const rect = anchor.getBoundingClientRect();
  const size = { width: el.offsetWidth, height: el.offsetHeight };
  const pos = clampTipPosition(
    {
      top: rect.top,
      left: rect.left,
      width: rect.width,
      height: rect.height,
      bottom: rect.bottom,
      right: rect.right,
    },
    size,
    { width: window.innerWidth, height: window.innerHeight },
  );
  el.style.top = `${pos.top}px`;
  el.style.left = `${pos.left}px`;
  el.dataset.flipped = String(pos.flipped);
  bindGlobalListeners();
}

export function hideTip(): void {
  if (tipEl) tipEl.style.opacity = '0';
}

/** 测试用：页面上现存的气泡节点数（应为 1）。 */
export function tipInstanceCount(): number {
  return document.querySelectorAll('#sop-help-tip').length;
}
