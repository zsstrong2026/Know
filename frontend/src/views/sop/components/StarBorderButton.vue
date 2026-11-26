<template>
  <!-- 星光边框按钮：Canvas 在边框外圈画巡游星点，纯装饰，不影响按钮语义 -->
  <button
    ref="rootRef"
    type="button"
    class="star-border"
    :class="{ 'is-active': active }"
    :disabled="disabled"
    :style="rootStyle"
  >
    <canvas ref="canvasRef" class="star-border__light" aria-hidden="true" />
    <slot />
  </button>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue';
import {
  buildOutline,
  clamp,
  mixRgb,
  parseColor,
  pointAt,
  rgbaOf,
  wrap,
  TAU,
  type BorderPoint,
  type BorderShape,
  type Rgb,
} from '../canvas/starBorder';

const props = withDefaults(
  defineProps<{
    /** 星轨颜色，需为具体色值（hex/rgb），留空取 --td-brand-color */
    color?: string;
    /** 拖尾颜色，留空跟随 color */
    trailColor?: string;
    /** 绕行一圈所需秒数 */
    duration?: number;
    /** 星点数量（1~6） */
    stars?: number;
    /** 拖尾长度占周长的比例（0~1） */
    trailLength?: number;
    /** 边框线宽（px） */
    thickness?: number;
    /** 圆角（px），应与 CSS 圆角一致 */
    radius?: number;
    /** 辉光强度（0~1） */
    glow?: number;
    direction?: 'clockwise' | 'counterclockwise';
    /** 选中态：常亮 + 品牌底色 */
    active?: boolean;
    disabled?: boolean;
  }>(),
  {
    color: '',
    trailColor: '',
    duration: 5,
    stars: 1,
    trailLength: 0.32,
    thickness: 1,
    radius: 8,
    glow: 0.6,
    direction: 'clockwise',
    active: false,
    disabled: false,
  },
);

const rootRef = ref<HTMLButtonElement | null>(null);
const canvasRef = ref<HTMLCanvasElement | null>(null);

const rootStyle = computed(() => ({
  '--sb-radius': `${props.radius}px`,
  '--sb-thickness': `${props.thickness}px`,
}));

/** Canvas 比按钮每边外扩这么多像素，给辉光留出绘制空间 */
const BLEED = 40;
const WHITE: Rgb = [255, 255, 255];

const point: BorderPoint = { x: 0, y: 0, nx: 0, ny: 1 };
const colors = { key: '', head: [0, 82, 217] as Rgb, tail: [0, 82, 217] as Rgb };
const state = {
  width: 0,
  height: 0,
  dpr: 1,
  dark: false,
  phase: 0,
  velocity: 0,
  motion: 0,
  reveal: 0,
  boost: 0,
  flash: 0,
  hovered: false,
  focused: false,
  visible: true,
};

let raf = 0;
let alive = true;
let last = 0;
let reduce = false;
let resizeObserver: ResizeObserver | null = null;
let visibilityObserver: IntersectionObserver | null = null;

/** 画布颜色取不到 CSS 变量，需先解析成具体色值 */
function refreshColors() {
  const root = rootRef.value;
  if (!root) return;
  const brand = getComputedStyle(root).getPropertyValue('--td-brand-color').trim() || '#0052d9';
  const head = props.color || brand;
  const tail = props.trailColor || head;
  const key = `${head}|${tail}`;
  if (key === colors.key) return;
  colors.key = key;
  colors.head = parseColor(head);
  colors.tail = parseColor(tail);
}

/** 依据容器背景亮度切换混合模式：暗底用 lighter 发光，亮底用 source-over 免得发灰 */
function isDarkSurface(): boolean {
  const bg = getComputedStyle(document.documentElement).getPropertyValue('--td-bg-color-container').trim();
  const [r, g, b] = parseColor(bg || '#ffffff');
  return (r * 299 + g * 587 + b * 114) / 1000 < 128;
}

function measure() {
  const root = rootRef.value;
  const canvas = canvasRef.value;
  if (!root || !canvas) return;
  state.width = root.offsetWidth;
  state.height = root.offsetHeight;
  state.dpr = Math.min(window.devicePixelRatio || 1, 2);
  canvas.width = Math.max(1, Math.round((state.width + BLEED * 2) * state.dpr));
  canvas.height = Math.max(1, Math.round((state.height + BLEED * 2) * state.dpr));
  refreshColors();
  state.dark = isDarkSurface();
  wake();
}

