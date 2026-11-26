/**
 * 星光边框几何工具：把圆角矩形轮廓拆成「直线 + 圆弧」共 8 段，
 * 光点按「沿周长行驶的距离」取点，即可实现绕按钮边框巡游的星轨。
 */

export type Rgb = [number, number, number];

export interface BorderSegment {
  length: number;
  x: number;
  y: number;
  dx: number;
  dy: number;
  angle: number | null;
}

export interface BorderShape {
  r: number;
  segments: BorderSegment[];
  total: number;
}

export interface BorderPoint {
  x: number;
  y: number;
  nx: number;
  ny: number;
}

export const TAU = Math.PI * 2;
const FALLBACK: Rgb = [255, 255, 255];

export const clamp = (value: number, min: number, max: number) => Math.min(max, Math.max(min, value));

export const wrap = (value: number, total: number) => ((value % total) + total) % total;

export const mixRgb = (a: Rgb, b: Rgb, t: number): Rgb => [
  a[0] + (b[0] - a[0]) * t,
  a[1] + (b[1] - a[1]) * t,
  a[2] + (b[2] - a[2]) * t,
];

export const rgbaOf = (rgb: Rgb, alpha: number) =>
  `rgba(${Math.round(rgb[0])}, ${Math.round(rgb[1])}, ${Math.round(rgb[2])}, ${clamp(alpha, 0, 1).toFixed(3)})`;

let colorContext: CanvasRenderingContext2D | null = null;

/** 借 canvas 把任意 CSS 颜色（hex / rgb / 颜色名）解析成 RGB 分量。 */
export function parseColor(color: string): Rgb {
  colorContext = colorContext ?? document.createElement('canvas').getContext('2d');
  if (!colorContext) return FALLBACK;
  // 先写黑色兜底，非法颜色不会残留上一次的结果
  colorContext.fillStyle = '#000000';
  colorContext.fillStyle = color;
  const value = String(colorContext.fillStyle);
  if (value.startsWith('#')) {
    const hex = parseInt(value.slice(1, 7), 16);
    return [(hex >> 16) & 255, (hex >> 8) & 255, hex & 255];
  }
  const parts = value.match(/[\d.]+/g)?.map(Number) ?? FALLBACK;
  return [parts[0], parts[1], parts[2]];
}

/** 上直 → 右上弧 → 右直 → 右下弧 → 下直 → 左下弧 → 左直 → 左上弧。 */
export function buildOutline(width: number, height: number, radius: number): BorderShape {
  const r = Math.max(0, Math.min(radius, width / 2, height / 2));
  const arc = (Math.PI * r) / 2;
  const across = Math.max(0, width - 2 * r);
  const down = Math.max(0, height - 2 * r);
  const segments: BorderSegment[] = [
    { length: across, x: r, y: 0, dx: 1, dy: 0, angle: null },
    { length: arc, x: width - r, y: r, dx: 0, dy: 0, angle: -Math.PI / 2 },
    { length: down, x: width, y: r, dx: 0, dy: 1, angle: null },
    { length: arc, x: width - r, y: height - r, dx: 0, dy: 0, angle: 0 },
    { length: across, x: width - r, y: height, dx: -1, dy: 0, angle: null },
    { length: arc, x: r, y: height - r, dx: 0, dy: 0, angle: Math.PI / 2 },
    { length: down, x: 0, y: height - r, dx: 0, dy: -1, angle: null },
    { length: arc, x: r, y: r, dx: 0, dy: 0, angle: Math.PI },
  ];
  return { r, segments, total: Math.max(1, segments.reduce((sum, segment) => sum + segment.length, 0)) };
}

/** 按沿周长的距离取坐标与外法线（用于光点定位）。 */
export function pointAt(shape: BorderShape, distance: number, point: BorderPoint): void {
  let s = wrap(distance, shape.total);
  for (const segment of shape.segments) {
    if (segment.length > 0 && s <= segment.length) {
      if (segment.angle === null) {
        point.x = segment.x + segment.dx * s;
        point.y = segment.y + segment.dy * s;
        point.nx = segment.dy;
        point.ny = -segment.dx;
      } else {
        const angle = segment.angle + s / shape.r;
        point.nx = Math.cos(angle);
        point.ny = Math.sin(angle);
        point.x = segment.x + point.nx * shape.r;
        point.y = segment.y + point.ny * shape.r;
      }
      return;
    }
    s -= segment.length;
  }
}
