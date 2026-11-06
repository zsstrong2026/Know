<template>
  <!-- 数据库 AI（P3-5）：自然语言 → 补丁 → 预览 → 一次性写入 -->
  <t-dialog
    :visible="visible"
    :header="t('workbench.database.ai.title')"
    width="720px"
    :confirm-btn="confirmLabel"
    :cancel-btn="t('workbench.database.ai.cancel')"
    :confirm-loading="applying"
    :confirm-disabled="!canApply"
    @confirm="onApply"
    @cancel="close"
    @close="close"
  >
    <div class="dbai">
      <p class="dbai-hint">{{ t('workbench.database.ai.hint') }}</p>

      <t-textarea
        v-model="prompt"
        :maxlength="PROMPT_MAX"
        :autosize="{ minRows: 3, maxRows: 6 }"
        :placeholder="t('workbench.database.ai.placeholder')"
        :disabled="generating"
      />

      <div class="dbai-actions">
        <t-button size="small" theme="primary" :loading="generating" :disabled="!prompt.trim()" @click="generate">
          {{ t('workbench.database.ai.generate') }}
        </t-button>
        <span v-if="context" class="dbai-meta">
          {{
            t('workbench.database.ai.context', {
              columns: context.columns.length,
              rows: context.rows.length,
            })
          }}
        </span>
        <span v-if="context?.truncated" class="dbai-warn">{{ t('workbench.database.ai.truncated') }}</span>
      </div>

      <div v-if="errorKey" class="dbai-error">
        <t-icon name="error-circle" size="13px" />
        <span>{{ t(errorKey) }}</span>
      </div>

      <!-- 预览：逐条列出将执行的 op，覆盖项单独标记 -->
      <div v-if="resolved && resolved.ops.length > 0" class="dbai-preview">
        <div class="dbai-preview-head">
          <b>{{ t('workbench.database.ai.preview.title') }}</b>
          <span class="dbai-counts">
            {{
              t('workbench.database.ai.preview.counts', {
                columns: counts.columns,
                rows: counts.rows,
                cells: counts.cells,
              })
            }}
          </span>
        </div>
        <ul class="dbai-list">
          <li v-for="(op, index) in resolved.ops" :key="index" :class="{ 'is-overwrite': isOverwrite(op) }">
            <span class="dbai-tag">{{ tagOf(op) }}</span>
            <span class="dbai-text">{{ describe(op) }}</span>
            <span v-if="isOverwrite(op)" class="dbai-overwrite">
              {{ t('workbench.database.ai.preview.overwrite') }}
            </span>
          </li>
        </ul>

        <label v-if="resolved.overwriteCount > 0" class="dbai-confirm">
          <t-checkbox v-model="allowOverwrite" />
          <span>{{ t('workbench.database.ai.preview.overwriteConfirm', { count: resolved.overwriteCount }) }}</span>
        </label>

        <p v-if="resolved.errors.length > 0" class="dbai-error-text">
          {{ t('workbench.database.ai.preview.invalid', { count: resolved.errors.length }) }}
        </p>
      </div>

      <p v-if="resolved && resolved.ops.length === 0 && !errorKey && !generating" class="dbai-empty">
        {{ resolved.errors.length > 0 ? t('workbench.database.ai.preview.blocked') : t('workbench.database.ai.preview.empty') }}
      </p>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
/**
 * 数据库 AI 对话框。
 *
 * 责任边界：
 *   - 只负责收集需求、展示预览、确认覆盖；
 *   - 请求由 api/workbench.ts 发出，补丁解析在 workbench/ai/database-patch.ts；
 *   - 真正写入通过 controller.applyAiPatch 落到 BlockSuite，组件不碰 store。
 */
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { requestDatabasePatch } from '@/api/workbench'
import {
  resolvePatch,
  summarizePatch,
  type AIPatch,
  type ResolvedOp,
  type ResolvedPatch,
} from '@/workbench/ai/database-patch'
import { WORKBENCH_ERROR, workbenchErrorCode } from '@/workbench/error-code'
import type { DatabaseToolbarController } from '@/workbench/editor/database-session'
import type { DatabaseAIContext } from '@/workbench/editor/database-ai'

const PROMPT_MAX = 1000

const props = defineProps<{
  visible: boolean
  itemId: string
  controller: DatabaseToolbarController | null
  /** 默认智能体 id；空表示跟随租户默认模型 */
  agentId?: string
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
}>()

const { t } = useI18n()

const prompt = ref('')
const generating = ref(false)
const applying = ref(false)
const errorKey = ref('')
const allowOverwrite = ref(false)
const context = ref<DatabaseAIContext | null>(null)
const resolved = ref<ResolvedPatch | null>(null)

const counts = computed(() =>
  resolved.value ? summarizePatch(resolved.value) : { columns: 0, rows: 0, cells: 0 },
)

/** 有可执行 op 且没有解析错误时才允许写入；覆盖项还要额外勾选。 */
const canApply = computed(() => {
  const patch = resolved.value
  if (!patch || patch.ops.length === 0 || patch.errors.length > 0) return false
  if (patch.overwriteCount > 0 && !allowOverwrite.value) return false
  return true
})

const confirmLabel = computed(() =>
  canApply.value ? t('workbench.database.ai.apply') : t('workbench.database.ai.applyDisabled'),
)

/** 每次打开都重新采样上下文，避免用上一次的表结构生成。 */
watch(
  () => props.visible,
  (open) => {
    if (!open) return
    prompt.value = ''
    errorKey.value = ''
    allowOverwrite.value = false
    resolved.value = null
    context.value = props.controller?.aiContext() ?? null
  },
  { immediate: true },
)

function close() {
  emit('update:visible', false)
}

