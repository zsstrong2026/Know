/**
 * 位图尺寸与类型识别（导出 Word 内嵌图片用）。
 *
 * Word 的 drawing 必须写出具体宽高（EMU），所以要在不依赖 DOM 的前提下
 * 从字节里读出像素尺寸：PNG / JPEG / GIF / WebP / BMP 各读各的头部。
 * 读不出来就返回 null，由调用方按默认宽度兜底，绝不让导出失败。
 */

export interface ImageSize {
  width: number
  height: number
}

/** 与 Word 的 `wp:extent` 同单位：1 英寸 = 914400 EMU。 */
export const EMU_PER_INCH = 914400
/** Word 默认按 96 DPI 解释像素，和屏幕看到的大小一致。 */
export const EMU_PER_PIXEL = 9525

/** Word 能内嵌的位图类型 → 包内扩展名。SVG 不在其中（drawing 需要额外回退图）。 */
const MIME_EXTENSIONS: Record<string, string> = {
  'image/png': 'png',
  'image/jpeg': 'jpeg',
  'image/jpg': 'jpeg',
  'image/gif': 'gif',
  'image/webp': 'webp',
  'image/bmp': 'bmp',
}

/** 扩展名 → MIME，写 `[Content_Types].xml` 的 Default 用。 */
const EXTENSION_MIMES: Record<string, string> = {
  png: 'image/png',
  jpeg: 'image/jpeg',
  gif: 'image/gif',
  webp: 'image/webp',
  bmp: 'image/bmp',
}

/** 只保留可内嵌的位图 MIME；带参数（如 `image/png;charset`) 时先截断。 */
export function normalizeImageMime(mime: string | undefined): string | null {
  const base = String(mime ?? '').split(';')[0]?.trim().toLowerCase() ?? ''
  return MIME_EXTENSIONS[base] ? base : null
}

export function extensionOfMime(mime: string): string {
  return MIME_EXTENSIONS[mime] ?? ''
}

export function mimeOfExtension(extension: string): string {
  return EXTENSION_MIMES[extension] ?? 'application/octet-stream'
}

function ascii(bytes: Uint8Array, start: number, length: number): string {
  return String.fromCharCode(...bytes.subarray(start, start + length))
}

function readPng(bytes: Uint8Array): ImageSize | null {
  // 8 字节签名 + 4 字节长度 + 4 字节 "IHDR"，随后是高/宽（各 4 字节大端）
  if (bytes.length < 24 || ascii(bytes, 12, 4) !== 'IHDR') return null
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength)
  const width = view.getUint32(16)
  const height = view.getUint32(20)
  return width > 0 && height > 0 ? { width, height } : null
}

function readGif(bytes: Uint8Array): ImageSize | null {
  if (bytes.length < 10) return null
  const width = bytes[6] | (bytes[7] << 8)
  const height = bytes[8] | (bytes[9] << 8)
  return width > 0 && height > 0 ? { width, height } : null
}

function readBmp(bytes: Uint8Array): ImageSize | null {
  if (bytes.length < 26) return null
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength)
  const width = Math.abs(view.getInt32(18, true))
  const height = Math.abs(view.getInt32(22, true))
  return width > 0 && height > 0 ? { width, height } : null
}

function readJpeg(bytes: Uint8Array): ImageSize | null {
  let offset = 2
  while (offset + 9 < bytes.length) {
    if (bytes[offset] !== 0xff) {
      offset += 1
      continue
    }
    const marker = bytes[offset + 1] ?? 0
    // 独立的标记没有长度字段，直接跳过
    if (marker === 0x01 || (marker >= 0xd0 && marker <= 0xd8)) {
      offset += 2
      continue
    }
    const length = ((bytes[offset + 2] ?? 0) << 8) | (bytes[offset + 3] ?? 0)
    const isSof = marker >= 0xc0 && marker <= 0xcf && marker !== 0xc4 && marker !== 0xc8 && marker !== 0xcc
    if (isSof) {
      const height = ((bytes[offset + 5] ?? 0) << 8) | (bytes[offset + 6] ?? 0)
      const width = ((bytes[offset + 7] ?? 0) << 8) | (bytes[offset + 8] ?? 0)
      return width > 0 && height > 0 ? { width, height } : null
    }
    if (length <= 0) return null
    offset += 2 + length
  }
  return null
}

