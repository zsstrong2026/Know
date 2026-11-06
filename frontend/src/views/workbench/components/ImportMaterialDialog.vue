<template>
  <!-- 导入资料（P4-1）：加入工作知识库 / 作为画布附件两条路径共用一次选文件 -->
  <t-dialog
    v-model:visible="visible"
    :header="t('workbench.import.title')"
    width="620px"
    :footer="false"
    :close-on-overlay-click="!running"
    :close-on-esc-keydown="!running"
    @close="onClose"
  >
    <div class="imp-body">
      <!-- 路径二选一：只有在条目编辑页打开时才提供「作为附件」 -->
      <t-radio-group v-if="canAttach" v-model="target" variant="default-filled">
        <t-radio-button value="kb">{{ t('workbench.import.target.kb') }}</t-radio-button>
        <t-radio-button value="attachment">{{ t('workbench.import.target.attachment') }}</t-radio-button>
      </t-radio-group>
      <p class="imp-hint">{{ hint }}</p>

      <div class="imp-drop" :class="{ 'is-busy': running }" @click="pickFiles">
        <t-icon name="upload" size="20px" />
        <span class="imp-drop-title">{{ t('workbench.import.pick') }}</span>
        <span class="imp-drop-sub">{{ pickHint }}</span>
        <input
          ref="inputRef"
          type="file"
          multiple
          :accept="IMPORT_ACCEPT"
          style="display: none"
          @change="onFileChange"
        />
      </div>

      <p v-if="rejected.length > 0" class="imp-rejected">
        {{ t('workbench.import.rejected', { count: rejected.length }) }}
        <span v-for="row in rejected.slice(0, 3)" :key="`${row.name}:${row.reason}`" class="imp-rejected-row">
          {{ row.name }} · {{ t(`workbench.import.reason.${row.reason}`) }}
        </span>
      </p>

      <div v-if="tasks.length > 0" class="imp-list">
        <div v-for="task in tasks" :key="task.key" class="imp-row">
          <t-icon :name="iconFor(task.state)" size="15px" :class="`imp-state is-${task.state}`" />
          <span class="imp-name" :title="task.file.name">{{ task.file.name }}</span>
          <span class="imp-size">{{ formatFileSize(task.file.size) }}</span>
          <span class="imp-note">{{ noteFor(task) }}</span>
        </div>
      </div>

      <div class="imp-footer">
        <span class="imp-count">{{ t('workbench.import.selected', { count: tasks.length }) }}</span>
        <span class="spacer" />
        <t-button variant="outline" :disabled="running" @click="onClose">
          {{ t('workbench.import.cancel') }}
        </t-button>
        <t-button
          v-if="failedCount(tasks) > 0 && !running"
          variant="outline"
          @click="retry"
        >
          {{ t('workbench.import.retry', { count: failedCount(tasks) }) }}
        </t-button>
        <t-button
          theme="primary"
          :loading="running"
          :disabled="pendingCount(tasks) === 0 && failedCount(tasks) === 0"
          @click="submit"
        >
          {{ t('workbench.import.submit') }}
        </t-button>
      </div>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
/**
 * 导入资料对话框（P4-1，实现方案 §12.3）。
 *
 * 两条路径的差别只在「交给谁」：
 *   - 加入工作知识库：交给全局上传队列（进度、取消、重试都在那边），本对话框只做入口；
 *   - 作为画布附件：本对话框串行上传成条目资产，再让条目页插入 attachment 块。
 * 校验（格式/大小/空文件）与逐项状态机在 workbench/import-material.ts，便于单测。
 */
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { useWorkbenchStore } from '@/stores/workbench'
import { useUploadTasksStore } from '@/stores/uploadTasks'
import { formatFileSize } from '@/utils/files'
import {
  ATTACHMENT_MAX_BYTES,
  IMPORT_ACCEPT,
  IMPORT_MAX_BYTES,
  collectAttachments,
  createImportTasks,
  failedCount,
  pendingCount,
  pickImportFiles,
  resetFailedTasks,
  runImportTasks,
  type ImportTask,
  type RejectedFile,
} from '@/workbench/import-material'
import type { ImportedAttachment } from '@/workbench/import-material'

