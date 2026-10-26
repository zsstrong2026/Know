<template>
  <!-- 占位视图：后续任务逐个替换为真实页面 -->
  <div class="pm-placeholder">
    <t-icon name="time" size="32px" />
    <div class="pm-placeholder-title">{{ $t(titleKey) }}</div>
    <div class="pm-placeholder-desc">{{ $t('pm.placeholder') }}</div>
  </div>
</template>

<script setup lang="ts">
defineProps<{ titleKey: string }>();
</script>

<style lang="less" scoped>
.pm-placeholder {
  flex: 1;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--app-space-2);
  padding: var(--app-space-8);
  color: var(--td-text-color-placeholder);
}

.pm-placeholder-title {
  font-size: var(--app-text-lg);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.pm-placeholder-desc {
  font-size: var(--app-text-sm);
}
</style>
