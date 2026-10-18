<template>
  <!-- 生成产物弹窗：选形态 + 标题 + 补充要求 → store.requestArtifact（202 异步） -->
  <!-- z-index 2700：需高于 ArtifactDrawer 显式设置的 2600，否则弹窗被抽屉面板遮挡 -->
  <t-dialog :visible="visible" :header="$t('meeting.generateArtifact')" :width="560" :z-index="2700"
    :close-on-overlay-click="false" attach="body"
    :confirm-btn="{ content: $t('meeting.generateArtifact'), loading: submitting }"
    :cancel-btn="{ content: $t('meeting.cancel') }" @confirm="submit"
    @close="emit('update:visible', false)">
    <t-form :data="form" label-width="90" class="gen-form">
      <t-form-item :label="$t('meeting.artifactKind')" required>
        <div class="kind-grid">
          <button v-for="k in KINDS" :key="k.value" type="button" class="kind-cell"
            :class="{ 'is-active': form.kindType === k.value }" @click="form.kindType = k.value">
            {{ $t(k.label) }}
          </button>
        </div>
      </t-form-item>
      <t-form-item :label="$t('meeting.artifactTitle')">
        <t-input v-model="form.title" :maxlength="100"
          :placeholder="$t('meeting.artifactTitlePlaceholder')" />
      </t-form-item>
      <t-form-item :label="$t('meeting.artifactRequirement')">
        <t-textarea v-model="form.requirement" :autosize="{ minRows: 3, maxRows: 6 }"
          :placeholder="$t('meeting.artifactRequirementPlaceholder')" />
      </t-form-item>
    </t-form>
  </t-dialog>
</template>

<script setup lang="ts">
import { reactive, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import { useMeetingStore } from '@/stores/meeting';
import type { ArtifactKindType } from '@/api/meeting';

const props = defineProps<{ visible: boolean }>();
const emit = defineEmits<{ 'update:visible': [boolean]; requested: [] }>();

const { t } = useI18n();
const store = useMeetingStore();
const submitting = ref(false);

// 8 种形态（顺序即展示顺序；与后端白名单一致）
const KINDS: Array<{ value: ArtifactKindType; label: string }> = [
  { value: 'report_markdown', label: 'meeting.kindReportMarkdown' },
  { value: 'minutes', label: 'meeting.kindMinutes' },
  { value: 'todo_list', label: 'meeting.kindTodoList' },
  { value: 'docx', label: 'meeting.kindDocx' },
  { value: 'xlsx', label: 'meeting.kindXlsx' },
  { value: 'pptx', label: 'meeting.kindPptx' },
  { value: 'html_report', label: 'meeting.kindHtmlReport' },
  { value: 'chart_image', label: 'meeting.kindChartImage' },
];

const form = reactive({
  kindType: 'report_markdown' as ArtifactKindType,
  title: '',
  requirement: '',
});

// 每次打开重置表单，避免沿用上次残留
watch(() => props.visible, (v) => {
  if (v) {
    form.kindType = 'report_markdown';
    form.title = '';
    form.requirement = '';
  }
});

async function submit() {
  submitting.value = true;
  try {
    await store.requestArtifact(form.kindType, form.title.trim(), form.requirement.trim());
    emit('update:visible', false);
    emit('requested'); // 抽屉据此保持在列表态，观看 loading 卡片
  } catch (e) {
    const msg = (e as Error)?.message || '';
    // 409：同房间已有生成任务（room 级互斥）
    if (msg.includes('409') || msg.includes('正在生成') || msg.includes('Conflict')) {
      MessagePlugin.warning(t('meeting.artifactBusy'));
      return;
    }
    MessagePlugin.error(msg || t('meeting.loadFailed'));
  } finally {
    submitting.value = false;
  }
}
</script>

<style lang="less" scoped>
.gen-form {
  padding-top: var(--app-space-2, 8px);
}

.kind-grid {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: var(--app-space-2, 8px);
  width: 100%;
}

.kind-cell {
  height: 34px;
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--td-radius-medium, 6px);
  background: var(--td-bg-color-container);
  font-size: 13px;
  cursor: pointer;
  transition: border-color 0.15s ease, color 0.15s ease;

  &:hover {
    border-color: var(--td-brand-color);
  }

  &.is-active {
    border-color: var(--td-brand-color);
    color: var(--td-brand-color);
    background: color-mix(in srgb, var(--td-brand-color) 6%, transparent);
    font-weight: 500;
  }
}
</style>