const props = defineProps<{
  /** 与同目录其他对话框一致：v-model:visible */
  visible: boolean
  /** 有值时才能选「作为附件」：附件必须挂在某个条目上 */
  itemId?: string
  /** 上传单个附件，返回 assetId（由条目页注入，避免这里反向依赖编辑器） */
  uploadAsset?: (file: File) => Promise<string>
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  /** 附件全部上传完成后交给条目页插入块 */
  attach: [files: ImportedAttachment[]]
  /** 已提交到工作知识库上传队列 */
  enqueued: [count: number]
}>()

const { t } = useI18n()
const store = useWorkbenchStore()
const uploadTasks = useUploadTasksStore()

const visible = computed({
  get: () => props.visible,
  set: (value: boolean) => emit('update:visible', value),
})

const inputRef = ref<HTMLInputElement | null>(null)
const target = ref<'kb' | 'attachment'>('kb')
const tasks = ref<ImportTask[]>([])
const rejected = ref<RejectedFile[]>([])
const running = ref(false)

/** 只有条目页 + 注入了上传函数时才提供附件路径 */
const canAttach = computed(() => Boolean(props.itemId && props.uploadAsset))

const hint = computed(() =>
  target.value === 'attachment'
    ? t('workbench.import.hint.attachment')
    : t('workbench.import.hint.kb'),
)

/** 附件路径的上限是 20MB，提示文案要跟着路径变，否则用户会以为挑错了文件。 */
const pickHint = computed(() =>
  target.value === 'attachment'
    ? t('workbench.import.pickHintAttachment')
    : t('workbench.import.pickHint'),
)

watch(
  () => props.visible,
  (open) => {
    if (!open) return
    tasks.value = []
    rejected.value = []
    running.value = false
    target.value = 'kb'
  },
)

function pickFiles() {
  if (running.value) return
  inputRef.value?.click()
}

function onFileChange(event: Event) {
  const input = event.target as HTMLInputElement
  const picked = Array.from(input.files ?? [])
  input.value = ''
  if (picked.length === 0) return
  // 附件路径走条目资产接口，上限比知识库上传更小（与后端常量同口径）
  const maxBytes = target.value === 'attachment' ? ATTACHMENT_MAX_BYTES : IMPORT_MAX_BYTES
  const { accepted, rejected: invalid } = pickImportFiles(picked, maxBytes)
  rejected.value = invalid
  // 追加而不是替换：允许分几次挑文件再一起导入
  tasks.value = [...tasks.value, ...createImportTasks(accepted)]
}

/**
 * 切到附件路径时把超过附件上限的待导入项挪到「已跳过」。
 *
 * 两条路径的上限不同（50MB vs 20MB），不重新筛一遍会让用户在提交时才
 * 收到服务端的 413，白等一次上传。
 */
watch(target, (next) => {
  if (next !== 'attachment') return
  const oversized = tasks.value.filter((task) => task.file.size > ATTACHMENT_MAX_BYTES)
  if (oversized.length === 0) return
  tasks.value = tasks.value.filter((task) => task.file.size <= ATTACHMENT_MAX_BYTES)
  rejected.value = [
    ...rejected.value,
    ...oversized.map((task) => ({ name: task.file.name, reason: 'too_large' as const })),
  ]
})

function iconFor(state: ImportTask['state']): string {
  switch (state) {
    case 'done':
      return 'check-circle'
    case 'failed':
      return 'error-circle'
    case 'uploading':
      return 'loading'
    default:
      return 'file'
  }
}

