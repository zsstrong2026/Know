<template>
  <t-dialog
    :visible="visible"
    width="520px"
    placement="center"
    :header="$t('pm.projects.create')"
    :confirm-btn="{ content: $t('common.create'), loading: submitting }"
    :cancel-btn="$t('common.cancel')"
    :on-confirm="submit"
    :on-close="close"
    @close="close"
  >
    <t-form label-align="top" :data="form" class="pm-project-form">
      <t-form-item :label="$t('pm.projects.name')" required-mark>
        <t-input
          v-model="form.name"
          :placeholder="$t('pm.projects.namePlaceholder')"
          :maxlength="PROJECT_NAME_MAX_LENGTH"
          :status="error ? 'error' : 'default'"
          @enter="submit"
        />
        <template #help>
          <span v-if="error" class="pm-form-error">{{ error }}</span>
          <span v-else class="pm-form-hint">{{ $t('pm.projects.kbHint') }}</span>
        </template>
      </t-form-item>

      <t-form-item :label="$t('pm.projects.description')">
        <t-textarea
          v-model="form.description"
          :placeholder="$t('pm.projects.descriptionPlaceholder')"
          :autosize="{ minRows: 4, maxRows: 4 }"
          :maxlength="500"
        />
      </t-form-item>
    </t-form>
  </t-dialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import { createPmProject, updatePmProject, type PmProject } from '@/api/pm';
import { PROJECT_NAME_MAX_LENGTH, normalizeProjectName, validateProjectName } from './pmProjectForm';

const props = defineProps<{ visible: boolean }>();
const emit = defineEmits<{ 'update:visible': [boolean]; created: [PmProject] }>();

const { t } = useI18n();
const form = reactive({ name: '', description: '' });
const error = ref('');
const submitting = ref(false);

const visible = computed(() => props.visible);

// 每次打开重置，避免上次输入残留
watch(
  () => props.visible,
  (visible) => {
    if (!visible) return;
    form.name = '';
    form.description = '';
    error.value = '';
  },
);

function close() {
  emit('update:visible', false);
}

async function submit() {
  // 校验文案走 i18n，默认中文文案仅用于单测
  error.value = validateProjectName(form.name, {
    empty: t('pm.projects.nameEmpty'),
    tooLong: t('pm.projects.nameTooLong'),
  });
  if (error.value) return;

  submitting.value = true;
  try {
    let project = await createPmProject({ name: normalizeProjectName(form.name), is_new: true });
    // 创建接口不落描述，用户填写时补一次更新
    const description = form.description.trim();
    if (description) project = await updatePmProject(project.id, { description });
    MessagePlugin.success(t('pm.projects.createSuccess'));
    emit('created', project);
    close();
  } catch {
    MessagePlugin.error(t('pm.projects.createFailed'));
  } finally {
    submitting.value = false;
  }
}
</script>

<style lang="less" scoped>
.pm-form-error {
  color: var(--td-error-color);
}

.pm-form-hint {
  color: var(--td-text-color-placeholder);
}
</style>
