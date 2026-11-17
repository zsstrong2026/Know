<template>
  <!--
    模板正文编辑器：模板管理页的全屏浮层，不是新路由。
    两种入口：
      - 编辑已有模板：先取快照，正文回写 PUT /templates/:id/content；
      - 新建模板（mode=create）：空文档起步，保存时连同名称/描述一起 POST /templates。
  -->
  <div class="tpl-editor-mask">
    <section class="tpl-editor" role="dialog" aria-modal="true" :aria-label="t('workbench.templates.content.title')">
      <header class="tpl-editor-head">
        <t-button
          variant="text"
          shape="square"
          :aria-label="t('workbench.templates.content.cancel')"
          @click="requestClose"
        >
          <t-icon name="chevron-left" size="18px" />
        </t-button>
        <span class="tpl-editor-title" :title="templateName">{{ templateName }}</span>
        <span v-if="saveLabel" class="save-state" :class="{ 'is-dirty': dirty }">{{ saveLabel }}</span>
        <span class="spacer" />
        <span class="tpl-editor-hint">{{ t('workbench.templates.content.savedHint') }}</span>
        <t-button size="small" theme="primary" :loading="saving" :disabled="!canSave" @click="save">
          {{ t('workbench.templates.content.save') }}
        </t-button>
      </header>

      <div class="tpl-editor-body">
        <div v-if="loadError" class="tpl-editor-error">
          <t-icon name="error-circle" size="20px" />
          <span>{{ t('workbench.templates.content.loadFailed') }}</span>
          <t-button size="small" variant="outline" @click="close">
            {{ t('workbench.templates.content.cancel') }}
          </t-button>
        </div>
        <div v-else class="tpl-editor-host" :class="{ 'is-ready': ready }">
          <div ref="hostRef" class="tpl-editor-mount" />
          <div v-if="!ready" class="tpl-editor-loading">
            {{ t('workbench.templates.content.loading') }}
          </div>
        </div>
      </div>
    </section>
  </div>
</template>

<script setup lang="ts">
/**
 * 模板正文编辑器。
 *
 * 关键约束：
 *   - 只用 mountEditor 挂一份 Y.Doc，不建保存队列：模板没有乐观锁与版本历史，
 *     保存就是整体覆盖；
 *   - 新建模式没有已有快照，直接给一份空白页，首次保存时才 POST 创建模板；
 *   - 模板不复制图片/附件（§9.16.3），因此不注入资产解析器，缺图按占位渲染；
 *   - 关闭前有未保存修改时二次确认，绝不静默丢弃。
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import {
  createWorkbenchTemplate,
  getWorkbenchTemplateContent,
  updateWorkbenchTemplateContent,
} from '@/api/workbench'
import {
  encodeTemplateBase64,
  prepareTemplateSnapshot,
  TemplateApplyError,
} from '@/workbench/templates/apply'
import { useWorkbenchPrefs } from '@/workbench/use-workbench-prefs'
import { noteStyleSeedFromPrefs } from '@/workbench/editor/prefs'
import type { CanvasHandle } from '@/workbench/editor/blocksuite'
import type { WorkbenchTemplateSummary } from '@/types/workbench'

const props = withDefaults(
  defineProps<{
    /** 编辑模式必填；新建模式下为空 */
    templateId?: string
    templateName: string
    /** create：从空白页开始，首次保存才落库 */
    mode?: 'edit' | 'create'
    /** 仅新建模式使用：随模板一起提交的描述 */
    templateDescription?: string
  }>(),
  { templateId: '', mode: 'edit', templateDescription: '' },
)

const emit = defineEmits<{
  close: []
  saved: [summary: WorkbenchTemplateSummary]
}>()

const { t } = useI18n()
const { editorPrefs } = useWorkbenchPrefs()

const creating = computed(() => props.mode === 'create')
const hostRef = ref<HTMLElement | null>(null)
const ready = ref(false)
const loadError = ref(false)
const dirty = ref(false)
const saving = ref(false)
const schemaVersion = ref(1)

// 编辑器实例不参与响应式：内部有大量不可序列化字段，深度代理只会拖慢渲染。
let editor: CanvasHandle | null = null
let closing = false
/**
 * 是否已开始记录改动。
 *
 * 挂载后 BlockSuite 仍会在首帧就地改写一些视图属性并触发 onChange；
 * 那不是用户编辑，等一帧再开始记录，避免一打开就显示「有未保存的修改」。
 */
let armed = false

const saveLabel = computed(() => {
  if (saving.value) return t('workbench.templates.content.saving')
  if (dirty.value) return t('workbench.templates.content.unsaved')
  // 新建模式初始就是空白页，没有「已保存」可言，避免误导
  return creating.value ? '' : t('workbench.templates.content.savedState')
})

/** 新建模式允许直接保存空白模板；编辑模式必须有改动才可保存。 */
const canSave = computed(() => creating.value || dirty.value)

