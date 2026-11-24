<template>
  <!-- 执行情况下拉：触发器 + 浮层（搜索 / 选项带计数 / Enter 选首个 / Esc 与点外部收起）-->
  <div ref="wrapEl" class="sop-fsel">
    <button type="button" class="sop-fsel-btn" :class="{ on: !!modelValue }" @click="toggle">
      <span>{{ currentLabel }}</span>
      <span class="car">⌄</span>
    </button>

    <div v-if="open" ref="popEl" class="sop-fsel-pop" :style="popStyle">
      <div class="sop-fsel-search">
        <span>⌕</span>
        <input
          ref="inputEl"
          v-model="keyword"
          type="text"
          :placeholder="$t('sop.items.execSearch')"
          autocomplete="off"
          @keydown.enter.prevent="pickFirst"
          @keydown.esc.prevent="close"
        />
      </div>
      <div class="sop-fsel-list">
        <button
          v-for="option in filtered"
          :key="option.value || 'all'"
          type="button"
          class="sop-fsel-opt"
          :class="{ on: option.value === modelValue }"
          @click="pick(option.value)"
        >
          {{ option.label }}
          <span class="cnt">{{ option.count }}</span>
        </button>
        <div v-if="!filtered.length" class="sop-fsel-empty">{{ $t('sop.items.execNoMatch') }}</div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { EXEC_STATE_LABELS, matchExecOptions, type ExecOption } from '@/api/sop/helpers';
import type { ExecState } from '@/api/sop';

const props = defineProps<{
  modelValue: ExecState | '';
  counts: Record<string, number>;
  total: number;
}>();

const emit = defineEmits<{ 'update:modelValue': [ExecState | ''] }>();

const { t } = useI18n();
const wrapEl = ref<HTMLElement | null>(null);
const inputEl = ref<HTMLInputElement | null>(null);
const open = ref(false);
const keyword = ref('');
const popStyle = ref<Record<string, string>>({});

const options = computed<ExecOption[]>(() => [
  { value: '', label: t('sop.items.execAll'), count: props.total },
  ...(Object.keys(EXEC_STATE_LABELS) as ExecState[]).map((state) => ({
    value: state,
    label: EXEC_STATE_LABELS[state],
    count: props.counts[state] || 0,
  })),
]);

const filtered = computed(() => matchExecOptions(options.value, keyword.value));

const currentLabel = computed(() => {
  const hit = options.value.find((option) => option.value === props.modelValue);
  return hit ? hit.label : t('sop.items.execAll');
});

async function toggle() {
  if (open.value) {
    close();
    return;
  }
  open.value = true;
  keyword.value = '';
  // 浮层贴在触发器下方，超出视口时上移（简单钳位，避免被裁切）
  await nextTick();
  const rect = wrapEl.value?.getBoundingClientRect();
  if (!rect) return;
  const spaceBelow = window.innerHeight - rect.bottom;
  popStyle.value = spaceBelow < 260
    ? { bottom: `${rect.height + 6}px`, right: '0' }
    : { top: `${rect.height + 6}px`, right: '0' };
  inputEl.value?.focus();
}

function close() {
  open.value = false;
  keyword.value = '';
}

function pick(value: ExecState | '') {
  emit('update:modelValue', value);
  close();
}

function pickFirst() {
  const first = filtered.value[0];
  if (first) pick(first.value);
}

function onDocumentDown(event: MouseEvent) {
  if (!open.value) return;
  if (wrapEl.value?.contains(event.target as Node)) return;
  close();
}

watch(open, (value) => {
  if (value) document.addEventListener('mousedown', onDocumentDown);
  else document.removeEventListener('mousedown', onDocumentDown);
});

onBeforeUnmount(() => document.removeEventListener('mousedown', onDocumentDown));
</script>

<style lang="less" scoped src="./sopExecFilter.less"></style>
