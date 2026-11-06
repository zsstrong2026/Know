<template>
  <!--
    数据库工具条（对齐 AFFiNE-Know原型.html 的事项表格）。

    原型把「视图 Tab」和「筛选行」拆成两行：Tab 行右侧只有视图设置，筛选行走
    「字段 chip ＋ 筛选 / 排序 / 搜索 / 新建行」。这里通过 Teleport 挂进上游工具行：
    上游的「新建行」按钮被隐藏，换成本组件右侧的主按钮，顺序与原型一致。
  -->
  <Teleport v-if="mountTarget" :to="mountTarget">
    <div class="db-toolbar">
      <!-- Calendar 视图的层级说明（原型：「行级 · 数据来自截止日 · 不读工作台 journal_date」） -->
      <span v-if="isCalendarView" class="db-cal-hint">
        {{ $t('workbench.database.toolbar.calendarHint', { column: calendarColumnName }) }}
      </span>

      <!-- Calendar 视图有多个日期属性时，需要指定用哪一列驱动日历；
           只有一个时按原型显示「日期属性：截止日」的静态说明，不占一个下拉 -->
      <t-select
        v-if="showDateColumn"
        v-model="dateColumnValue"
        class="db-date-select"
        size="small"
        :options="dateColumnOptions"
        :aria-label="$t('workbench.database.toolbar.dateColumn')"
        @change="onDateColumnChange"
      />
      <!-- 只有一个日期属性时不放下拉：按原型显示「日期属性 · 截止日」的静态说明 -->
      <span v-else-if="isCalendarView && calendarColumnName" class="db-date-label">
        <span class="dl-key">{{ $t('workbench.database.toolbar.dateColumn') }}</span>
        {{ calendarColumnName }}
      </span>

      <!-- 已生效的筛选：字段 + (等于/不等于 · 取值) + 删除 -->
      <span v-for="chip in chips" :key="chip.key" class="filter-chip">
        <span class="fc-key">{{ chip.label }}</span>
        <b>{{ chip.valueLabel }}</b>
        <button class="fc-x" type="button" :aria-label="chip.label" @click="removeFilter(chip.key)">
          ×
        </button>
      </span>

      <!-- ＋ 筛选：点开就是「字段 + 运算符 + 取值」的平铺菜单，选完立即变成 chip -->
      <div class="add-filter-wrap">
        <button class="add-filter" type="button" @click="toggleFilterPanel">
          <span class="plus">＋</span>{{ $t('workbench.database.toolbar.filter') }}
        </button>
        <div v-if="panelOpen && filterMenu.length" class="add-filter-panel is-scroll">
          <button
            v-for="item in filterMenu"
            :key="item.key + ':' + item.token"
            type="button"
            @click="pickFilter(item)"
          >
            <span>{{ item.label }}</span>
            <b>{{ item.valueLabel }}</b>
          </button>
        </div>
      </div>

      <span class="db-sep" />

      <!-- 排序：原型是「⇅ 截止日 ↑」实线按钮，点开选列与方向 -->
      <div v-if="sortOptions.length" class="sort-wrap">
        <button class="sort-btn" type="button" @click="toggleSortPanel">
          <span class="sort-ico">⇅</span>{{ sortLabel }}
        </button>
        <div v-if="sortOpen" class="add-filter-panel">
          <button
            v-for="option in sortOptions"
            :key="option.value"
            type="button"
            @click="pickSort(option.value)"
          >
            <span>{{ option.label }}</span>
          </button>
        </div>
      </div>

      <span class="spacer" />

      <t-input
        v-model="searchText"
        class="db-search"
        size="small"
        clearable
        :placeholder="$t('workbench.database.toolbar.searchPlaceholder')"
        @change="onSearchChange"
        @clear="onSearchChange"
      >
        <template #prefix>
          <t-icon name="search" size="13px" />
        </template>
      </t-input>

      <button class="db-new-row" type="button" @click="newRow">
        ＋ {{ $t('workbench.database.toolbar.newRow') }}
      </button>

      <!-- 事项页没有顶栏，原来顶栏「…」里的动作（导入资料 / CSV / AI 生成）挂到这里 -->
      <div v-if="actions.length" ref="moreWrap" class="db-more-wrap">
        <button
          class="db-more"
          type="button"
          :aria-label="$t('workbench.item.more')"
          :aria-expanded="moreOpen"
          @click="toggleMorePanel"
        >
          ⋯
        </button>
        <div v-if="moreOpen" class="add-filter-panel is-right">
          <button
            v-for="action in actions"
            :key="action.value"
            type="button"
            :disabled="action.disabled"
            @click="pickAction(action)"
          >
            <span>{{ action.label }}</span>
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup lang="ts">
/**
 * 数据库工具条：筛选 chip / 排序 / 搜索 / 新建行。
 *
 * 组件只负责渲染与 i18n 文案，动作全部转调 `DatabaseToolbarController`；
 * 筛选与排序保存在上游视图数据里，控件状态从 `state.activeFilters / activeSort` 回填。
 */
