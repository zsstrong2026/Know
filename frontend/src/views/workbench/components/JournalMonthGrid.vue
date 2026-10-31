<template>
  <!-- 6×7 月格：纯布局组件，数据由父级投影好传进来（§9.13.2） -->
  <div class="month-grid" role="grid" :aria-label="$t('workbench.journals.monthGrid')">
    <div class="mg-week" role="row">
      <span v-for="label in weekLabels" :key="label" role="columnheader">{{ label }}</span>
    </div>
    <div class="mg-body" role="rowgroup">
      <button
        v-for="cell in cells"
        :key="cell.date"
        class="mg-cell"
        :class="{
          'is-dim': !cell.inMonth,
          'is-selected': cell.date === selectedDate,
          'is-today': cell.date === today,
          'is-drop': cell.date === dropTarget,
        }"
        type="button"
        role="gridcell"
        :aria-pressed="cell.date === selectedDate"
        :aria-current="cell.date === today ? 'date' : undefined"
        :aria-label="cell.ariaLabel"
        :title="cell.ariaLabel"
        :data-journal-date="cell.date"
        @click="emit('select', cell.date)"
        @dragover.prevent="onDragOver(cell.date)"
        @dragleave="onDragLeave(cell.date)"
        @drop.prevent="onDrop(cell.date)"
      >
        <span class="mg-num">{{ cell.day }}</span>
        <span v-if="cell.dots.length" class="mg-dots" aria-hidden="true">
          <i v-for="(dot, index) in cell.dots" :key="index" :class="`is-${dot}`" />
          <em v-if="cell.extra > 0" class="mg-more">+{{ cell.extra }}</em>
        </span>
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * Journals 月格。
 *
 * 只做渲染与拖放事件上抛：日期投影、状态点排序、aria 文案都在
 * workbench/journals/month-cells.ts 里，便于单测与复用。
 */
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { monthGrid, todayISO } from '@/workbench/journals/date-only'
import { dotLayout, groupByDate, statusCounts } from '@/workbench/journals/month-cells'
import type { ArchiveStatus, WorkbenchJournalEntry } from '@/types/workbench'

const props = defineProps<{
  month: string
  selectedDate: string
  entries: WorkbenchJournalEntry[]
  /** 正在被拖拽的 Journal id；由父级在 dragstart / dragend 维护 */
  draggingId?: string
}>()

const emit = defineEmits<{
  select: [date: string]
  /** 拖拽改期：id 拖到 date（§9.13.5） */
  move: [payload: { id: string; date: string }]
}>()

const { t, locale } = useI18n()
const today = todayISO()
const dropTarget = ref('')

const WEEK_LABELS = ['sun', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat'] as const
const weekLabels = computed(() => WEEK_LABELS.map((key) => t(`workbench.journals.weekday.${key}`)))

const grouped = computed(() => groupByDate(props.entries))

/** 每格 aria-label 必须读出日期、条数与各状态计数（§9.13.2）。 */
function ariaLabel(date: string, rows: WorkbenchJournalEntry[]): string {
  const head = t('workbench.journals.dateLabel', {
    month: Number(date.slice(5, 7)),
    day: Number(date.slice(8, 10)),
  })
  if (rows.length === 0) return head
  const counts = statusCounts(rows)
  const parts = (Object.entries(counts) as Array<[ArchiveStatus, number]>)
    .filter(([status, count]) => count > 0 && status !== 'source_deleted')
    .map(([status, count]) => `${t(`workbench.archive.status.${status}`)} ${count}`)
  return `${head} · ${t('workbench.journals.itemCount', { count: rows.length })}: ${parts.join(', ')}`
}

const cells = computed(() => {
  // 显式读取 locale：t() 不是响应式依赖，切换语言要重算 aria 文案
  void locale.value
  return monthGrid(props.month).map((cell) => {
    const rows = grouped.value.get(cell.date) ?? []
    const layout = dotLayout(rows, 3)
    return {
      ...cell,
      day: Number(cell.date.slice(8, 10)),
      dots: layout.dots,
      extra: layout.extra,
      ariaLabel: ariaLabel(cell.date, rows),
    }
  })
})

function onDragOver(date: string) {
  dropTarget.value = date
}

function onDragLeave(date: string) {
  if (dropTarget.value === date) dropTarget.value = ''
}

function onDrop(date: string) {
  dropTarget.value = ''
  const id = props.draggingId
  if (id) emit('move', { id, date })
}
</script>

<style lang="less" scoped>
.month-grid {
  padding: 0 var(--app-space-3);
}

.mg-week {
  display: grid;
  grid-template-columns: repeat(7, 1fr);
  padding-bottom: var(--app-space-1);
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
  text-align: center;
}

.mg-body {
  display: grid;
  grid-template-columns: repeat(7, 1fr);
  gap: 3px;
}

.mg-cell {
  position: relative;
  height: 34px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  border-radius: var(--app-radius-sm);
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &.is-dim {
    color: var(--td-text-color-disabled);
  }

  &.is-today .mg-num {
    color: var(--td-brand-color);
    font-weight: 700;
  }

  &.is-selected {
    background: var(--td-brand-color);
    color: var(--td-text-color-anti);

    .mg-num {
      color: var(--td-text-color-anti);
    }
  }

  &.is-drop {
    box-shadow: inset 0 0 0 2px var(--td-brand-color);
  }
}

.mg-num {
  line-height: 1;
  font-variant-numeric: tabular-nums;
}

.mg-dots {
  position: absolute;
  bottom: 3px;
  left: 50%;
  display: flex;
  align-items: center;
  gap: 2px;
  transform: translateX(-50%);

  i {
    width: 4px;
    height: 4px;
    flex: none;
    border-radius: var(--td-radius-circle);
  }
}

/* 深色选中态：保留状态色，仅加白色外环保证对比度——颜色仍是唯一可辨信号之外的辅助 */
.mg-cell.is-selected .mg-dots i {
  box-shadow: 0 0 0 1px var(--td-text-color-anti);
}

.mg-more {
  font-size: var(--app-text-2xs);
  font-style: normal;
  line-height: 1;
  color: var(--td-text-color-placeholder);
}

.is-active { background: var(--td-success-color); }
.is-processing { background: var(--td-brand-color); }
.is-stale { background: var(--td-warning-color); }
.is-partial { background: var(--td-error-color); }
.is-none { background: var(--td-text-color-disabled); }
.is-deleted { background: var(--td-text-color-placeholder); opacity: 0.55; }
</style>
