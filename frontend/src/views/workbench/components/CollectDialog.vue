<template>
  <!-- 识别事项：AI 识别 / 正文待办 的候选 → 预览勾选（可编辑）→ 确认后同步到「事项」库 -->
  <t-dialog
    :visible="visible"
    :header="t('workbench.collect.title')"
    width="640px"
    :confirm-btn="t('workbench.collect.confirm')"
    :cancel-btn="t('workbench.collect.cancel')"
    :confirm-disabled="!canConfirm"
    :confirm-loading="loading"
    @confirm="onConfirm"
    @cancel="close"
    @close="close"
  >
    <div class="collect">
      <p class="collect-hint">{{ loading ? t('workbench.collect.recognizing') : hint }}</p>

      <p v-if="!loading && rows.length === 0" class="collect-empty">{{ emptyText }}</p>

      <template v-else>
        <div class="collect-head">
          <span>
            {{ t('workbench.collect.count', { selected: selectedCount, total: rows.length }) }}
          </span>
          <div class="collect-head-actions">
            <t-button variant="text" size="small" @click="selectAll(true)">
              {{ t('workbench.collect.selectAll') }}
            </t-button>
            <t-button variant="text" size="small" @click="selectAll(false)">
              {{ t('workbench.collect.selectNone') }}
            </t-button>
          </div>
        </div>

        <ul class="collect-list">
          <li v-for="(row, index) in rows" :key="index" :class="{ 'is-off': !checked[index] }">
            <label class="collect-row">
              <t-checkbox v-model="checked[index]" />
              <!-- 标题就地可改：AI 给出的措辞常常需要用户润色后才入库 -->
              <input
                v-model="row.title"
                class="collect-title"
                :aria-label="t('workbench.collect.titleField')"
              />
            </label>
            <div class="collect-meta">
              <span v-if="row.due" class="collect-chip">{{ t('workbench.database.columns.date') }} {{ row.due }}</span>
              <span v-if="row.owner" class="collect-chip">{{ row.owner }}</span>
              <span v-if="row.priority" class="collect-chip">{{ row.priority }}</span>
            </div>
            <p v-if="row.evidence" class="collect-evidence">{{ row.evidence }}</p>
          </li>
        </ul>
      </template>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
/**
 * 识别事项预览弹窗。
 *
 * 责任边界：只做「展示 + 勾选 + 编辑标题 + 确认」，不写编辑器、不写事项库。
 * 候选来源可以是 AI 抽取，也可以是正文待办规则路径；写入由所属页面负责。
 */
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ItemCandidate } from '@/workbench/ai/item-candidates'

const props = defineProps<{
  visible: boolean
  /** 待确认的候选，按出现顺序排列；打开时复制一份供就地编辑 */
  candidates: ItemCandidate[]
  /** AI 正在识别：展示进度文案并禁用确认 */
  loading?: boolean
  /** 候选来源：决定提示语与空态文案 */
  origin?: 'ai' | 'archive' | 'todo'
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  confirm: [candidates: ItemCandidate[]]
}>()

const { t } = useI18n()

/** 可编辑副本：直接改 props 会污染调用方状态 */
const rows = ref<ItemCandidate[]>([])
/** 与 rows 同下标的勾选状态；每次打开默认全选 */
const checked = ref<boolean[]>([])

const hint = computed(() => {
  if (props.origin === 'todo') return t('workbench.collect.hintTodo')
  return t('workbench.collect.hint')
})

const emptyText = computed(() =>
  props.origin === 'todo' ? t('workbench.collect.empty') : t('workbench.collect.emptyAi'),
)

watch(
  () => [props.visible, props.candidates] as const,
  ([open]) => {
    if (!open) return
    rows.value = props.candidates.map((candidate) => ({ ...candidate }))
    checked.value = rows.value.map(() => true)
  },
  { immediate: true, deep: true },
)

const selectedCount = computed(() => checked.value.filter(Boolean).length)
const canConfirm = computed(
  () => !props.loading && rows.value.length > 0 && selectedCount.value > 0,
)

function selectAll(value: boolean) {
  checked.value = rows.value.map(() => value)
}

function close() {
  emit('update:visible', false)
}

function onConfirm() {
  const picked = rows.value
    .filter((row, index) => checked.value[index] && row.title.trim())
    .map((row) => ({ ...row, title: row.title.trim() }))
  if (picked.length === 0) return
  emit('confirm', picked)
}
</script>

<style lang="less" scoped>
.collect {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-2);
}

.collect-hint,
.collect-empty {
  margin: 0;
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}

.collect-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}

.collect-head-actions {
  display: flex;
  align-items: center;
  gap: var(--app-space-1);
}

.collect-list {
  max-height: 380px;
  margin: 0;
  padding: 0;
  overflow-y: auto;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  list-style: none;
}

.collect-list li {
  padding: var(--app-space-2) var(--app-space-3);
}

.collect-list li + li {
  border-top: 1px solid var(--td-component-stroke);
}

.collect-list li.is-off {
  background: var(--td-bg-color-secondarycontainer);
}

.collect-row {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  cursor: pointer;
}

.collect-title {
  flex: 1;
  min-width: 0;
  padding: 2px 4px;
  border: 1px solid transparent;
  border-radius: var(--app-radius-xs);
  background: transparent;
  color: var(--td-text-color-primary);
  font-size: var(--app-text-sm);
  font-family: inherit;

  &:hover,
  &:focus {
    border-color: var(--td-component-stroke);
    background: var(--td-bg-color-container);
    outline: none;
  }
}

.collect-meta {
  display: flex;
  flex-wrap: wrap;
  gap: var(--app-space-1);
  margin: 4px 0 0 26px;
}

.collect-chip {
  padding: 0 6px;
  border-radius: var(--app-radius-pill);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-2xs);
  line-height: 18px;
}

.collect-evidence {
  margin: 4px 0 0 26px;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-2xs);
  line-height: 1.5;
  word-break: break-word;
}
</style>
