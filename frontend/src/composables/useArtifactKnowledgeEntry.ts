import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { downloadArtifact } from '@/api/chat'
import { uploadKnowledgeFile } from '@/api/knowledge-base'
import { resolveFilePreviewExt, resolvePreviewKind } from '@/utils/filePreview'
import { useUIStore } from '@/stores/ui'
import { useArtifactKnowledgeStore, type ArtifactKnowledgeLink } from '@/stores/artifactKnowledge'

/**
 * 产物「加入知识库」的统一入口（方案 1-A + 2-A）。
 *
 * - 文本型产物（text / markdown / html / mermaid）：下载内容后打开手工知识编辑器，
 *   在线编辑并保存草稿 / 发布；
 * - 二进制型产物（PDF / Word / Excel / 图片 / 音视频等）：选定知识库后直接上传原文件，
 *   不做在线编辑；
 * - 关联状态仅落前端本地（artifactKnowledge store），行内标注入口状态。
 *
 * 侧栏面板（ChatArtifactsPanel）与抽屉（ChatArtifactsDrawer）共用本函数，避免两处重复实现。
 */

/** 能回到编辑器继续编辑的类型；其余按二进制处理。 */
const EDITABLE_KINDS = new Set(['text', 'markdown', 'html', 'mermaid'])

export interface ArtifactKnowledgeTarget {
  sessionId: string
  messageId: string
  index: number
  fileName: string
  fileType?: string
}

/** 去掉扩展名，作为手工知识条目的默认标题。 */
function stripExtension(fileName: string): string {
  const dot = fileName.lastIndexOf('.')
  return dot > 0 ? fileName.slice(0, dot) : fileName
}

export function useArtifactKnowledgeEntry() {
  const { t } = useI18n()
  const uiStore = useUIStore()
  const linkStore = useArtifactKnowledgeStore()

  const dialogVisible = ref(false)
  const pendingTarget = ref<ArtifactKnowledgeTarget | null>(null)
  /** 正在处理的产物键，用于按钮 loading 与重复点击拦截。 */
  const busyKey = ref<string | null>(null)

  const pendingFileName = computed(() => pendingTarget.value?.fileName || '')
  /** 弹窗上传中：锁住弹窗交互。 */
  const dialogUploading = computed(() => !!busyKey.value)

  const keyOf = (target: ArtifactKnowledgeTarget) =>
    `${target.sessionId}:${target.messageId}:${target.index}`

  function getLink(target: ArtifactKnowledgeTarget): ArtifactKnowledgeLink | null {
    return linkStore.getLink(target.sessionId, target.messageId, target.index)
  }

  function isBusy(target: ArtifactKnowledgeTarget): boolean {
    return !!busyKey.value && busyKey.value === keyOf(target)
  }

  function isEditable(fileName?: string, fileType?: string): boolean {
    return EDITABLE_KINDS.has(resolvePreviewKind(resolveFilePreviewExt(fileName, fileType)))
  }

  /** 保存成功后的回写：文本型标记为可继续编辑，二进制型只做状态标注。 */
  function persist(
    target: ArtifactKnowledgeTarget,
    payload: { kbId: string; kbName?: string; knowledgeId: string; status: 'draft' | 'publish' },
    editable: boolean,
  ) {
    linkStore.setLink(target.sessionId, target.messageId, target.index, {
      kbId: payload.kbId,
      kbName: payload.kbName,
      knowledgeId: payload.knowledgeId,
      status: payload.status,
      editable,
    })
  }

  /** 文本型：下载内容 → 打开手工编辑器在线编辑 → 保存后回写状态。 */
  async function openInEditor(target: ArtifactKnowledgeTarget) {
    busyKey.value = keyOf(target)
    try {
      const blob = await downloadArtifact(target.sessionId, target.messageId, target.index)
      const content = await blob.text()
      uiStore.openManualEditor({
        mode: 'create',
        title: stripExtension(target.fileName),
        content,
        status: 'draft',
        onSuccess: ({ kbId, kbName, knowledgeId, status }) => {
          persist(target, { kbId, kbName, knowledgeId, status }, true)
        },
      })
    } catch (err) {
      console.error('[artifactKnowledge] open editor failed:', err)
      MessagePlugin.error(t('agent.artifactDrawer.addToKbFailed'))
    } finally {
      busyKey.value = null
    }
  }

  /** 已加入且可编辑：回到编辑器继续编辑（编辑器自行拉取已保存内容）。 */
  function reopenEditor(target: ArtifactKnowledgeTarget, link: ArtifactKnowledgeLink) {
    uiStore.openManualEditor({
      mode: 'edit',
      kbId: link.kbId,
      knowledgeId: link.knowledgeId,
      status: link.status,
      onSuccess: ({ kbId, kbName, knowledgeId, status }) => {
        persist(target, { kbId, kbName: kbName || link.kbName, knowledgeId, status }, true)
      },
    })
  }

  /** 行内入口点击：分流到编辑器或选库弹窗。 */
  async function start(target: ArtifactKnowledgeTarget) {
    if (!target.sessionId || !target.messageId || busyKey.value) return
    const link = getLink(target)
    if (link) {
      // 已加入的文本产物可继续编辑；二进制产物保持状态展示，不重复入库。
      if (link.editable) reopenEditor(target, link)
      return
    }
    if (isEditable(target.fileName, target.fileType)) {
      await openInEditor(target)
      return
    }
    pendingTarget.value = target
    dialogVisible.value = true
  }

  /** 二进制型：选定知识库后直接上传原文件。 */
  async function confirmUpload(payload: { kbId: string; kbName?: string }) {
    const target = pendingTarget.value
    if (!target || busyKey.value) return
    busyKey.value = keyOf(target)
    try {
      const blob = await downloadArtifact(target.sessionId, target.messageId, target.index)
      const file = new File([blob], target.fileName || 'artifact', {
        type: blob.type || 'application/octet-stream',
      })
      const res: any = await uploadKnowledgeFile(payload.kbId, { file, fileName: target.fileName })
      const knowledgeId: string | undefined = res?.data?.id
      if (!res?.success || !knowledgeId) {
        throw new Error(res?.message || 'upload failed')
      }
      persist(
        target,
        { kbId: payload.kbId, kbName: payload.kbName, knowledgeId, status: 'publish' },
        false,
      )
      MessagePlugin.success(t('agent.artifactDrawer.addToKbUploaded'))
      closeDialog()
    } catch (err) {
      console.error('[artifactKnowledge] upload failed:', err)
      MessagePlugin.error(t('agent.artifactDrawer.addToKbFailed'))
    } finally {
      busyKey.value = null
    }
  }

  function closeDialog() {
    if (busyKey.value) return
    dialogVisible.value = false
    pendingTarget.value = null
  }

  /** 产物被删除时一并清理其入库状态标注。 */
  function removeEntry(target: ArtifactKnowledgeTarget) {
    linkStore.removeLink(target.sessionId, target.messageId, target.index)
  }

  return {
    dialogVisible,
    pendingFileName,
    dialogUploading,
    getLink,
    isBusy,
    isEditable,
    start,
    reopenEditor,
    confirmUpload,
    closeDialog,
    removeEntry,
  }
}