import { computed, onBeforeUnmount, onMounted, reactive, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { DatabaseToolbarController } from '@/workbench/editor/database-session'
import type { DatabaseFilterInput } from '@/workbench/editor/database-session'
import type { DatabaseToolbarState } from '@/workbench/editor/database-state'

const props = defineProps<{
  controller: DatabaseToolbarController | null
  state: DatabaseToolbarState
  /** 工具条右侧「⋯」菜单项；空数组时不渲染按钮（画布内数据库块走顶栏菜单） */
  actions?: Array<{ value: string; label: string; disabled?: boolean }>
}>()

const emit = defineEmits<{ action: [value: string] }>()

const { t } = useI18n()
const state = computed(() => props.state)
const actions = computed(() => props.actions ?? [])

/**
 * Teleport 宿主：上游工具行里的工具组。
 *
 * 上游切换视图 / 重建 renderer 时会换掉这个元素，因此挂载后持续检查是否仍然
 * 连着文档，断开就重新解析一次，避免工具条跟着旧节点一起消失。
 */
const mountTarget = shallowRef<HTMLElement | null>(null)
let targetTimer: number | undefined

function resolveTarget() {
  const element = document.querySelector<HTMLElement>('affine-database data-view-header-tools')
  if (element && element.isConnected) mountTarget.value = element
}

/**
 * 「⋯」菜单的关闭策略：点面板外任意位置收起。
 *
 * 面板挂在 Teleport 宿主里，不能用全局遮罩（会挡住表格点击），
 * 因此用捕获阶段的 pointerdown 判断点击是否落在 wrap 内。
 */
function onDocumentPointerDown(event: Event) {
  if (!moreOpen.value) return
  const target = event.target as Node | null
  if (target && moreWrap.value?.contains(target)) return
  moreOpen.value = false
}

onMounted(() => {
  resolveTarget()
  targetTimer = window.setInterval(() => {
    if (!mountTarget.value?.isConnected) resolveTarget()
  }, 800)
  document.addEventListener('pointerdown', onDocumentPointerDown, true)
})

onBeforeUnmount(() => {
  if (targetTimer) window.clearInterval(targetTimer)
  document.removeEventListener('pointerdown', onDocumentPointerDown, true)
})

/** 各筛选下拉当前选中的值；key 见 `applyFilters` 的编码。清空后该条件不参与筛选。 */
const selected = reactive<Record<string, string>>({})
const searchText = ref('')
const dateColumnValue = ref('')
const panelOpen = ref(false)
const sortOpen = ref(false)
/** 右侧「⋯」菜单（导入资料 / CSV / AI 生成）的展开状态 */
const moreOpen = ref(false)
const moreWrap = ref<HTMLElement | null>(null)

watch(
  () => state.value.calendarDateColumnId,
  (value) => {
    dateColumnValue.value = value
  },
  { immediate: true },
)

/** Calendar 视图且有多个日期属性时才需要选择器（§10.8.2 规则 1）。 */
const showDateColumn = computed(
  () => state.value.currentViewMode === 'calendar' && state.value.dateColumns.length > 1,
)

const isCalendarView = computed(() => state.value.currentViewMode === 'calendar')

/** 驱动日历的日期列名：优先当前绑定列，退到第一列。 */
const calendarColumnName = computed(() => {
  const columns = state.value.dateColumns
  const bound = columns.find((column) => column.id === state.value.calendarDateColumnId)
  return (bound ?? columns[0])?.name ?? ''
})

const dateColumnOptions = computed(() =>
  state.value.dateColumns.map((column) => ({ label: column.name, value: column.id })),
)

function onDateColumnChange(value: string | number | Array<string | number>) {
  const columnId = Array.isArray(value) ? String(value[0] ?? '') : String(value)
  if (columnId) props.controller?.setCalendarDateColumn(columnId)
}

/**
 * 筛选 chip 的取值编码。
 *
 *   - `<optionId>|eq`  select 列等值（上游 isOneOf）
 *   - `<optionId>|neq` select 列取反（上游 isNotOneOf，原型「状态 不等于 · 已完成」）
 *   - `month`         日期列「在本月」（落成 after + before 两条原生条件）
 */
const MONTH_TOKEN = 'month'

function operatorLabel(negate: boolean): string {
  return t(negate ? 'workbench.database.toolbar.operatorNotEquals' : 'workbench.database.toolbar.operatorEquals')
}

/** 已生效的筛选 chip（字段名 + 「不等于 · 已完成」）。 */
const chips = computed(() =>
  state.value.filters.flatMap((filter) => {
    const value = selected[filter.key]
    if (!value) return []
    if (value === MONTH_TOKEN) {
      return [
        { key: filter.key, label: filter.label, valueLabel: t('workbench.database.toolbar.thisMonth') },
      ]
    }
    const [optionId, mode] = value.split('|')
    const option = filter.options.find((item) => item.id === optionId)
    if (!option) return []
    return [
      {
        key: filter.key,
        label: filter.label,
        valueLabel: `${operatorLabel(mode === 'neq')} · ${option.value}`,
      },
    ]
  }),
)

/**
 * 「＋ 筛选」菜单：每个字段给出所有可表达的（运算符 + 取值）组合。
 *
 * select 列各列「等于 / 不等于」两档，日期列只有「在本月」。
 * 已生效的那一条不再重复列出，和原型「选中即变 chip」的交互一致。
 */
const filterMenu = computed(() =>
  state.value.filters.flatMap((filter) => {
    const items = filter.type === 'date'
      ? [{ token: MONTH_TOKEN, valueLabel: t('workbench.database.toolbar.thisMonth') }]
      : filter.options.flatMap((option) => [
          { token: `${option.id}|eq`, valueLabel: `${operatorLabel(false)} · ${option.value}` },
          { token: `${option.id}|neq`, valueLabel: `${operatorLabel(true)} · ${option.value}` },
        ])
    return items
      .filter((item) => selected[filter.key] !== item.token)
      .map((item) => ({ key: filter.key, label: filter.label, token: item.token, valueLabel: item.valueLabel }))
  }),
)

const sortOptions = computed(() =>
  state.value.sortColumns.flatMap((column) => [
    { label: `${column.label} ↑`, value: `${column.id}:asc` },
    { label: `${column.label} ↓`, value: `${column.id}:desc` },
  ]),
)

/** 排序按钮文案：「截止日 ↑ / 截止日 ↓」，未排序时展示首列的升序默认值。 */
const sortLabel = computed(() => {
  const active = state.value.activeSort
  const column = active && state.value.sortColumns.find((item) => item.id === active.columnId)
  if (column) return `${column.label} ${active.desc ? '↓' : '↑'}`
  const first = state.value.sortColumns[0]
  return first ? `${first.label} ↑` : t('workbench.database.toolbar.sort')
})

/**
 * 视图数据里的筛选回填到 chip。
 *
 * 条件保存在视图数据里（刷新后仍在），控件状态必须以它为准，
 * 否则会出现「表格已经过滤、筛选行却显示未选」的错位。
 */
watch(
  () => [state.value.filters, state.value.activeFilters] as const,
  () => {
    for (const key of Object.keys(selected)) delete selected[key]
    for (const filter of state.value.filters) {
      const active = state.value.activeFilters.find((item) => item.columnId === filter.columnId)
      if (!active) continue
      if (active.month) selected[filter.key] = MONTH_TOKEN
      else if (active.optionIds.length) {
        selected[filter.key] = `${active.optionIds[0]}|${active.negate ? 'neq' : 'eq'}`
      }
    }
  },
  { immediate: true, deep: true },
)

/** 换视图时收起面板并清空搜索：搜索串只存在于当前视图的内存里。 */
watch(
  () => state.value.currentViewMode,
  () => {
    searchText.value = ''
    panelOpen.value = false
    sortOpen.value = false
    moreOpen.value = false
  },
)

/** 把当前 chip 状态整体写回视图：每次变更都重建条件，避免残留已删掉的条件。 */
function applyFilters() {
  const inputs = state.value.filters.flatMap((filter): DatabaseFilterInput[] => {
    const value = selected[filter.key]
    if (!value) return []
    if (value === MONTH_TOKEN) return [{ columnId: filter.columnId, month: true }]
    const [optionId, mode] = value.split('|')
    if (!optionId) return []
    return [{ columnId: filter.columnId, optionIds: [optionId], negate: mode === 'neq' }]
  })
  props.controller?.setFilters(inputs)
}

function pickFilter(item: { key: string; token: string }) {
  selected[item.key] = item.token
  panelOpen.value = false
  applyFilters()
}

function removeFilter(key: string) {
  delete selected[key]
  applyFilters()
}

function onSearchChange() {
  props.controller?.setSearch(searchText.value)
}

/** 两个下拉面板互斥，避免同时展开叠在一起。 */
function toggleFilterPanel() {
  sortOpen.value = false
  moreOpen.value = false
  panelOpen.value = !panelOpen.value
}

function toggleSortPanel() {
  panelOpen.value = false
  moreOpen.value = false
  sortOpen.value = !sortOpen.value
}

/** 「⋯」与筛选/排序面板互斥，避免三个浮层叠在一起。 */
function toggleMorePanel() {
  panelOpen.value = false
  sortOpen.value = false
  moreOpen.value = !moreOpen.value
}

/** 菜单项只上抛动作名，具体行为由条目页统一分派（导入资料 / CSV / AI）。 */
function pickAction(action: { value: string; disabled?: boolean }) {
  if (action.disabled) return
  moreOpen.value = false
  emit('action', action.value)
}

function pickSort(value: string) {
  const [columnId, direction] = value.split(':')
  sortOpen.value = false
  props.controller?.setSort(columnId ?? '', direction === 'desc')
}

function newRow() {
  props.controller?.newRow()
}
</script>

<style lang="less" scoped>
/* 原型：.db-toolbar{display:flex;gap:6px;padding:7px 0;border-bottom:1px solid #e7e7e7;flex-wrap:wrap} */
.db-toolbar {
  display: flex;
  /* 排在「视图 Tab + 设置」之后，独占第二行（原型就是 Tab 行 + 筛选行两行）。 */
  order: 10;
  flex: 1 1 100%;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  width: 100%;
  padding: 7px 0;
  border-bottom: 1px solid var(--td-component-stroke);
}

/* 原型 .filter-chip */
.filter-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 24px;
  padding: 0 6px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 4px;
  background: var(--td-bg-color-container);
  font-size: 11px;
  color: var(--td-text-color-secondary);
}

