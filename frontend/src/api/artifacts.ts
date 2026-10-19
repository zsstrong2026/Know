import { del, get, post, postDown } from '@/utils/request'

/**
 * One file in the artifact library: the latest version of a file a skill
 * generated in one of the caller's sessions. Download it through
 * downloadArtifact(session_id, message_id, index) from '@/api/chat', which
 * re-checks session ownership server side.
 */
export interface ArtifactLibraryItem {
  session_id: string
  session_title: string
  message_id: string
  index: number
  /** `resource://<handle>` when the deployment runs a resource catalog. */
  handle?: string
  file_name: string
  file_type: string
  file_size: number
  source_path: string
  created_at: string
  /** How many times this file was (re)generated in its session. */
  version_count: number
}

export interface ArtifactLibraryParams {
  keyword?: string
  /** Extensions such as ".pdf"; empty means every type. */
  fileTypes?: string[]
  page?: number
  pageSize?: number
}

export interface ArtifactLibraryPage {
  success: boolean
  data: ArtifactLibraryItem[]
  total: number
  page: number
  page_size: number
}

export function listArtifactLibrary(params: ArtifactLibraryParams = {}) {
  const query: Record<string, string | number> = {}
  const keyword = params.keyword?.trim()
  if (keyword) query.keyword = keyword
  if (params.fileTypes?.length) query.file_types = params.fileTypes.join(',')
  if (params.page) query.page = params.page
  if (params.pageSize) query.page_size = params.pageSize
  return get<ArtifactLibraryPage>('/api/v1/artifacts', { params: query })
}

export interface ArtifactDeleteResult {
  success: boolean
  data: { file_name: string; deleted: number }
}

/**
 * Deletes a file from the artifact library, along with every earlier version of
 * it in the same session — a library row is the file, not one regeneration of
 * it, so leaving the older versions behind would leave the row in place.
 *
 * The stored bytes are reclaimed unless something else still references them,
 * so this is not reversible. Callers confirm with the user first.
 */
export function deleteArtifactLibraryItem(item: ArtifactLibraryItem) {
  const query = new URLSearchParams({
    session_id: item.session_id,
    message_id: item.message_id,
    index: String(item.index),
  })
  return del<ArtifactDeleteResult>(`/api/v1/artifacts?${query.toString()}`)
}

export interface BatchArtifactDeleteResult {
  success: boolean
  data: {
    requested: number
    /** 实际删除成功的条目，字段与请求定位三元组一致，便于调用方从列表中移除对应行。 */
    deleted: Array<Pick<ArtifactLibraryItem, 'session_id' | 'message_id' | 'index'> & { file_name: string }>
  }
}

/**
 * 批量删除产物库中的文件。后端逐条校验归属并删除，已删除或无权限的条目
 * 会被跳过；返回值只包含真正删掉的条目。与批量下载共用同一上限。
 * 不可恢复，调用方必须先向用户确认。
 */
export function batchDeleteArtifacts(items: ArtifactLibraryItem[]) {
  return post<BatchArtifactDeleteResult>('/api/v1/artifacts/batch-delete', {
    items: items.map((item) => ({
      session_id: item.session_id,
      message_id: item.message_id,
      index: item.index,
    })),
  })
}

/** 后端一次批量打包的上限，与 handler 侧常量保持一致。 */
export const BATCH_DOWNLOAD_LIMIT = 100

/**
 * 把选中的产物打包成一个 ZIP 返回。
 *
 * 请求体只带定位三元组（会话 / 消息 / 下标），后端会逐个复查会话归属：
 * 期间被删除或失去权限的文件会被跳过，其余仍然会打进包里。
 * 因此「下载成功」不代表每个文件都在，调用方无需逐个核对。
 */
export function batchDownloadArtifacts(items: ArtifactLibraryItem[]): Promise<Blob> {
  return postDown('/api/v1/artifacts/download', {
    items: items.map((item) => ({
      session_id: item.session_id,
      message_id: item.message_id,
      index: item.index,
    })),
  })
}
