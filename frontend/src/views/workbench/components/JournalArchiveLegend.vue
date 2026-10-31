<template>
  <!-- 归档状态图例：色点 + 文字，颜色只做辅助（§9.13.2） -->
  <div class="legend" :aria-label="$t('workbench.journals.legendTitle')">
    <span v-for="status in statuses" :key="status">
      <i :class="`is-${status}`" aria-hidden="true" />
      {{ $t(`workbench.archive.status.${status}`) }}
    </span>
  </div>
</template>

<script setup lang="ts">
/**
 * 六项图例：source_deleted 不出现在 Journals 接口里，所以不列。
 * 顺序与月格点位的严重程度一致，方便对照。
 */
import { DOT_STATUS_ORDER } from '@/workbench/journals/month-cells'

const statuses = [...DOT_STATUS_ORDER].reverse()
</script>

<style lang="less" scoped>
.legend {
  display: flex;
  flex-wrap: wrap;
  gap: 3px 7px;
  padding: 3px var(--app-space-3) var(--app-space-2);
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);

  span {
    display: inline-flex;
    align-items: center;
    gap: 3px;
    white-space: nowrap;
  }

  i {
    width: 4px;
    height: 4px;
    flex: none;
    border-radius: var(--td-radius-circle);
  }
}

.is-active { background: var(--td-success-color); }
.is-processing { background: var(--td-brand-color); }
.is-stale { background: var(--td-warning-color); }
.is-partial { background: var(--td-error-color); }
.is-none { background: var(--td-text-color-disabled); }
.is-deleted { background: var(--td-text-color-placeholder); opacity: 0.55; }
</style>