.filter-chip .fc-key {
  color: var(--td-text-color-placeholder);
}

.filter-chip b {
  font-weight: 500;
  color: var(--td-text-color-primary);
}

.fc-x {
  margin-left: 2px;
  padding: 0;
  border: 0;
  background: none;
  color: #bbb;
  font-size: 12px;
  line-height: 1;
  cursor: pointer;
}

.fc-x:hover {
  color: var(--td-text-color-secondary);
}

/* 原型 .add-filter */
.add-filter-wrap {
  position: relative;
}

.add-filter {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 24px;
  padding: 0 6px;
  border: 1px dashed var(--td-bg-color-component);
  border-radius: 4px;
  background: transparent;
  color: var(--td-text-color-placeholder);
  font-size: 11px;
  cursor: pointer;
}

.add-filter:hover {
  color: var(--td-text-color-primary);
  border-color: var(--td-text-color-placeholder);
}

.add-filter-panel {
  position: absolute;
  top: 28px;
  left: 0;
  z-index: 20;
  min-width: 168px;
  padding: 4px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  background: var(--td-bg-color-container);
  box-shadow: var(--td-shadow-2);
}

.add-filter-panel button {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  width: 100%;
  padding: 5px 8px;
  border: 0;
  border-radius: 4px;
  background: none;
  color: var(--td-text-color-secondary);
  font-size: 12px;
  cursor: pointer;
}

