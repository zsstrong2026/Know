<template>
  <!-- 模板卡片：缩略图是纯 CSS 结构示意，不创建编辑器实例（§9.16.5） -->
  <button
    type="button"
    class="tpl-card"
    :class="{ 'is-applying': applying, 'is-failed': failed }"
    :disabled="disabled || applying"
    :aria-label="computedName"
    @click="emit('apply')"
  >
    <span class="tpl-thumb" :class="`is-${thumbVariant}`" aria-hidden="true">
      <span class="tpl-thumb-line is-title" />
      <span class="tpl-thumb-line" />
      <span class="tpl-thumb-line is-short" />
      <span class="tpl-thumb-line" />
    </span>
    <span class="tpl-name">{{ computedName }}</span>
    <span v-if="computedDesc" class="tpl-desc">{{ computedDesc }}</span>
    <span class="tpl-foot">
      <template v-if="applying">{{ t('workbench.templates.applying') }}</template>
      <template v-else-if="failed">{{ t('workbench.templates.loadFailed') }}</template>
      <template v-else>{{ t('workbench.templates.use') }}</template>
    </span>
  </button>
</template>

<script setup lang="ts">
/**
 * 单张模板卡片。
 *
 * 只负责展示与点击；取快照与套用由选择器统一处理，
 * 避免每张卡片各自 import 上游包。
 */
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { TemplateCardItem } from '@/workbench/templates/picker'

const props = defineProps<{
  card: TemplateCardItem
  applying?: boolean
  failed?: boolean
  disabled?: boolean
}>()

const emit = defineEmits<{ apply: [] }>()

const { t } = useI18n()

/** 内置模板走 i18n，我的模板直接用用户填的名称。 */
const computedName = computed(() =>
  props.card.nameKey ? t(props.card.nameKey) : props.card.name,
)
const computedDesc = computed(() =>
  props.card.descriptionKey ? t(props.card.descriptionKey) : (props.card.description ?? ''),
)

/** 缩略图按分类换个细微差异，让网格不至于完全一样。 */
const thumbVariant = computed(() => props.card.category ?? 'mine')
</script>

<style lang="less" scoped>
.tpl-card {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--app-space-1);
  min-width: 180px;
  padding: var(--app-space-2);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-container);
  text-align: left;

  &:hover:not(:disabled) {
    border-color: var(--td-brand-color-3);
  }

  &.is-applying {
    border-color: var(--td-brand-color);
    background: var(--td-brand-color-1);
  }

  &.is-failed {
    border-color: var(--td-error-color);
  }
}

.tpl-thumb {
  display: flex;
  flex-direction: column;
  gap: 4px;
  width: 100%;
  height: 92px;
  padding: var(--app-space-2);
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-secondarycontainer);
  overflow: hidden;

  &.is-work {
    background: linear-gradient(180deg, var(--td-brand-color-1), var(--td-bg-color-secondarycontainer));
  }

  &.is-record {
    background: linear-gradient(180deg, var(--td-warning-color-1), var(--td-bg-color-secondarycontainer));
  }

  &.is-writing {
    background: linear-gradient(180deg, var(--td-success-color-1), var(--td-bg-color-secondarycontainer));
  }
}

.tpl-thumb-line {
  height: 5px;
  border-radius: var(--app-radius-xs);
  background: var(--td-bg-color-container);
  opacity: 0.9;

  &.is-title {
    width: 62%;
    height: 8px;
  }

  &.is-short {
    width: 74%;
  }
}

.tpl-name {
  font-size: var(--app-text-sm);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.tpl-desc {
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.tpl-foot {
  margin-top: auto;
  font-size: var(--app-text-2xs);
  color: var(--td-brand-color);
}
</style>
