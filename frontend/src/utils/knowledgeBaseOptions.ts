import { listKnowledgeBases } from '@/api/knowledge-base'
import { useOrganizationStore } from '@/stores/organization'

export interface KnowledgeBaseOption {
  label: string
  value: string
}

/**
 * 列出可以写入内容的知识库：
 *  - 自有的文档型知识库；
 *  - 共享给自己且具备写权限（editor/admin）的文档型知识库。
 *
 * 手工编辑器与产物「加入知识库」共用同一份口径，避免两处可选范围不一致。
 */
export async function fetchWritableDocumentKnowledgeBases(): Promise<KnowledgeBaseOption[]> {
  const organizationStore = useOrganizationStore()
  const [ownRes, sharedKbs] = await Promise.all([
    listKnowledgeBases() as Promise<any>,
    organizationStore.fetchSharedKnowledgeBases().catch(() => []),
  ])

  const isDocumentKb = (type?: string) => !type || type === 'document'

  const ownKbs = Array.isArray(ownRes?.data) ? ownRes.data : []
  const list: KnowledgeBaseOption[] = ownKbs
    .filter((item: any) => isDocumentKb(item.type))
    .map((item: any) => ({ label: item.name, value: item.id }))

  // viewer-only 的共享库后端会拒绝写入，这里不展示。
  const seen = new Set(list.map((option) => option.value))
  for (const share of sharedKbs || []) {
    const kb = share?.knowledge_base
    const canWrite = share?.permission === 'editor' || share?.permission === 'admin'
    if (!kb || !canWrite || !isDocumentKb(kb.type) || seen.has(kb.id)) continue
    seen.add(kb.id)
    list.push({ label: kb.name, value: kb.id })
  }

  return list
}
