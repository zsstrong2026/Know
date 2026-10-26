<template>
  <!-- 只读分析方案：叠在需求弹窗之上，不新增路由 -->
  <t-dialog
    :visible="visible"
    width="820px"
    placement="center"
    :header="dialogTitle"
    :footer="false"
    :close-on-overlay-click="true"
    @close="close"
  >
    <div class="plan-dialog__body">
      <PmMarkdown v-if="markdown" :content="markdown" />
      <p v-else class="plan-dialog__empty">{{ $t('pm.requirements.ai.planEmpty') }}</p>
    </div>
    <div class="plan-dialog__foot">
      <t-button variant="outline" @click="close">{{ $t('pm.requirements.ai.planClose') }}</t-button>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import { useI18n } from 'vue-i18n';
import PmMarkdown from './components/PmMarkdown.vue';

const props = defineProps<{
  visible: boolean;
  markdown: string;
  title?: string;
}>();
const emit = defineEmits<{ 'update:visible': [value: boolean] }>();

const { t } = useI18n();
const dialogTitle = computed(() => props.title || t('pm.requirements.ai.planTitle'));

function close() {
  emit('update:visible', false);
}
</script>

<style lang="less" scoped>
.plan-dialog__body {
  max-height: 62vh;
  overflow-y: auto;
  padding-right: var(--app-space-2);
}

.plan-dialog__empty {
  margin: 0;
  padding: var(--app-space-6) 0;
  text-align: center;
  color: var(--td-text-color-placeholder);
}

.plan-dialog__foot {
  display: flex;
  justify-content: flex-end;
  padding-top: var(--app-space-4);
}
</style>