.add-filter-panel button:hover {
  background: var(--td-bg-color-container-hover);
  color: var(--td-text-color-primary);
}

.db-sep {
  width: 1px;
  height: 14px;
  margin: 0 3px;
  background: var(--td-component-stroke);
}

/* Calendar 视图的层级说明：静态文字，不做成 chip，避免和筛选条件混淆 */
.db-cal-hint,
.db-date-label {
  font-size: 11px;
  color: var(--td-text-color-placeholder);
  white-space: nowrap;
}

.db-date-label .dl-key {
  margin-right: 4px;
  color: var(--td-text-color-placeholder);
}

.db-date-label .dl-key::after {
  content: ':';
  margin-left: 1px;
}

.db-cal-hint::before {
  content: '';
  display: inline-block;
  width: 5px;
  height: 5px;
  margin-right: 5px;
  border-radius: 50%;
  background: var(--td-brand-color);
  vertical-align: middle;
}

.spacer {
  flex: 1;
}

/* 原型排序按钮：实线描边 + 图标 + 「截止日 ↑」 */
.sort-wrap {
  position: relative;
}

.sort-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 24px;
  padding: 0 6px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 4px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-secondary);
  font-size: 11px;
  cursor: pointer;
}

.sort-btn:hover {
  color: var(--td-text-color-primary);
}

