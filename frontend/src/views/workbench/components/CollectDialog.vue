<template>
  <!-- 收集事项：规则抽取文档待办 → 预览勾选 → 确认后同步到「事项」库 -->
  <t-dialog
    :visible="visible"
    :header="t('workbench.collect.title')"
    width="560px"
    :confirm-btn="t('workbench.collect.confirm')"
    :cancel-btn="t('workbench.collect.cancel')"
    :confirm-disabled="!canConfirm"
    @confirm="onConfirm"
    @cancel="close"
    @close="close"
  >
    <div class="collect">
      <p class="collect-hint">{{ t('workbench.collect.hint') }}</p>

      <p v-if="todos.length === 0" class="collect-empty">{{ t('workbench.collect.empty') }}</p>

      <template v-else>
        <div class="collect-head">
          <span>
            {{ t('workbench.collect.count', { selected: selectedCount, total: todos.length }) }}
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
          <li v-for="(todo, index) in todos" :key="index">
            <label class="collect-row">
              <t-checkbox v-model="checked[index]" />
              <span class="collect-text" :class="{ 'is-done': todo.done }">{{ todo.title }}</span>
              <span v-if="todo.done" class="collect-tag">{{ t('workbench.collect.done') }}</span>
            </label>
          </li>
        </ul>
      </template>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
/**
 * 收集事项预览弹窗。
 *
 * 责任边界：只做「展示 + 勾选 + 确认」，不写编辑器、不写事项库。
 * 抽取与写入分别由 collect-todos.ts 与所属页面负责，AI 不是必需路径。
 */
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import type { CollectedTodo } from '@/workbench/editor/collect-todos'

const props = defineProps<{
  visible: boolean
  /** 从文档抽取出的待办，按出现顺序排列 */
  todos: CollectedTodo[]
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  confirm: [todos: CollectedTodo[]]
}>()

const { t } = useI18n()

/** 与 todos 同下标的勾选状态；每次打开默认全选 */
const checked = ref<boolean[]>([])

watch(
  () => props.visible,
  (open) => {
    if (open) checked.value = props.todos.map(() => true)
  },
  { immediate: true },
)

const selectedCount = computed(() => checked.value.filter(Boolean).length)
const canConfirm = computed(() => props.todos.length > 0 && selectedCount.value > 0)

function selectAll(value: boolean) {
  checked.value = props.todos.map(() => value)
}

function close() {
  emit('update:visible', false)
}

function onConfirm() {
  const picked = props.todos.filter((_, index) => checked.value[index])
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
  max-height: 320px;
  margin: 0;
  padding: 0;
  overflow-y: auto;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  list-style: none;
}

.collect-list li + li {
  border-top: 1px solid var(--td-component-stroke);
}

.collect-row {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  padding: var(--app-space-2);
  cursor: pointer;
}

.collect-text {
  flex: 1;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-primary);
  word-break: break-word;
}

.collect-text.is-done {
  color: var(--td-text-color-placeholder);
  text-decoration: line-through;
}

.collect-tag {
  flex: none;
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}
</style>