function noteFor(task: ImportTask): string {
  if (task.state === 'done') return t('workbench.import.state.done')
  if (task.state === 'failed') return t('workbench.import.state.failed')
  if (task.state === 'uploading') return t('workbench.import.state.uploading')
  return t('workbench.import.state.pending')
}

function onClose() {
  if (running.value) return
  visible.value = false
}

/** 加入工作知识库：交给全局上传队列，本对话框立即关闭。 */
function submitToKb() {
  const kbId = store.workKbId
  if (!kbId) {
    MessagePlugin.error(t('workbench.import.failed'))
    return
  }
  const files = tasks.value.filter((task) => task.state === 'pending').map((task) => task.file)
  if (files.length === 0) return
  uploadTasks.enqueue({
    kbId,
    kbName: store.bootstrap?.work_kb.name ?? '',
    uploads: files.map((file) => ({ file })),
  })
  MessagePlugin.success(t('workbench.import.enqueued', { count: files.length }))
  emit('enqueued', files.length)
  visible.value = false
}

/** 作为附件：串行上传后把成功的资产交给条目页插入。 */
async function runAttachmentTasks() {
  const upload = props.uploadAsset
  if (!upload) return
  running.value = true
  try {
    tasks.value = await runImportTasks(
      tasks.value,
      async (file) => ({ assetId: await upload(file) }),
      (next) => {
        tasks.value = next
      },
    )
    const attachments = collectAttachments(tasks.value)
    if (attachments.length > 0) {
      emit('attach', attachments)
      MessagePlugin.success(t('workbench.import.attached', { count: attachments.length }))
    }
    // 有失败项时停在对话框里，用户可以单独重试
    if (failedCount(tasks.value) === 0) visible.value = false
  } finally {
    running.value = false
  }
}

function submit() {
  if (running.value) return
  // 没有待处理项（全部成功或全部失败）时不重复提交：失败项走「重试」
  if (pendingCount(tasks.value) === 0) {
    if (failedCount(tasks.value) === 0) visible.value = false
    return
  }
  if (target.value === 'kb') submitToKb()
  else void runAttachmentTasks()
}

function retry() {
  tasks.value = resetFailedTasks(tasks.value)
  void submit()
}
</script>

<style lang="less" scoped>
.imp-body {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-3);
}

.imp-hint {
  margin: 0;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-xs);
  line-height: 1.6;
}

.imp-drop {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  padding: var(--app-space-8) 0;
  border: 1px dashed var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-secondary);
  cursor: pointer;
  transition: border-color var(--app-motion-fast) ease;

  &:hover {
    border-color: var(--td-brand-color);
  }

  &.is-busy {
    pointer-events: none;
    opacity: 0.6;
  }
}

.imp-drop-title {
  color: var(--td-text-color-primary);
  font-size: var(--app-text-md);
}

.imp-drop-sub {
  font-size: var(--app-text-xs);
}

.imp-rejected {
  display: flex;
  flex-direction: column;
  gap: 2px;
  margin: 0;
  color: var(--td-warning-color);
  font-size: var(--app-text-xs);
}

.imp-rejected-row {
  color: var(--td-text-color-placeholder);
}

.imp-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
  max-height: 260px;
  overflow-y: auto;
  scrollbar-width: thin;
}

.imp-row {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  min-height: 38px;
  padding: 4px 8px;
  border-radius: var(--app-radius-md);

  &:hover {
    background: var(--td-bg-color-container-hover);
  }
}

.imp-state {
  flex: none;
  color: var(--td-text-color-placeholder);

  &.is-done {
    color: var(--td-success-color);
  }

  &.is-failed {
    color: var(--td-error-color);
  }
}

.imp-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  font-size: var(--app-text-md);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.imp-size,
.imp-note {
  flex: none;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-2xs);
}

.imp-footer {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  padding-top: var(--app-space-3);
  border-top: 1px solid var(--td-component-stroke);

  .spacer {
    flex: 1;
  }
}

.imp-count {
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-xs);
}
</style>
