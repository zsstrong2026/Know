<template>
  <!--
    智能关联（P3）：召回结果 → 预览勾选 → 一次性写入画布。

    口径与画布 AI 一致：服务端只给结果，这边只做「勾选 + 确认」，
    写入由条目页通过 `apply` 回调完成，保证画布变更始终走同一条保存链路。
  -->
  <t-dialog
    :visible="visible"
    :header="t('workbench.relation.title')"
    width="680px"
    :confirm-btn="t('workbench.relation.apply')"
    :cancel-btn="t('workbench.relation.cancel')"
    :confirm-loading="applying"
    :confirm-disabled="selectedCount === 0"
    @confirm="onApply"
    @cancel="close"
    @close="close"
  >
    <div class="wrs">
      <p class="wrs-hint">{{ t('workbench.relation.hint') }}</p>

      <div v-if="loading" class="wrs-empty">{{ t('workbench.relation.loading') }}</div>

      <template v-else>
        <div v-if="degraded" class="wrs-warn">
          <t-icon name="error-circle" size="13px" />
          <span>{{ t('workbench.relation.degraded') }}</span>
        </div>

        <p v-if="rows.length === 0" class="wrs-empty">{{ t('workbench.relation.empty') }}</p>

        <template v-else>
          <div class="wrs-bar">
            <span>{{ t('workbench.relation.count', { selected: selectedCount, total: rows.length }) }}</span>
            <t-button variant="text" size="small" @click="selectAll(true)">
              {{ t('workbench.relation.selectAll') }}
            </t-button>
          </div>

          <ul class="wrs-list">
            <li v-for="(row, index) in strongRows" :key="row.item_id">
              <label class="wrs-row">
                <t-checkbox v-model="checked[index]" />
                <span class="wrs-name">{{ row.title || t('workbench.relation.untitled') }}</span>
                <span class="wrs-tag is-strong">{{ t('workbench.relation.strong') }}</span>
                <button class="wrs-ignore" type="button" @click.prevent="ignore(row.item_id)">
                  {{ t('workbench.relation.ignore') }}
                </button>
              </label>
              <p class="wrs-reason">{{ row.reason }}</p>
              <p v-if="row.target_quote" class="wrs-quote">「{{ row.target_quote }}」</p>
            </li>
          </ul>

          <!-- 弱关联单独折叠：只会稀释注意力，默认不勾选 -->
          <div v-if="weakRows.length > 0" class="wrs-more">
            <button class="wrs-more-btn" type="button" @click="weakOpen = !weakOpen">
              <t-icon :name="weakOpen ? 'chevron-up' : 'chevron-down'" size="12px" />
              {{ t('workbench.relation.moreWeak', { count: weakRows.length }) }}
            </button>
            <ul v-if="weakOpen" class="wrs-list">
              <li v-for="row in weakRows" :key="row.item_id">
                <label class="wrs-row">
                  <t-checkbox v-model="weakChecked[row.item_id]" />
                  <span class="wrs-name">{{ row.title || t('workbench.relation.untitled') }}</span>
                  <span class="wrs-tag">{{ t('workbench.relation.weak') }}</span>
                  <button class="wrs-ignore" type="button" @click.prevent="ignore(row.item_id)">
                    {{ t('workbench.relation.ignore') }}
                  </button>
                </label>
                <p class="wrs-reason">{{ row.reason }}</p>
              </li>
            </ul>
          </div>
        </template>
      </template>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
/**
 * 智能关联预览对话框（P3）。
 *
 * 三个约定：
 *   - strong 默认勾选，weak 折叠且默认不勾选（弱关联误报率高，不该默认写进画布）；
 *   - 「忽略」只影响本次会话的排除集，并且会持久化到关联状态，下次召回不再出现；
 *   - 空结果时给出明确空态，而不是一个没有内容的确认按钮。
 */
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { requestWorkbenchRelations, type WorkbenchRelation } from '@/api/workbench'

const props = defineProps<{
  visible: boolean
  itemId: string
  agentId?: string
  /** 已应用的条目 id：召回时排除，避免重复推荐 */
  appliedItemIds: string[]
  /** 用户忽略过的条目 id */
  ignoredItemIds: string[]
  /** 确认写入；返回成功写入的条数 */
  apply: (relations: WorkbenchRelation[]) => number
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  applied: [count: number]
  ignored: [itemIds: string[]]
}>()

const { t } = useI18n()

