<template>
  <!-- 后续任务会逐个替换为完整页面：这里只保证标题与空态，路由与布局先跑通 -->
  <div class="sop-page">
    <header class="sop-page-head">
      <h2 class="sop-page-title">{{ title }}</h2>
      <p v-if="description" class="sop-page-desc">{{ description }}</p>
    </header>
    <div class="sop-page-body">
      <t-empty :description="emptyText" />
    </div>
  </div>
</template>

<script setup lang="ts">
defineProps<{ title: string; description?: string; emptyText: string }>();
</script>

<style lang="less" scoped>
.sop-page {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  padding: var(--app-space-5);
}

.sop-page-head {
  margin-bottom: var(--app-space-4);
}

.sop-page-title {
  margin: 0;
  font-size: var(--app-text-lg);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.sop-page-desc {
  margin: 6px 0 0;
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
}

.sop-page-body {
  flex: 1;
  min-height: 240px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: 1px dashed var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
}
</style>
