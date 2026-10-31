<template>
  <!-- 七日条：日期选择器，不是周视图（§9.13.2） -->
  <nav class="date-strip" :aria-label="$t('workbench.journals.dateNav')">
    <button class="strip-arrow" type="button" :aria-label="$t('workbench.journals.prevWeek')" @click="emit('step', -7)">
      <t-icon name="chevron-left" size="15px" />
    </button>
    <div class="strip-days">
      <button
        v-for="cell in cells"
        :key="cell.date"
        class="strip-day"
        :class="{ 'is-selected': cell.date === selectedDate, 'is-today': cell.date === today }"
        type="button"
        :aria-pressed="cell.date === selectedDate"
        :aria-current="cell.date === today ? 'date' : undefined"
        :aria-label="cell.ariaLabel"
        :title="cell.ariaLabel"
        @click="emit('select', cell.date)"
      >
        <span class="sd-week">{{ cell.weekLabel }}</span>
        <b class="sd-num">{{ cell.day }}</b>
        <span v-if="cell.dots.length" class="sd-dots" aria-hidden="true">
          <i v-for="(dot, index) in cell.dots" :key="index" :class="`is-${dot}`" />
        </span>
      </button>
    </div>
    <button class="strip-arrow" type="button" :aria-label="$t('workbench.journals.nextWeek')" @click="emit('step', 7)">
      <t-icon name="chevron-right" size="15px" />
    </button>
    <span class="strip-divider" aria-hidden="true" />
    <button class="strip-today" type="button" @click="emit('select', today)">
      {{ $t('workbench.journals.today') }}
    </button>
  </nav>
</template>

<script setup lang="ts">
/**
 * Journals 顶部七日导航。
 *
 * 只消费 YYYY-MM-DD，日期运算全部在 date-only.ts 里完成；
 * 状态点最多 2 个，取当天最严重的两个，其余在 aria/tooltip 汇总。
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { todayISO, weekDays } from '@/workbench/journals/date-only'
import { dotLayout, groupByDate, statusCounts } from '@/workbench/journals/month-cells'
import type { ArchiveStatus, WorkbenchJournalEntry } from '@/types/workbench'

const props = defineProps<{
  selectedDate: string
  entries: WorkbenchJournalEntry[]
}>()

const emit = defineEmits<{
  select: [date: string]
  /** ±7 天；保留星期位置 */
  step: [delta: number]
}>()

const { t, locale } = useI18n()
const today = todayISO()

const WEEK_LABELS = ['sun', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat'] as const

const grouped = computed(() => groupByDate(props.entries))

/** 星期文案按当前语言渲染，避免硬编码中文。 */
function weekdayLabel(date: string): string {
  const index = new Date(`${date}T00:00:00Z`).getUTCDay()
  return t(`workbench.journals.weekday.${WEEK_LABELS[index]}`)
}

/** aria-label 必须同时给出日期、条数与各状态计数，不能只靠颜色（§9.13.2）。 */
function ariaLabel(date: string, rows: WorkbenchJournalEntry[]): string {
  const head = `${t('workbench.journals.dateLabel', {
    month: Number(date.slice(5, 7)),
    day: Number(date.slice(8, 10)),
  })} · ${weekdayLabel(date)}`
  if (rows.length === 0) return head
  const counts = statusCounts(rows)
  const parts = (Object.entries(counts) as Array<[ArchiveStatus, number]>)
    .filter(([status, count]) => count > 0 && status !== 'source_deleted')
    .map(([status, count]) => `${t(`workbench.archive.status.${status}`)} ${count}`)
  return `${head} · ${t('workbench.journals.itemCount', { count: rows.length })}: ${parts.join(', ')}`
}

const cells = computed(() => {
  // 显式读取 locale：t() 不是响应式依赖，切换语言时必须重算星期与 aria 文案
  void locale.value
  return weekDays(props.selectedDate).map((date) => {
    const rows = grouped.value.get(date) ?? []
    const layout = dotLayout(rows, 2)
    return {
      date,
      day: Number(date.slice(8, 10)),
      weekLabel: weekdayLabel(date),
      dots: layout.dots,
      ariaLabel: ariaLabel(date, rows),
    }
  })
})
</script>

<style lang="less" scoped>
.date-strip {
  height: 66px;
  flex: none;
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  padding: var(--app-space-2) var(--app-space-5);
  background: var(--td-bg-color-container);
  border-bottom: 1px solid var(--td-component-stroke);
}

.strip-days {
  flex: 1;
  min-width: 0;
  display: grid;
  grid-template-columns: repeat(7, minmax(0, 1fr));
  gap: var(--app-space-1);
}

.strip-day {
  position: relative;
  height: 48px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 3px;
  border-radius: var(--app-radius-sm);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-2xs);

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &.is-today {
    color: var(--td-brand-color);
  }

  &.is-selected {
    background: var(--td-brand-color);
    color: var(--td-text-color-anti);

    .sd-num {
      color: var(--td-text-color-anti);
    }
  }
}

.sd-num {
  font-size: var(--app-text-lg);
  font-weight: 600;
  font-variant-numeric: tabular-nums;
  line-height: 1;
  color: var(--td-text-color-primary);
}

.sd-dots {
  position: absolute;
  bottom: 3px;
  left: 50%;
  display: flex;
  gap: 2px;
  transform: translateX(-50%);

  i {
    width: 3px;
    height: 3px;
    border-radius: var(--td-radius-circle);
  }
}

/* 选中态保留状态色，只加白色外环保证在深色底上的对比度 */
.strip-day.is-selected .sd-dots i {
  box-shadow: 0 0 0 1px var(--td-text-color-anti);
}

.sd-dots i.is-active { background: var(--td-success-color); }
.sd-dots i.is-processing { background: var(--td-brand-color); }
.sd-dots i.is-stale { background: var(--td-warning-color); }
.sd-dots i.is-partial { background: var(--td-error-color); }
.sd-dots i.is-none { background: var(--td-text-color-disabled); }
.sd-dots i.is-deleted { background: var(--td-text-color-placeholder); opacity: 0.55; }

.strip-arrow {
  width: 24px;
  height: 38px;
  flex: none;
  display: grid;
  place-items: center;
  border-radius: var(--app-radius-xs);
  color: var(--td-text-color-placeholder);

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }
}

.strip-divider {
  width: 1px;
  height: 26px;
  background: var(--td-component-stroke);
}

.strip-today {
  flex: none;
  padding: var(--app-space-1) var(--app-space-2);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-xs);
  font-size: var(--app-text-xs);
  color: var(--td-text-color-secondary);
  white-space: nowrap;

  &:hover {
    color: var(--td-text-color-primary);
  }
}
</style>