const loading = ref(false)
const applying = ref(false)
const degraded = ref(false)
const rows = ref<WorkbenchRelation[]>([])
/** 与 strongRows 同下标的勾选状态 */
const checked = ref<boolean[]>([])
/** 弱关联按 itemId 记勾选，避免折叠展开时下标与 weakRows 错位 */
const weakChecked = ref<Record<string, boolean>>({})
const weakOpen = ref(false)
/** 本次会话内点过「忽略」的条目：直接给父级，由父级持久化 */
const ignoredHere = ref<string[]>([])

const strongRows = computed(() => rows.value.filter((row) => row.strength === 'strong'))
const weakRows = computed(() => rows.value.filter((row) => row.strength !== 'strong'))
const selectedCount = computed(
  () =>
    checked.value.filter(Boolean).length +
    Object.values(weakChecked.value).filter(Boolean).length,
)

watch(
  () => props.visible,
  (visible) => {
    if (!visible) return
    ignoredHere.value = []
    weakOpen.value = false
    void load()
  },
  { immediate: true },
)

async function load() {
  loading.value = true
  degraded.value = false
  rows.value = []
  checked.value = []
  weakChecked.value = {}
  try {
    const result = await requestWorkbenchRelations({
      item_id: props.itemId,
      agent_id: props.agentId,
      exclude_item_ids: [...props.appliedItemIds, ...props.ignoredItemIds],
    })
    degraded.value = Boolean(result.degraded_reason)
    rows.value = result.relations ?? []
    // 强关联默认全选；弱关联默认不选（模型自述「只是主题相近」的，不该默认落画布）
    checked.value = rows.value.map((row) => row.strength === 'strong')
    weakChecked.value = Object.fromEntries(
      rows.value.filter((row) => row.strength !== 'strong').map((row) => [row.item_id, false]),
    )
  } catch {
    degraded.value = false
    rows.value = []
  } finally {
    loading.value = false
  }
}

function selectAll(value: boolean) {
  checked.value = checked.value.map(() => value)
  // 全部取消时连折叠里的弱关联一起清掉，否则「已选 N」与列表观感不符
  if (!value) weakChecked.value = {}
}

function ignore(itemId: string) {
  if (ignoredHere.value.includes(itemId)) return
  ignoredHere.value.push(itemId)
  rows.value = rows.value.filter((row) => row.item_id !== itemId)
  // 下标随 rows 变化，必须重建勾选状态
  checked.value = rows.value.map((row) => row.strength === 'strong')
  emit('ignored', [...ignoredHere.value])
}

function onApply() {
  if (applying.value) return
  const picked: WorkbenchRelation[] = []
  let strongIndex = 0
  for (const row of rows.value) {
    if (row.strength === 'strong') {
      if (checked.value[strongIndex]) picked.push(row)
      strongIndex += 1
    } else if (weakChecked.value[row.item_id]) {
      picked.push(row)
    }
  }
  if (picked.length === 0) return
  applying.value = true
  try {
    emit('applied', props.apply(picked))
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
.wrs {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-3);
}

.wrs-hint {
  margin: 0;
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}

.wrs-warn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  font-size: var(--app-text-xs);
  color: #ed7b2f;
}

.wrs-empty {
  padding: var(--app-space-5) 0;
  text-align: center;
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}

.wrs-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}

.wrs-list {
  max-height: 380px;
  margin: 0;
  padding: 0;
  overflow-y: auto;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  list-style: none;
}

.wrs-list li {
  padding: var(--app-space-2) var(--app-space-3);
}

.wrs-list li + li {
  border-top: 1px solid var(--td-component-stroke);
}

.wrs-row {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  cursor: pointer;
}

.wrs-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--app-text-sm);
}

.wrs-tag {
  padding: 0 6px;
  border-radius: var(--app-radius-pill);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-2xs);
  line-height: 18px;

  &.is-strong {
    background: #e6f2ff;
    color: #0052d9;
  }
}

.wrs-ignore {
  padding: 0;
  border: none;
  background: transparent;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-2xs);
  cursor: pointer;

  &:hover {
    color: var(--td-text-color-primary);
  }
}

.wrs-reason {
  margin: 4px 0 0 26px;
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-secondary);
}

.wrs-quote {
  margin: 2px 0 0 26px;
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.wrs-more {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-2);
}

.wrs-more-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 0;
  border: none;
  background: transparent;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-xs);
  cursor: pointer;
}
</style>