/** 拖尾：沿轮廓回溯采样，外圈画辉光、内圈画主线 */
function drawTrail(
  ctx: CanvasRenderingContext2D,
  shape: BorderShape,
  head: number,
  length: number,
  sign: number,
  intensity: number,
  tip = true,
) {
  if (length < 0.5 || intensity <= 0) return;
  const steps = clamp(Math.ceil(length / 2), 2, 90);
  pointAt(shape, head, point);
  let px = point.x;
  let py = point.y;
  for (let i = 1; i <= steps; i++) {
    pointAt(shape, head - sign * length * (i / steps), point);
    const u = 1 - (i - 0.5) / steps;
    const fade = u * u * intensity;
    let tone = mixRgb(colors.tail, colors.head, u);
    if (state.dark) tone = mixRgb(tone, WHITE, u ** 6 * 0.55);
    ctx.lineCap = tip && i === 1 ? 'round' : 'butt';

    ctx.lineWidth = 2 + 6 * props.glow;
    ctx.strokeStyle = rgbaOf(tone, fade * (state.dark ? 0.2 : 0.1) * props.glow);
    ctx.beginPath();
    ctx.moveTo(px, py);
    ctx.lineTo(point.x, point.y);
    ctx.stroke();

    ctx.lineWidth = props.thickness;
    ctx.strokeStyle = rgbaOf(tone, fade);
    ctx.beginPath();
    ctx.moveTo(px, py);
    ctx.lineTo(point.x, point.y);
    ctx.stroke();

    px = point.x;
    py = point.y;
  }
}

/** 星点本体：圆形辉光 + 高亮核心 */
function drawHead(ctx: CanvasRenderingContext2D, x: number, y: number, intensity: number) {
  if (intensity <= 0) return;
  const tone = colors.head;
  const reach = (8 + 26 * props.glow) * (state.dark ? 1 : 0.75);
  const strength = (state.dark ? 0.4 : 0.18) * props.glow * intensity;
  const bloom = ctx.createRadialGradient(x, y, 0, x, y, reach);
  bloom.addColorStop(0, rgbaOf(tone, strength));
  bloom.addColorStop(0.25, rgbaOf(tone, strength * 0.4));
  bloom.addColorStop(0.6, rgbaOf(tone, strength * 0.1));
  bloom.addColorStop(1, rgbaOf(tone, 0));
  ctx.fillStyle = bloom;
  ctx.fillRect(x - reach, y - reach, reach * 2, reach * 2);

  const size = 1.2 + props.thickness * 0.8;
  const core = ctx.createRadialGradient(x, y, 0, x, y, size * 2.4);
  core.addColorStop(0, rgbaOf(state.dark ? WHITE : tone, intensity));
  core.addColorStop(0.45, rgbaOf(tone, intensity * 0.55));
  core.addColorStop(1, rgbaOf(tone, 0));
  ctx.fillStyle = core;
  ctx.beginPath();
  ctx.arc(x, y, size * 2.4, 0, TAU);
  ctx.fill();
}

function render(shape: BorderShape) {
  const canvas = canvasRef.value;
  const ctx = canvas?.getContext('2d');
  if (!canvas || !ctx) return;

  ctx.setTransform(1, 0, 0, 1, 0, 0);
  ctx.clearRect(0, 0, canvas.width, canvas.height);
  if (state.width < 2 || state.height < 2) return;

  const offset = (BLEED + props.thickness / 2) * state.dpr;
  ctx.setTransform(state.dpr, 0, 0, state.dpr, offset, offset);
  ctx.globalCompositeOperation = state.dark ? 'lighter' : 'source-over';

  const total = shape.total;
  const stars = clamp(Math.round(props.stars), 1, 6);
  const spacing = total / stars;
  const speed = Math.abs(state.motion);
  const sign = state.motion >= 0 ? 1 : -1;
  // 速度越快拖尾越长，但不超过星点间距的一半，避免首尾相接糊成实心边框
  const length = Math.min(speed * props.trailLength, spacing * 0.9, total * 0.55);
  const intensity = state.reveal * (1 + 0.5 * state.boost + 0.5 * state.flash);

  // 点击/悬停时的整圈呼吸光
  if (state.flash > 0.01) {
    ctx.lineWidth = props.thickness + 2 * props.glow;
    ctx.strokeStyle = rgbaOf(colors.head, state.flash * (state.dark ? 0.35 : 0.18));
    ctx.beginPath();
    ctx.roundRect(
      0,
      0,
      Math.max(1, state.width - props.thickness),
      Math.max(1, state.height - props.thickness),
      shape.r,
    );
    ctx.stroke();
  }

  // 速度越低，静止星辉越明显：保证不悬停时也有微光
  const still = clamp(1 - speed / Math.max(1, (total / props.duration) * 0.6), 0, 1) * Math.min(1, intensity);
  const rest = (8 + 12 * props.glow + props.thickness * 2) * still;

  for (let i = 0; i < stars; i++) {
    const head = state.phase + i * spacing;
    drawTrail(ctx, shape, head, length, sign, Math.min(1.2, intensity));
    if (still > 0.01) {
      drawTrail(ctx, shape, head, rest, 1, still, false);
      drawTrail(ctx, shape, head, rest, -1, still, false);
    }
    pointAt(shape, head, point);
    drawHead(ctx, point.x, point.y, Math.min(1.3, intensity));
  }
}

