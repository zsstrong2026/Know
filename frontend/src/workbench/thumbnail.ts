/**
 * 列表卡片封面：把编辑器当前视口截成小图。
 *
 * 只在文档保存/就绪后由编辑页调用，生成 480×300 的 JPEG（约几十 KB），
 * 上传到条目的缩略图接口；列表卡片再按需下载展示。
 * 截图失败不阻断编辑：返回 null，卡片继续用 CSS 骨架封面兜底。
 */
import type { EditorMode } from './editor/blocksuite'

/** 与卡片封面接近的 16:10，横向 480px 足够 2x 屏清晰，又不会让体积失控。 */
const THUMB_WIDTH = 480
const THUMB_HEIGHT = 300

/** 读取当前主题的容器底色，避免截图透明区域在卡片上发黑。 */
function themeBackground(): string {
  const raw = getComputedStyle(document.documentElement)
    .getPropertyValue('--td-bg-color-container')
    .trim()
  if (raw) return raw
  return document.documentElement.getAttribute('theme-mode') === 'dark' ? '#1c1c1e' : '#ffffff'
}

/**
 * 把原图按「铺满 + 裁切」缩放到封面尺寸。
 *
 * 文档从顶部截取（阅读习惯），画布从中心截取（内容通常在视口中央）。
 * 导出为 JPEG 而不是 PNG：封面有底色，不需要透明通道，体积可小一个量级。
 */
function renderThumbnail(
  source: HTMLCanvasElement,
  mode: EditorMode,
  background: string,
): Promise<Blob | null> {
  const canvas = document.createElement('canvas')
  canvas.width = THUMB_WIDTH
  canvas.height = THUMB_HEIGHT
  const ctx = canvas.getContext('2d')
  if (!ctx) return Promise.resolve(null)

  ctx.fillStyle = background
  ctx.fillRect(0, 0, THUMB_WIDTH, THUMB_HEIGHT)
  if (source.width === 0 || source.height === 0) return Promise.resolve(null)

  const scale = Math.max(THUMB_WIDTH / source.width, THUMB_HEIGHT / source.height)
  const width = source.width * scale
  const height = source.height * scale
  const x = (THUMB_WIDTH - width) / 2
  const y = mode === 'page' ? 0 : (THUMB_HEIGHT - height) / 2
  ctx.drawImage(source, x, y, width, height)

  return new Promise((resolve) => {
    try {
      canvas.toBlob((blob) => resolve(blob), 'image/jpeg', 0.82)
    } catch {
      // 跨域图片可能污染画布；封面只是增强，失败直接回退骨架图。
      resolve(null)
    }
  })
}

/**
 * 截取编辑器视口。target 优先用 BlockSuite 的 viewport 元素，
 * 避免把外部工具条、AI 浮层一起截进去。
 */
export async function captureWorkbenchThumbnail(
  host: HTMLElement | null,
  mode: EditorMode,
): Promise<Blob | null> {
  if (!host) return null
  const target =
    (host.querySelector('.affine-page-viewport, .affine-edgeless-viewport') as HTMLElement | null) ??
    host
  if (target.clientWidth < 80 || target.clientHeight < 60) return null

  try {
    const { default: html2canvas } = await import('html2canvas')
    if (typeof html2canvas !== 'function') return null
    const background = themeBackground()
    const source = await html2canvas(target, {
      backgroundColor: background,
      // 画布很宽时限制源图宽度，避免在大屏 4K 下生成巨大位图。
      scale: Math.min(1, 960 / Math.max(target.clientWidth, 1)),
      logging: false,
      useCORS: true,
      allowTaint: false,
      // 编辑器工具条/浮动格式栏不属于正文，截进封面会像一块 UI 补丁。
      ignoreElements: (element) =>
        element.tagName.toLowerCase().includes('toolbar') ||
        element.classList.contains('affine-format-bar-widget'),
    })
    return await renderThumbnail(source, mode, background)
  } catch {
    // 个别浏览器或复杂画布可能不支持 DOM 截图；静默失败，不打扰编辑。
    return null
  }
}
