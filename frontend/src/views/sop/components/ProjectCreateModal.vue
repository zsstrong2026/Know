<template>
  <t-dialog
    :visible="visible"
    width="560px"
    placement="center"
    :header="$t('sop.project.create')"
    :confirm-btn="{ content: $t('sop.project.create'), loading: submitting }"
    :cancel-btn="$t('common.cancel')"
    :on-confirm="submit"
    :on-close="close"
    @close="close"
  >
    <t-form label-align="top" :data="form">
      <t-form-item :label="$t('sop.project.name')" required-mark>
        <t-input
          v-model="form.name"
          :placeholder="$t('sop.project.namePlaceholder')"
          :maxlength="200"
          :status="nameError ? 'error' : 'default'"
          @enter="submit"
        />
        <template #help>
          <span v-if="nameError" class="sop-form-error">{{ nameError }}</span>
        </template>
      </t-form-item>

      <t-form-item :label="$t('sop.project.code')" required-mark>
        <t-input
          v-model="form.code"
          class="sop-form-code"
          :placeholder="$t('sop.project.codePlaceholder')"
          :maxlength="10"
          :status="codeError ? 'error' : 'default'"
        />
        <template #help>
          <span v-if="codeError" class="sop-form-error">{{ codeError }}</span>
          <span v-else class="sop-form-hint">{{ $t('sop.project.codeHint') }}</span>
        </template>
      </t-form-item>

      <t-form-item :label="$t('sop.project.description')">
        <t-textarea
          v-model="form.description"
          :placeholder="$t('sop.project.descriptionPlaceholder')"
          :autosize="{ minRows: 3, maxRows: 5 }"
          :maxlength="500"
        />
      </t-form-item>

      <t-form-item :label="$t('sop.kbCard.title')">
        <t-radio-group v-model="form.kbMode" class="sop-kb-modes">
          <t-radio value="new">{{ $t('sop.kbCard.modeNew', { name: form.name.trim() || $t('sop.project.name') }) }}</t-radio>
          <t-radio value="existing">{{ $t('sop.kbCard.modeExisting') }}</t-radio>
          <t-radio value="none">{{ $t('sop.kbCard.modeNone') }}</t-radio>
        </t-radio-group>
        <t-select
          v-if="form.kbMode === 'existing'"
          v-model="form.kbId"
          class="sop-kb-select"
          :options="kbOptions"
          :placeholder="$t('sop.kbCard.bindTitle')"
          filterable
        />
        <template #help>
          <span class="sop-form-hint">{{ $t('sop.kbCard.sub') }}</span>
        </template>
      </t-form-item>
    </t-form>
  </t-dialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import { storeToRefs } from 'pinia';
import { validateProjectCode } from '@/api/sop/helpers';
import type { KBMode } from '@/api/sop';
import { listKnowledgeBases } from '@/api/knowledge-base';
import { useSopStore } from '@/stores/sop';

const props = defineProps<{ visible: boolean }>();
const emit = defineEmits<{ 'update:visible': [boolean]; created: [] }>();

const { t } = useI18n();
const store = useSopStore();
const { projects } = storeToRefs(store);

const form = reactive({
  name: '',
  code: '',
  description: '',
  // 知识库三选：new=自动建同名库（默认）/ existing=绑已有库 / none=暂不建。
  kbMode: 'new' as KBMode,
  kbId: '',
});
const kbList = ref<{ id: string; name: string }[]>([]);
const kbOptions = computed(() => kbList.value.map((kb) => ({ label: kb.name, value: kb.id })));
const nameError = ref('');
const codeError = ref('');
const submitting = ref(false);

const visible = computed(() => props.visible);

watch(
  () => props.visible,
  (open) => {
    if (!open) return;
    form.name = '';
    form.code = '';
    form.description = '';
    form.kbMode = 'new';
    form.kbId = '';
    nameError.value = '';
    codeError.value = '';
    void loadKBs();
  },
);

/** 知识库下拉只在选「使用已有」时才需要：失败就当没有可选库，不阻塞创建。 */
async function loadKBs() {
  if (kbList.value.length) return;
  try {
    const res = await listKnowledgeBases();
    const data = (res as { data?: { id: string; name: string }[] })?.data || [];
    kbList.value = data.map((kb) => ({ id: String(kb.id), name: kb.name }));
  } catch {
    kbList.value = [];
  }
}

function close() {
  emit('update:visible', false);
}

function validate(): boolean {
  nameError.value = '';
  codeError.value = '';
  const name = form.name.trim();
  const code = form.code.trim().toUpperCase();
  if (!name) nameError.value = t('sop.project.nameRequired');
  if (!code) codeError.value = t('sop.project.codeRequired');
  else if (!validateProjectCode(code)) codeError.value = t('sop.project.codeInvalid');
  else if (projects.value.some((p) => p.code.toUpperCase() === code)) {
    codeError.value = t('sop.project.codeDuplicate', { code });
  }
  if (form.kbMode === 'existing' && !form.kbId) {
    MessagePlugin.warning(t('sop.kbCard.bindTitle'));
    return false;
  }
  return !nameError.value && !codeError.value;
}

async function submit() {
  if (!validate()) return;
  submitting.value = true;
  try {
    await store.createProject({
      name: form.name.trim(),
      code: form.code.trim().toUpperCase(),
      description: form.description.trim(),
      kb_mode: form.kbMode,
      kb_id: form.kbMode === 'existing' ? form.kbId : undefined,
    });
    emit('created');
    close();
  } catch (error: any) {
    // 后端冲突文案（编号/名称已被占用）直接透出，比「创建失败」更有用
    const message = error?.message || t('sop.project.createFailed');
    MessagePlugin.error(message);
  } finally {
    submitting.value = false;
  }
}
</script>

<style lang="less" scoped>
.sop-form-code {
  font-family: var(--app-font-family-mono);
  text-transform: uppercase;
}

.sop-form-error {
  color: var(--td-error-color);
}

.sop-form-hint {
  color: var(--td-text-color-placeholder);
}

.sop-kb-modes {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.sop-kb-select {
  width: 100%;
  margin-top: 8px;
}
</style>