function readWebp(bytes: Uint8Array): ImageSize | null {
  if (bytes.length < 30) return null
  const kind = ascii(bytes, 12, 4)
  // 扩展格式：24 位画布宽高（存的是 width-1）
  if (kind === 'VP8X') {
    const width = 1 + (bytes[24] | (bytes[25] << 8) | (bytes[26] << 16))
    const height = 1 + (bytes[27] | (bytes[28] << 8) | (bytes[29] << 16))
    return { width, height }
  }
  // 无损：14 位宽高打包在 4 字节里（同样存的是 width-1）
  if (kind === 'VP8L') {
    const bits = bytes[21] | (bytes[22] << 8) | (bytes[23] << 16) | (bytes[24] << 24)
    return { width: 1 + (bits & 0x3fff), height: 1 + ((bits >> 14) & 0x3fff) }
  }
  // 有损：跳过 3 字节帧标签与 3 字节同步码，再读 14 位宽高
  if (kind === 'VP8 ') {
    const width = (bytes[26] | (bytes[27] << 8)) & 0x3fff
    const height = (bytes[28] | (bytes[29] << 8)) & 0x3fff
    return width > 0 && height > 0 ? { width, height } : null
  }
  return null
}

/** 从字节里读像素尺寸；不认识的格式返回 null。 */
export function readImageSize(bytes: Uint8Array): ImageSize | null {
  if (!bytes || bytes.length < 10) return null
  if (bytes[0] === 0x89 && ascii(bytes, 1, 3) === 'PNG') return readPng(bytes)
  if (ascii(bytes, 0, 3) === 'GIF') return readGif(bytes)
  if (bytes[0] === 0xff && bytes[1] === 0xd8) return readJpeg(bytes)
  if (ascii(bytes, 0, 2) === 'BM') return readBmp(bytes)
  if (ascii(bytes, 0, 4) === 'RIFF' && ascii(bytes, 8, 4) === 'WEBP') return readWebp(bytes)
  return null
}

/**
 * 按魔数识别位图 MIME。
 *
 * 用来兜住 `Blob.type` 为空的情况：粘贴或旧的资产记录常常只有字节没有类型。
 */
export function sniffImageMime(bytes: Uint8Array): string | null {
  if (!bytes || bytes.length < 12) return null
  if (bytes[0] === 0x89 && ascii(bytes, 1, 3) === 'PNG') return 'image/png'
  if (ascii(bytes, 0, 3) === 'GIF') return 'image/gif'
  if (bytes[0] === 0xff && bytes[1] === 0xd8) return 'image/jpeg'
  if (ascii(bytes, 0, 2) === 'BM') return 'image/bmp'
  if (ascii(bytes, 0, 4) === 'RIFF' && ascii(bytes, 8, 4) === 'WEBP') return 'image/webp'
  return null
}

export interface EmuSize {
  widthEmu: number
  heightEmu: number
}

/**
 * 像素尺寸 → Word 的 EMU。
 *
 * 超过正文可用宽度时等比缩小：A4 去掉左右各 1 英寸页边距后约 6.27 英寸，
 * 不缩的宽图会被 Word 裁到页外。
 */
export function toEmuSize(size: ImageSize, maxWidthEmu = 5_731_510): EmuSize {
  const widthEmu = Math.max(1, Math.round(size.width * EMU_PER_PIXEL))
  const heightEmu = Math.max(1, Math.round(size.height * EMU_PER_PIXEL))
  if (widthEmu <= maxWidthEmu) return { widthEmu, heightEmu }
  const ratio = maxWidthEmu / widthEmu
  return {
    widthEmu: Math.round(maxWidthEmu),
    heightEmu: Math.max(1, Math.round(heightEmu * ratio)),
  }
}
