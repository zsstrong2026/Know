<template>
  <!-- 项目切换器：当前项 ✓ + 编号前缀，底部「＋ 新建项目…」 -->
  <t-dropdown
    :options="options"
    trigger="click"
    placement="bottom-right"
    attach="body"
    @click="onSelect"
  >
    <button class="sop-project-trigger" type="button">
      <span class="sop-project-label">{{ $t('sop.project.label') }}</span>
      <span class="sop-project-name">{{ store.currentProject?.name || $t('sop.project.none') }}</span>
      <t-icon name="chevron-down" size="14px" />
    </button>
  </t-dropdown>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import { storeToRefs } from 'pinia';
import { useSopStore } from '@/stores/sop';

const emit = defineEmits<{ create: [] }>();

const { t } = useI18n();
const store = useSopStore();
const { projects } = storeToRefs(store);

const options = computed(() => [
  ...projects.value.map((project) => ({
    value: project.id,
    content: `${store.currentProjectId === project.id ? '✓ ' : ''}${project.name}（${project.code}）`,
  })),
  { value: '__create__', content: `＋ ${t('sop.project.createEllipsis')}` },
]);

async function onSelect(data: { value?: string | number }) {
  const value = String(data.value || '');
  if (!value) return;
  if (value === '__create__') {
    emit('create');
    return;
  }
  if (value === store.currentProjectId) return;
  try {
    await store.switchProject(value);
  } catch {
    MessagePlugin.error(t('sop.project.switchFailed'));
  }
}
</script>

<style lang="less" scoped>
.sop-project-trigger {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  max-width: 320px;
  padding: 6px 10px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  font-size: var(--app-text-sm);
  cursor: pointer;

  &:hover {
    border-color: var(--td-brand-color);
  }
}

.sop-project-label {
  color: var(--td-text-color-secondary);
}

.sop-project-name {
  max-width: 200px;
  overflow: hidden;
  font-weight: 600;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
