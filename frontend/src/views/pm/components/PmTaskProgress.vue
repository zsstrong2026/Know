<template>
  <!-- 异步任务进度：按固定间隔轮询，任务到达终态后停止 -->
  <div v-if="props.taskId" class="pm-task-progress">
    <div class="ptp-head">
      <span class="ptp-title">{{ $t('pm.task.title') }}</span>
      <t-tag size="small" variant="light" :theme="statusTheme">{{ statusLabel }}</t-tag>
    </div>
    <t-progress :percentage="percentage" :status="progressStatus" :label="false" />
    <div v-if="errorMessage" class="ptp-error">{{ errorMessage }}</div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { getPmTask, type PmTask } from '@/api/pm';

const props = withDefaults(
  defineProps<{ taskId?: string; intervalMs?: number }>(),
  { taskId: '', intervalMs: 3000 },
);
const emit = defineEmits<{
  done: [task: PmTask];
  failed: [task: PmTask];
}>();

const { t } = useI18n();
const task = ref<PmTask | null>(null);
let timer: ReturnType<typeof setInterval> | null = null;

const percentage = computed(() => Math.min(100, Math.max(0, task.value?.progress ?? 0)));
const statusLabel = computed(() => {
  const status = task.value?.status ?? 'pending';
  return t(`pm.task.status.${status}`);
});
// 失败时进度条转为错误态，其余保持默认样式
const progressStatus = computed(() => (task.value?.status === 'failed' ? 'error' : undefined));
const statusTheme = computed(() => {
  switch (task.value?.status) {
    case 'succeeded':
      return 'success';
    case 'failed':
      return 'danger';
    case 'running':
      return 'primary';
    default:
      return 'default';
  }
});
const errorMessage = computed(() =>
  task.value?.status === 'failed' ? task.value.error_message : '',
);

function stop() {
  if (timer) {
    clearInterval(timer);
    timer = null;
  }
}

async function poll() {
  const taskId = props.taskId;
  if (!taskId) return;
  try {
    const latest = await getPmTask(taskId);
    // 请求期间任务被切换时丢弃过期响应
    if (taskId !== props.taskId) return;
    task.value = latest;
    if (latest.status === 'succeeded') {
      stop();
      emit('done', latest);
    } else if (latest.status === 'failed') {
      stop();
      emit('failed', latest);
    }
  } catch {
    // 单次请求失败不终止轮询，下一轮自动重试
  }
}

// 任务 ID 变更时重置并立即拉取一次；先启动定时器，避免首轮即终态时残留轮询
watch(
  () => props.taskId,
  (taskId) => {
    stop();
    task.value = null;
    if (!taskId) return;
    void poll();
    timer = setInterval(() => void poll(), props.intervalMs);
  },
  { immediate: true },
);

onBeforeUnmount(stop);
</script>

<style lang="less" scoped>
.pm-task-progress {
  padding: var(--app-space-3) var(--app-space-4);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
  background: var(--td-bg-color-container);
}

.ptp-head {
  margin-bottom: var(--app-space-2);
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--app-space-2);
}

.ptp-title {
  font-size: var(--app-text-md);
  font-weight: 500;
  color: var(--td-text-color-primary);
}

.ptp-error {
  margin-top: var(--app-space-2);
  font-size: var(--app-text-sm);
  color: var(--td-error-color);
}
</style>