function frame(now: number) {
  raf = 0;
  if (!alive) return;
  const dt = Math.min(0.05, Math.max(0.001, (now - last) / 1000));
  last = now;

  const engaged = state.hovered || state.focused;
  const boostTarget = engaged ? 1 : 0;
  state.boost += (boostTarget - state.boost) * (1 - Math.exp(-dt / 0.18));
  state.flash *= Math.exp(-dt / 0.26);
  const revealTarget = props.active || engaged ? 1 : 0.62;
  state.reveal += (revealTarget - state.reveal) * (1 - Math.exp(-dt / 0.22));

  const shape = buildOutline(
    Math.max(1, state.width - props.thickness),
    Math.max(1, state.height - props.thickness),
    Math.max(0, props.radius - props.thickness / 2),
  );
  const dir = props.direction === 'counterclockwise' ? -1 : 1;
  const cruise = reduce ? 0 : (dir * shape.total) / Math.max(0.8, props.duration);
  const targetSpeed = cruise * (engaged ? 2.2 : 1);
  state.velocity += (targetSpeed - state.velocity) * (1 - Math.exp(-dt / 0.3));
  state.motion = state.velocity;
  state.phase = wrap(state.phase + state.velocity * dt, shape.total);

  render(shape);

  if (state.visible) raf = requestAnimationFrame(frame);
}

function wake() {
  if (raf || !alive || !state.visible) return;
  last = performance.now();
  raf = requestAnimationFrame(frame);
}

function onEnter() {
  state.hovered = true;
  wake();
}

function onLeave() {
  state.hovered = false;
  wake();
}

function onDown() {
  state.flash = 1;
  wake();
}

function onFocus() {
  state.focused = rootRef.value?.matches(':focus-visible') ?? false;
  wake();
}

function onBlur() {
  state.focused = false;
  wake();
}

onMounted(() => {
  const root = rootRef.value;
  if (!root) return;
  reduce = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false;
  root.addEventListener('pointerenter', onEnter);
  root.addEventListener('pointerleave', onLeave);
  root.addEventListener('pointerdown', onDown);
  root.addEventListener('focus', onFocus);
  root.addEventListener('blur', onBlur);
  resizeObserver = new ResizeObserver(measure);
  resizeObserver.observe(root);
  visibilityObserver = new IntersectionObserver((entries) => {
    state.visible = entries.some((entry) => entry.isIntersecting);
    if (state.visible) {
      wake();
    } else {
      cancelAnimationFrame(raf);
      raf = 0;
    }
  });
  visibilityObserver.observe(root);
  measure();
});

onBeforeUnmount(() => {
  alive = false;
  cancelAnimationFrame(raf);
  raf = 0;
  resizeObserver?.disconnect();
  visibilityObserver?.disconnect();
  const root = rootRef.value;
  root?.removeEventListener('pointerenter', onEnter);
  root?.removeEventListener('pointerleave', onLeave);
  root?.removeEventListener('pointerdown', onDown);
  root?.removeEventListener('focus', onFocus);
  root?.removeEventListener('blur', onBlur);
});

watch(
  () => [props.color, props.trailColor],
  () => {
    refreshColors();
    wake();
  },
);
watch(() => props.active, wake);
</script>

<style lang="less" scoped src="./starBorderButton.less"></style>
