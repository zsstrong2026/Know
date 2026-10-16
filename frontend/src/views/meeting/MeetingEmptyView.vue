<template>
  <!-- 右侧空态：未选中会议时展示引导；列表非空时自动进入第一个会议 -->
  <div class="room-empty">
    <t-icon name="chat" :size="40" class="re-icon" />
    <div class="re-title">{{ $t('meeting.selectRoom') }}</div>
    <div class="re-hint">{{ $t('meeting.selectRoomHint') }}</div>
  </div>
</template>

<script setup lang="ts">
import { watch } from 'vue';
import { useRouter } from 'vue-router';
import { useMeetingStore } from '@/stores/meeting';

const router = useRouter();
const store = useMeetingStore();

// 列表加载完成后自动打开第一个会议，避免右侧长期停留在空态
watch(
  () => store.rooms.length,
  (len) => {
    if (len > 0) {
      router.replace({ name: 'meetingRoom', params: { roomId: store.rooms[0].id } });
    }
  },
  { immediate: true },
);
</script>

<style lang="less" scoped>
.room-empty {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--app-space-2);
  background: var(--td-bg-color-container);
}

.re-icon {
  color: var(--td-text-color-placeholder);
  opacity: 0.6;
}

.re-title {
  font-size: var(--app-text-xl);
  font-weight: 600;
  color: var(--td-text-color-secondary);
}

.re-hint {
  font-size: var(--app-text-md);
  color: var(--td-text-color-placeholder);
}
</style>