/** 首次挂载：编辑模式先取快照，新建模式直接建空白页。 */
async function mountEditorWithContent() {
  if (!hostRef.value) return
  try {
    let snapshotBytes: Uint8Array | null = null
    if (!creating.value) {
      const content = await getWorkbenchTemplateContent(props.templateId)
      const snapshot = prepareTemplateSnapshot({
        name: content.name,
        ydoc: content.ydoc,
        editor_schema_version: content.editor_schema_version,
      })
      schemaVersion.value = snapshot.schemaVersion
      snapshotBytes = snapshot.bytes
    }
    const { mountEditor } = await import('@/workbench/editor/blocksuite')
    editor = await mountEditor({
      element: hostRef.value,
      // 独立 workspace id：避免与同标签页里已打开的文档共用 store id
      itemId: creating.value ? `template-new-${Date.now()}` : `template-${props.templateId}`,
      kind: 'canvas',
      // 模板只做 Page（§9.16.3），与「另存为模板」的入口保持一致
      mode: 'page',
      snapshot: snapshotBytes,
      settings: {
        edgelessScrollZoom: editorPrefs.value.scrollZoom,
        codeBlockLineNumbers: editorPrefs.value.codeLineNumbers,
      },
      noteStyle: noteStyleSeedFromPrefs(editorPrefs.value),
      onChange: () => {
        if (!armed) return
        dirty.value = true
      },
    })
    ready.value = true
    // 首帧的就地改写不算用户编辑；下一帧之后才开始记录 dirty。
    requestAnimationFrame(() => {
      armed = true
    })
  } catch (error) {
    loadError.value = true
    MessagePlugin.error(
      error instanceof TemplateApplyError
        ? t('workbench.templates.content.loadInvalid')
        : t('workbench.templates.content.loadFailed'),
    )
  }
}

async function save() {
  if (saving.value || !editor) return
  saving.value = true
  try {
    const ydoc = encodeTemplateBase64(editor.getSnapshot())
    const summary = creating.value
      ? await createWorkbenchTemplate({
          name: props.templateName,
          description: props.templateDescription,
          ydoc,
          editor_schema_version: schemaVersion.value,
        })
      : await updateWorkbenchTemplateContent(props.templateId, {
          ydoc,
          editor_schema_version: schemaVersion.value,
        })
    dirty.value = false
    MessagePlugin.success(
      creating.value
        ? t('workbench.templates.manage.created')
        : t('workbench.templates.content.saved'),
    )
    emit('saved', summary)
    close()
  } catch {
    MessagePlugin.error(t('workbench.templates.content.saveFailed'))
  } finally {
    saving.value = false
  }
}

/** 有未保存修改时先确认，避免返回/关闭丢掉编辑。 */
function requestClose() {
  if (saving.value || closing) return
  // 新建模式没改过内容时没有可丢弃的东西，直接退出
  if (!dirty.value) {
    close()
    return
  }
  const dialog = DialogPlugin.confirm({
    header: t('workbench.templates.content.unsavedTitle'),
    body: t('workbench.templates.content.unsavedBody'),
    theme: 'warning',
    confirmBtn: t('workbench.templates.content.discard'),
    cancelBtn: t('workbench.templates.content.keepEditing'),
    onConfirm: () => {
      dialog.hide()
      close()
    },
  })
}

function close() {
  if (closing) return
  closing = true
  emit('close')
}

/** 销毁必须放在卸载阶段：模板浮层关闭后仍留着编辑器会泄漏 Y.Doc 与 DOM。 */
function destroyEditor() {
  editor?.destroy()
  editor = null
}

onMounted(() => {
  void mountEditorWithContent()
})

onBeforeUnmount(destroyEditor)
</script>

<style lang="less" scoped>
.tpl-editor-mask {
  position: fixed;
  inset: 0;
  z-index: 1000;
  display: flex;
  background: var(--td-bg-color-page);
}

.tpl-editor {
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
  background: var(--td-bg-color-container);
}

.tpl-editor-head {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  padding: var(--app-space-2) var(--app-space-4);
  border-bottom: 1px solid var(--td-component-stroke);
}

.tpl-editor-title {
  max-width: 320px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--app-text-lg);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.save-state {
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);

  &.is-dirty {
    color: var(--td-warning-color);
  }
}

.spacer {
  flex: 1;
}

.tpl-editor-hint {
  max-width: 360px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}

.tpl-editor-body {
  position: relative;
  flex: 1;
  min-height: 0;
  overflow: hidden;
}

/* 编辑器需要确定高度：宿主铺满浮层正文区，滚动交给 BlockSuite 自己。
   页面宽度/字号沿用工作台根节点上的 --affine-* 变量，这里不再覆盖。 */
.tpl-editor-host {
  position: absolute;
  inset: 0;
}

.tpl-editor-mount {
  position: absolute;
  inset: 0;
  overflow: hidden;
}

.tpl-editor-loading,
.tpl-editor-error {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--app-space-2);
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
}
</style>