async function generate() {
  const controller = props.controller
  if (!controller) {
    errorKey.value = 'workbench.database.ai.state.notReady'
    return
  }
  // 生成前重新采样：用户可能刚刚手动改过表结构。
  context.value = controller.aiContext()
  if (!context.value) {
    errorKey.value = 'workbench.database.ai.state.notReady'
    return
  }

  generating.value = true
  errorKey.value = ''
  resolved.value = null
  allowOverwrite.value = false
  try {
    const response = await requestDatabasePatch({
      item_id: props.itemId,
      prompt: prompt.value.trim(),
      agent_id: props.agentId || undefined,
      columns: context.value.columns,
      rows: context.value.rows,
      truncated: context.value.truncated,
    })
    resolved.value = resolvePatch(response.patch as AIPatch, {
      columns: context.value.columns,
      rows: context.value.rows,
    })
    if (resolved.value.errors.length > 0) {
      errorKey.value = 'workbench.database.ai.preview.blocked'
    }
  } catch (error) {
    errorKey.value = errorKeyOf(error)
  } finally {
    generating.value = false
  }
}

function onApply() {
  const patch = resolved.value
  if (!patch || !canApply.value) return
  applying.value = true
  try {
    const outcome = props.controller?.applyAiPatch(patch, allowOverwrite.value)
    if (!outcome) {
      errorKey.value = 'workbench.database.ai.state.notReady'
      return
    }
    MessagePlugin.success(
      t('workbench.database.ai.applied', {
        columns: outcome.columns,
        rows: outcome.rows,
        cells: outcome.cells,
      }),
    )
    close()
  } catch {
    // 事务内抛错已整体回滚，这里只提示，不保留半截写入。
    errorKey.value = 'workbench.database.ai.applyFailed'
  } finally {
    applying.value = false
  }
}

/** 后端错误码 → 提示 key；2408 引导去配置模型，2410 让用户重新生成。 */
function errorKeyOf(error: unknown): string {
  const code = workbenchErrorCode(error)
  if (code === WORKBENCH_ERROR.noModel) return 'workbench.database.ai.state.noModel'
  if (code === WORKBENCH_ERROR.payloadTooLarge) return 'workbench.database.ai.state.tooLong'
  if (code === WORKBENCH_ERROR.notFound) return 'workbench.database.ai.state.notFound'
  if (code === WORKBENCH_ERROR.streamBroken) return 'workbench.database.ai.state.invalid'
  return 'workbench.database.ai.state.failed'
}

function isOverwrite(op: ResolvedOp): boolean {
  return op.kind === 'set_cell' && op.overwrite
}

/** 预览里的短标签：新建列 / 新建行 / 填单元格。 */
function tagOf(op: ResolvedOp): string {
  if (op.kind === 'add_property') return t('workbench.database.ai.preview.tagColumn')
  if (op.kind === 'add_row') return t('workbench.database.ai.preview.tagRow')
  return t('workbench.database.ai.preview.tagCell')
}

/** 人话描述；不展示原始 JSON，避免用户看不懂也没法判断。 */
function describe(op: ResolvedOp): string {
  if (op.kind === 'add_property') {
    return op.options.length > 0
      ? t('workbench.database.ai.preview.columnWithOptions', {
          name: op.name,
          type: op.type,
          options: op.options.join(' / '),
        })
      : t('workbench.database.ai.preview.column', { name: op.name, type: op.type })
  }
  if (op.kind === 'add_row') {
    const cells = op.cells.map((cell) => `${cell.columnName}=${cell.value}`).join('；')
    return cells
      ? t('workbench.database.ai.preview.row', { title: op.title || '—', cells })
      : t('workbench.database.ai.preview.rowOnly', { title: op.title || '—' })
  }
  return t('workbench.database.ai.preview.cell', {
    row: op.rowTitle || op.rowId,
    column: op.columnName,
    value: op.value,
  })
}
</script>

<style lang="less" scoped>
.dbai {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-2);
}

.dbai-hint {
  margin: 0;
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}

.dbai-actions {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
}

.dbai-meta {
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}

.dbai-warn {
  font-size: var(--app-text-2xs);
  color: var(--td-warning-color);
}

.dbai-error {
  display: flex;
  align-items: center;
  gap: var(--app-space-1);
  font-size: var(--app-text-xs);
  color: var(--td-error-color);
}

.dbai-error-text,
.dbai-empty {
  margin: 0;
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}

.dbai-preview {
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  padding: var(--app-space-2);
}

.dbai-preview-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  margin-bottom: var(--app-space-1);
  font-size: var(--app-text-sm);
}

.dbai-counts {
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}

.dbai-list {
  max-height: 280px;
  overflow-y: auto;
  margin: 0;
  padding: 0;
  list-style: none;

  li {
    display: flex;
    align-items: baseline;
    gap: var(--app-space-2);
    padding: 3px 0;
    font-size: var(--app-text-xs);
    color: var(--td-text-color-primary);
  }

  li.is-overwrite .dbai-text {
    color: var(--td-warning-color);
  }
}

.dbai-tag {
  flex: none;
  padding: 0 var(--app-space-1);
  border-radius: var(--app-radius-xs);
  background: var(--td-brand-color-1);
  font-size: var(--app-text-2xs);
  color: var(--td-brand-color);
}

.dbai-text {
  min-width: 0;
  word-break: break-word;
}

.dbai-overwrite {
  flex: none;
  font-size: var(--app-text-2xs);
  color: var(--td-warning-color);
}

.dbai-confirm {
  display: flex;
  align-items: center;
  gap: var(--app-space-1);
  margin-top: var(--app-space-2);
  font-size: var(--app-text-xs);
  color: var(--td-warning-color);
  cursor: pointer;
}
</style>
