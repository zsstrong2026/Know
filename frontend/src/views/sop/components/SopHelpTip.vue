<template>
  <button
    type="button"
    class="sop-help-tip"
    :aria-label="text"
    @mouseenter="onShow"
    @focus="onShow"
    @mouseleave="hideTip"
    @blur="hideTip"
  >
    ?
  </button>
</template>

<script setup lang="ts">
import { ref } from 'vue';
import { hideTip, showTip } from './help-tip';

const props = defineProps<{ text: string }>();
const el = ref<HTMLElement | null>(null);

function onShow(event: Event) {
  const target = (event.currentTarget as HTMLElement) || el.value;
  if (target) showTip(target, props.text);
}
</script>

<style lang="less" scoped>
.sop-help-tip {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 15px;
  height: 15px;
  padding: 0;
  border: 1px solid var(--td-text-color-placeholder);
  border-radius: var(--app-radius-pill);
  background: transparent;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-2xs);
  line-height: 1;
  cursor: help;

  &:hover,
  &:focus-visible {
    border-color: var(--td-brand-color);
    color: var(--td-brand-color);
    outline: none;
  }
}
</style>
