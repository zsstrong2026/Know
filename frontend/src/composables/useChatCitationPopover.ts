import type { Ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { getChunkByIdOnly } from '@/api/knowledge-base'
import { getEmbedChunkById } from '@/api/embed'
import type { CitationKnowledgeRef } from '@/utils/citationMarkdown'
import type { KnowledgeReferenceLike, ReferenceHighlightTarget } from '@/utils/referenceSources'
import { useCitationPopover } from './useCitationPopover'

export { clearCitationChunkCache } from '@/utils/citationChunkCache'
export type { CitationFloatState } from './useCitationPopover'

export type ChatCitationPopoverOptions = {
  getKnowledgeReferences?: () => CitationKnowledgeRef[] | null | undefined
  /** 消息本身不带引用列表时（如会议室），点击引用后按需加载引用数据 */
  loadReferences?: (target: ReferenceHighlightTarget) => Promise<KnowledgeReferenceLike[] | null | undefined>
  embedChannelId?: () => string | undefined
  embedToken?: () => string | undefined
  sessionId?: () => string | undefined
}

export function useChatCitationPopover(rootRef: Ref<HTMLElement | null>, options?: ChatCitationPopoverOptions) {
  const { t } = useI18n()
  return useCitationPopover(rootRef, {
    mode: 'chat',
    getKnowledgeReferences: options?.getKnowledgeReferences,
    loadReferences: options?.loadReferences,
    getCacheScope: () => {
      const channel = options?.embedChannelId?.()
      const token = options?.embedToken?.()
      return channel && token ? `embed:${channel}:${token}` : options?.sessionId?.() || 'default'
    },
    fetchChunk: (chunkId) => {
      const channel = options?.embedChannelId?.()
      const token = options?.embedToken?.()
      return channel && token ? getEmbedChunkById(channel, token, chunkId) : getChunkByIdOnly(chunkId)
    },
    notFoundError: () => t('agentStream.citation.notFound'),
    loadError: () => t('agentStream.citation.loadFailed'),
  })
}