.sort-ico {
  color: var(--td-text-color-placeholder);
  font-size: 11px;
}

.db-search {
  width: 150px;
  height: 26px;
}

.db-date-select {
  width: 140px;
  height: 26px;
}

.db-search :deep(.t-input) {
  height: 26px;
  border-radius: 4px;
  font-size: 11px;
}

/* 原型 .btn.is-sm.is-primary：26px 高、12px 字号、品牌色实心 */
.db-new-row {
  height: 26px;
  padding: 0 9px;
  border: 0;
  border-radius: 6px;
  background: var(--td-brand-color);
  color: #fff;
  font-size: 12px;
  font-weight: 500;
  cursor: pointer;
}

.db-new-row:hover {
  background: var(--td-brand-color-hover);
}

/* 原顶栏「…」的落点：与「新建行」同高的方按钮，沿用上面的浮层样式 */
.db-more-wrap {
  position: relative;
}

.db-more {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  padding: 0;
  border: 1px solid var(--td-component-stroke);
  border-radius: 6px;
  background: var(--td-bg-color-container);
  color: var(--td-text-color-secondary);
  font-size: 14px;
  line-height: 1;
  cursor: pointer;
}

.db-more:hover {
  color: var(--td-text-color-primary);
}

/* 靠右对齐：按钮在工具条最右端，面板若左对齐会溢出视口 */
.add-filter-panel.is-right {
  left: auto;
  right: 0;
}

/* 筛选菜单有「字段 × 运算符 × 取值」十几项，限高滚动，避免超出视口 */
.add-filter-panel.is-scroll {
  max-height: 260px;
  min-width: 208px;
  overflow-y: auto;
}

.add-filter-panel button:disabled {
  opacity: 0.45;
  cursor: default;
}

.add-filter-panel button:disabled:hover {
  background: none;
  color: var(--td-text-color-secondary);
}
</style>
