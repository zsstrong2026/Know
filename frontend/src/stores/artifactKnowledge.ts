import { defineStore } from 'pinia'

/**
 * 会话产物 ↔ 知识库的关联状态。
 *
 * 后端没有产物与知识条目的关联字段，因此这里只在前端本地持久化（方案 2-A），
 * 以 `sessionId:messageId:index` 定位某一次生成的某个文件。
 */
export interface ArtifactKnowledgeLink {
  kbId: string
  kbName?: string
  /** 知识条目 ID：文本型为手工知识 ID，二进制型为上传后的文件知识 ID。 */
  knowledgeId: string
  status: 'draft' | 'publish'
  /** 文本型产物能回到编辑器继续编辑；二进制上传的文件只做状态标注。 */
  editable: boolean
  updatedAt: number
}

const STORAGE_KEY = 'weknora-chat-artifact-knowledge-links'

const linkKey = (sessionId: string, messageId: string, index: number) =>
  `${sessionId}:${messageId}:${index}`

function readLinks(): Record<string, ArtifactKnowledgeLink> {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return {}
    const parsed = JSON.parse(raw)
    return parsed && typeof parsed === 'object' ? parsed : {}
  } catch {
    // 隐私模式 / 数据损坏都不该让产物列表渲染失败。
    return {}
  }
}

export const useArtifactKnowledgeStore = defineStore('artifactKnowledge', {
  state: () => ({
    links: readLinks() as Record<string, ArtifactKnowledgeLink>,
  }),

  actions: {
    getLink(sessionId: string, messageId: string, index: number): ArtifactKnowledgeLink | null {
      if (!sessionId || !messageId) return null
      return this.links[linkKey(sessionId, messageId, index)] || null
    },

    setLink(
      sessionId: string,
      messageId: string,
      index: number,
      link: Omit<ArtifactKnowledgeLink, 'updatedAt'>,
    ) {
      if (!sessionId || !messageId) return
      // 整体替换对象，保证依赖 links 的组件能感知变化。
      this.links = {
        ...this.links,
        [linkKey(sessionId, messageId, index)]: { ...link, updatedAt: Date.now() },
      }
      this.persist()
    },

    removeLink(sessionId: string, messageId: string, index: number) {
      const key = linkKey(sessionId, messageId, index)
      if (!(key in this.links)) return
      const next = { ...this.links }
      delete next[key]
      this.links = next
      this.persist()
    },

    persist() {
      try {
        localStorage.setItem(STORAGE_KEY, JSON.stringify(this.links))
      } catch {
        // 写入失败只影响下次打开时的状态展示，不影响当前会话。
      }
    },
  },
})
