<template>
  <div ref="wrapEl" class="sop-msel">
    <button type="button" class="sop-msel-trigger" @click="toggle">
      <span class="sop-msel-summary">{{ summary }}</span>
      <span class="car">⌄</span>
    </button>

    <div v-if="open" class="sop-msel-pop">
      <label v-for="option in options" :key="option.value" class="sop-msel-row">
        <input
          type="checkbox"
          :checked="isChecked(option.value)"
          @change="toggleOption(option.value, ($event.target as HTMLInputElement).checked)"
        />
        <span>{{ option.label }}</span>
      </label>
      <div v-if="!options.length" class="sop-msel-empty">{{ $t('sop.items.execNoMatch') }}</div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';

const props = defineProps<{
  options: { value: string; label: string }[];
  /** null 表示「全部」（skills 语义）；数组表示显式勾选。 */
  modelValue: string[] | null;
  allLabel: string;
  placeholder: string;
}>();

const emit = defineEmits<{ 'update:modelValue': [string[] | null] }>();

const { t } = useI18n();
const wrapEl = ref<HTMLElement | null>(null);
const open = ref(false);

const summary = computed(() => {
  if (props.modelValue === null) return props.allLabel;
  if (!props.modelValue.length) return props.placeholder;
  const labels = props.modelValue
    .map((value) => props.options.find((option) => option.value === value)?.label || value)
    .slice(0, 3);
  const rest = props.modelValue.length - labels.length;
  return rest > 0 ? `${labels.join('、')} 等 ${props.modelValue.length} 项` : labels.join('、');
});

function isChecked(value: string): boolean {
  return props.modelValue === null || props.modelValue.includes(value);
}

function toggle() {
  if (open.value) {
    open.value = false;
    return;
  }
  // 开一个关其余：其它组件实例收到 mousedown 后自行收起
  document.dispatchEvent(new CustomEvent('sop-msel-open', { detail: wrapEl.value }));
  open.value = true;
}

function toggleOption(value: string, checked: boolean) {
  // 全选态首次取消勾选：展开为「除该项外的全部」
  const base = props.modelValue === null ? props.options.map((option) => option.value) : [...props.modelValue];
  let next = checked ? [...new Set([...base, value])] : base.filter((item) => item !== value);
  if (next.length === props.options.length) {
    emit('update:modelValue', props.modelValue === null ? null : next);
    return;
  }
  if (!next.length) {
    emit('update:modelValue', []);
    return;
  }
  emit('update:modelValue', next);
}

function onDocumentDown(event: MouseEvent) {
  if (!open.value) return;
  if (wrapEl.value?.contains(event.target as Node)) return;
  open.value = false;
}

function onOtherOpen(event: Event) {
  if ((event as CustomEvent).detail !== wrapEl.value) open.value = false;
}

watch(open, (value) => {
  if (value) {
    document.addEventListener('mousedown', onDocumentDown);
    document.addEventListener('sop-msel-open', onOtherOpen);
  } else {
    document.removeEventListener('mousedown', onDocumentDown);
    document.removeEventListener('sop-msel-open', onOtherOpen);
  }
});

onBeforeUnmount(() => {
  document.removeEventListener('mousedown', onDocumentDown);
  document.removeEventListener('sop-msel-open', onOtherOpen);
});

void t;
</script>

<style lang="less" scoped>
.sop-msel {
  position: relative;
}

.sop-msel-trigger {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 100%;
  padding: 5px 10px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-sm);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  font-size: var(--app-text-sm);
  text-align: left;
  cursor: pointer;

  .car {
    margin-left: auto;
    color: var(--td-text-color-placeholder);
  }

  &:hover {
    border-color: var(--td-brand-color);
  }
}

.sop-msel-summary {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sop-msel-pop {
  position: absolute;
  z-index: 30;
  top: calc(100% + 4px);
  left: 0;
  right: 0;
  max-height: 220px;
  overflow: auto;
  padding: 6px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-container);
  box-shadow: var(--td-shadow-2);
}

.sop-msel-row {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 5px 6px;
  border-radius: var(--app-radius-sm);
  font-size: var(--app-text-sm);
  cursor: pointer;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }
}

.sop-msel-empty {
  padding: 8px;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
  text-align: center;
}
</style>
