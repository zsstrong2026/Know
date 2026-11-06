<template>
  <!-- 画布 AI（P2-6）：自然语言 → 分组/平移补丁 → 预览 → 一次性写入 -->
  <t-dialog
    :visible="visible"
    :header="t('workbench.canvasAi.title')"
    width="680px"
    :confirm-btn="t('workbench.canvasAi.apply')"
    :cancel-btn="t('workbench.canvasAi.cancel')"
    :confirm-loading="applying"
    :confirm-disabled="!plan || plan.ops.length === 0"
    @confirm="onApply"
    @cancel="close"
    @close="close"
  >
    <div class="cai">
      <p class="cai-hint">{{ t('workbench.canvasAi.hint') }}</p>

      <t-textarea
        v-model="prompt"
        :maxlength="PROMPT_MAX"
        :autosize="{ minRows: 3, maxRows: 6 }"
        :placeholder="t('workbench.canvasAi.placeholder')"
        :disabled="generating"
      />

      <div class="cai-actions">
        <t-button size="small" theme="primary" :loading="generating" :disabled="!prompt.trim() || !snapshot" @click="generate">
          {{ t('workbench.canvasAi.generate') }}
        </t-button>
        <span v-if="snapshot" class="cai-meta">
          {{ t('workbench.canvasAi.context', { elements: snapshot.elements.length, frames: snapshot.frames.length }) }}
        </span>
        <span v-if="snapshot?.truncated" class="cai-warn">{{ t('workbench.canvasAi.truncated') }}</span>
      </div>

      <div v-if="errorKey" class="cai-error">
        <t-icon name="error-circle" size="13px" />
        <span>{{ t(errorKey) }}</span>
      </div>

      <!-- 预览：只列将要发生的事，不预渲染画布 -->
      <div v-if="plan" class="cai-preview">
        <div class="cai-preview-head">
          <b>{{ t('workbench.canvasAi.preview') }}</b>
          <span v-if="plan.skipped > 0" class="cai-skip">
            {{ t('workbench.canvasAi.skipped', { count: plan.skipped }) }}
          </span>
        </div>
        <p v-if="plan.ops.length === 0" class="cai-empty">{{ t('workbench.canvasAi.empty') }}</p>
        <ul v-else class="cai-list">
          <li v-for="(op, index) in plan.ops" :key="index">
            <template v-if="op.kind === 'frame'">
              <span class="cai-tag is-frame">{{ t('workbench.canvasAi.frameTag') }}</span>
              {{ t('workbench.canvasAi.frameLine', { title: op.title || t('workbench.canvasAi.untitled'), count: op.memberIds.length }) }}
            </template>
            <template v-else>
              <span class="cai-tag is-move">{{ t('workbench.canvasAi.moveTag') }}</span>
              {{ t('workbench.canvasAi.moveLine', { x: Math.round(op.x), y: Math.round(op.y) }) }}
            </template>
          </li>
        </ul>
      </div>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
/**
 * 画布 AI 对话框（P2-6）。
 *
 * 职责边界：只负责「问模型 → 解析 → 预览」，不碰 store。
 * 快照读取与真正写入由条目页通过 `snapshot` / `apply` 两个函数传进来，
 * 这样对话框不需要知道 BlockSuite，也让写入始终走同一条保存链路。
 */
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { requestCanvasPatch } from '@/api/workbench'
import {
  parseCanvasOps,
  resolveCanvasPlan,
  summarizePlan,
  type CanvasPlan,
} from '@/workbench/ai/canvas-patch'
import type { CanvasSnapshot } from '@/workbench/editor/canvas-ai'

const PROMPT_MAX = 1000

const props = defineProps<{
  visible: boolean
  itemId: string
  agentId?: string
  /** 读当前画布快照；没有可整理的画布时返回 null */
  snapshot: () => CanvasSnapshot | null
  /** 应用计划，返回实际写入的计数；surfaceId 来自打开时的快照 */
  apply: (plan: CanvasPlan, surfaceId: string) => { frames: number; moved: number; failed: number }
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  applied: [outcome: { frames: number; moved: number; failed: number }]
}>()

const { t } = useI18n()
const prompt = ref('')
const generating = ref(false)
const applying = ref(false)
const plan = ref<CanvasPlan | null>(null)
const errorKey = ref('')
/** 打开时抓一次快照：对话框停留期间画布可能变化，但预览与写回都用同一份 */
const snapshot = ref<CanvasSnapshot | null>(null)

watch(
  () => props.visible,
  (visible) => {
    if (!visible) return
    prompt.value = ''
    plan.value = null
    errorKey.value = ''
    snapshot.value = props.snapshot()
  },
  { immediate: true },
)

const summary = computed(() => (plan.value ? summarizePlan(plan.value) : { frames: 0, moves: 0, members: 0 }))

async function generate() {
  const current = snapshot.value
  if (!current || !prompt.value.trim()) return
  generating.value = true
  errorKey.value = ''
  plan.value = null
  try {
    const response = await requestCanvasPatch({
      item_id: props.itemId,
      prompt: prompt.value.trim(),
      agent_id: props.agentId,
      elements: current.elements.map((element) => ({
        id: element.id,
        kind: element.kind,
        text: element.text,
        x: element.x,
        y: element.y,
        w: element.w,
        h: element.h,
        frame_id: element.frameId,
      })),
      frames: current.frames.map((frame) => ({
        id: frame.id,
        title: frame.title,
        x: frame.x,
        y: frame.y,
        w: frame.w,
        h: frame.h,
      })),
      truncated: current.truncated,
    })
    const parsed = parseCanvasOps(response.patch)
    const resolved = resolveCanvasPlan(parsed.ops, current.elements)
    plan.value = { ops: resolved.ops, skipped: resolved.skipped + parsed.skipped }
  } catch {
    errorKey.value = 'workbench.canvasAi.failed'
  } finally {
    generating.value = false
  }
}

function onApply() {
  const current = snapshot.value
  if (!plan.value || !current || applying.value) return
  applying.value = true
  try {
    const outcome = props.apply(plan.value, current.surfaceId)
    emit('applied', outcome)
    close()
  } finally {
    applying.value = false
  }
}

function close() {
  emit('update:visible', false)
}
</script>

<style scoped lang="less">
.cai {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-3);
}

.cai-hint {
  margin: 0;
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}

.cai-actions {
  display: flex;
  align-items: center;
  gap: var(--app-space-3);
}

.cai-meta {
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}

.cai-warn {
  font-size: var(--app-text-2xs);
  color: #ed7b2f;
}

.cai-error {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: var(--app-text-xs);
  color: #c9353f;
}

.cai-preview {
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-sm);
  padding: var(--app-space-3);
}

.cai-preview-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  font-size: var(--app-text-xs);
  margin-bottom: var(--app-space-2);
}

.cai-skip {
  color: var(--td-text-color-placeholder);
}

.cai-empty {
  margin: 0;
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}

.cai-list {
  margin: 0;
  padding-left: 0;
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 6px;
  max-height: 260px;
  overflow-y: auto;
  font-size: var(--app-text-xs);
}

.cai-tag {
  display: inline-block;
  margin-right: 6px;
  padding: 0 5px;
  border-radius: 3px;
  font-size: var(--app-text-2xs);
  line-height: 16px;
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
}

.cai-tag.is-frame {
  background: #e6f2ff;
  color: #0052d9;
}

.cai-tag.is-move {
  background: #e8f8f0;
  color: #0f8a5f;
}
</style>
