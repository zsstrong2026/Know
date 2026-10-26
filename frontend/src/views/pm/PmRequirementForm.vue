<template>
  <!-- 需求创建/编辑：单步表单；AI 分析以右栏辅助形式展开，不阻塞主流程 -->
  <t-dialog
    :visible="visible"
    :width="panelOpen ? '1240px' : '760px'"
    placement="center"
    :header="$t(savedRequirement ? 'pm.requirements.form.editTitle' : 'pm.requirements.form.createTitle')"
    :footer="false"
    :close-on-overlay-click="false"
    @close="close"
  >
    <div class="rq-layout" :class="{ 'is-split': panelOpen }">
      <section class="rq-main">
        <t-form label-align="top" :data="draft">
          <t-form-item
            :label="$t('pm.requirements.form.title')"
            required-mark
            :status="showError && !canAnalyzeRequirement(draft) ? 'error' : 'default'"
          >
            <t-input
              v-model="draft.title"
              :placeholder="$t('pm.requirements.form.titlePlaceholder')"
              :maxlength="200"
            />
          </t-form-item>
          <t-form-item :label="$t('pm.requirements.form.description')">
            <t-textarea
              v-model="draft.description"
              :placeholder="$t('pm.requirements.form.descriptionPlaceholder')"
              :autosize="{ minRows: 8, maxRows: 8 }"
              :maxlength="2000"
            />
          </t-form-item>
          <div class="rq-row">
            <t-form-item
              :label="$t('pm.requirements.form.source')"
              required-mark
              :status="showError && !canAnalyzeRequirement(draft) ? 'error' : 'default'"
            >
              <t-select
                v-model="draft.source"
                :placeholder="$t('pm.requirements.form.sourcePlaceholder')"
                :options="sourceOptions"
                clearable
              />
            </t-form-item>
            <t-form-item :label="$t('pm.requirements.form.proposer')">
              <t-input
                v-model="draft.proposer"
                :placeholder="$t('pm.requirements.form.proposerPlaceholder')"
              />
            </t-form-item>
          </div>
          <t-form-item :label="$t('pm.requirements.form.priority')">
            <!-- 独立胶囊选择器：选中态按优先级语义色着色，与详情页标签保持一致 -->
            <div class="rq-priority" role="radiogroup" :aria-label="$t('pm.requirements.form.priority')">
              <button
                v-for="item in REQUIREMENT_PRIORITIES"
                :key="item"
                type="button"
                role="radio"
                :aria-checked="draft.priority === item"
                class="rq-priority__item"
                :class="[`rq-priority__item--${priorityTheme(item)}`, { 'is-active': draft.priority === item }]"
                @click="draft.priority = item"
              >
                {{ item }}
              </button>
            </div>
            <template #help>
              <span class="rq-hint">{{ $t('pm.requirements.form.priorityHint') }}</span>
            </template>
          </t-form-item>
          <t-form-item :label="$t('pm.requirements.form.attachments')">
            <!-- 附件随需求落库后上传，AI 分析时作为上下文 -->
            <div class="rq-attach">
              <ul v-if="attachmentItems.length" class="rq-attach__list">
                <li v-for="item in attachmentItems" :key="item.key" class="rq-attach__item">
                  <span class="rq-attach__name" :title="item.name">{{ item.name }}</span>
                  <span class="rq-attach__size">{{ formatFileSize(item.size) }}</span>
                  <button
                    type="button"
                    class="rq-attach__remove"
                    :disabled="saving"
                    :aria-label="$t('pm.requirements.form.attachmentRemove')"
                    @click="removeAttachment(item)"
                  >
                    ×
                  </button>
                </li>
              </ul>
              <label class="rq-attach__picker" :class="{ 'is-disabled': saving }">
                <input
                  type="file"
                  multiple
                  :accept="ATTACHMENT_ACCEPT"
                  :disabled="saving"
                  @change="onPickFiles"
                />
                {{ $t('pm.requirements.form.attachmentPick') }}
              </label>
              <span class="rq-hint">{{ $t('pm.requirements.form.attachmentHint') }}</span>
            </div>
          </t-form-item>
          <p v-if="showError && !canAnalyzeRequirement(draft)" class="rq-error">
            {{ $t('pm.requirements.form.required') }}
          </p>
        </t-form>
      </section>

      <section v-if="panelOpen && panelRequirement" class="rq-panel">
        <PmRequirementAnalysisChat
          :requirement="panelRequirement"
          :initial-analysis="analysis"
          @analysis-change="onAnalysisChange"
        />
      </section>
    </div>

    <div class="rq-foot">
      <t-button variant="text" :disabled="saving" @click="close">
        {{ $t('common.cancel') }}
      </t-button>
      <div class="rq-foot__actions">
        <t-button v-if="showAnalyzeEntry" variant="outline" :disabled="saving" @click="openAnalysis">
          {{ $t('pm.requirements.form.aiAnalyze') }}
        </t-button>
        <t-button v-else-if="showPlanEntry" variant="outline" @click="planVisible = true">
          {{ $t('pm.requirements.form.planView') }}
        </t-button>
        <t-button
          v-if="showAnalyzeEntry"
          variant="outline"
          :loading="saving"
          :disabled="!canSaveDraft(draft)"
          @click="submit('draft')"
        >
          {{ $t('pm.requirements.form.saveDraft') }}
        </t-button>
        <t-button v-else theme="primary" :loading="saving" @click="submit('edit')">
          {{ $t('pm.requirements.form.save') }}
        </t-button>
        <t-button
          v-if="showAnalyzeEntry"
          theme="primary"
          :loading="saving"
          :disabled="!canAnalyzeRequirement(draft)"
          @click="submit('confirm')"
        >
          {{ $t('pm.requirements.form.confirm') }}
        </t-button>
      </div>
    </div>

    <PmRequirementPlanDialog v-model:visible="planVisible" :markdown="planMarkdown" />
  </t-dialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  createPmRequirement,
  deletePmRequirementAttachment,
  listPmRequirementAttachments,
  updatePmRequirement,
  uploadPmRequirementAttachment,
  type CreatePmRequirementInput,
  type PmRequirement,
  type PmRequirementAttachment,
  type PmRequirementSource,
  type UpdatePmRequirementInput,
} from '@/api/pm';
import {
  ATTACHMENT_ACCEPT,
  ATTACHMENT_MAX_SIZE,
  REQUIREMENT_PRIORITIES,
  REQUIREMENT_SOURCES,
  buildAnalysisRecord,
  canAnalyzeRequirement,
  canSaveDraft,
  emptyRequirementAnalysis,
  emptyRequirementDraft,
  formatFileSize,
  hasAnalysis,
  isAllowedAttachment,
  priorityTheme,
  readRequirementAnalysis,
  type RequirementAnalysis,
  type RequirementDraft,
} from './pmRequirementState';
import PmRequirementAnalysisChat from './PmRequirementAnalysisChat.vue';
import PmRequirementPlanDialog from './PmRequirementPlanDialog.vue';

const props = defineProps<{
  visible: boolean;
  projectId: string;
  requirement: PmRequirement | null;
}>();
const emit = defineEmits<{
  'update:visible': [value: boolean];
  saved: [];
}>();

const { t } = useI18n();
const saving = ref(false);
const showError = ref(false);
const panelOpen = ref(false);
// 本次弹窗是否真正使用过 AI 分析，决定保存时是否回写分析结论
const panelTouched = ref(false);
const planVisible = ref(false);
const draft = reactive<RequirementDraft>(emptyRequirementDraft());
const analysis = reactive<RequirementAnalysis>(emptyRequirementAnalysis());
// 新建流程中已静默落库的需求，后续保存都基于它做 PATCH
const created = ref<PmRequirement | null>(null);
const existingAttachments = ref<PmRequirementAttachment[]>([]);
const stagedFiles = ref<File[]>([]);

interface AttachmentItem {
  key: string;
  name: string;
  size: number;
  attachment?: PmRequirementAttachment;
  file?: File;
}

// 两种来源合并渲染，删除时按来源分别处理
const attachmentItems = computed<AttachmentItem[]>(() => [
  ...existingAttachments.value.map((attachment) => ({
    key: attachment.id,
    name: attachment.file_name,
    size: attachment.size,
    attachment,
  })),
  ...stagedFiles.value.map((file, index) => ({
    key: `staged-${index}-${file.name}`,
    name: file.name,
    size: file.size,
    file,
  })),
]);

const sourceOptions = computed(() => REQUIREMENT_SOURCES.map((value) => ({
  label: t(`pm.requirements.source.${value}`),
  value,
})));

const savedRequirement = computed(() => created.value ?? props.requirement);
// 右栏使用「已落库需求 + 当前表单值」，保证上下文清单与表单一致
const panelRequirement = computed<PmRequirement | null>(() => {
  const base = savedRequirement.value;
  if (!base) return null;
  return {
    ...base,
    title: draft.title.trim() || base.title,
    description: draft.description.trim(),
    source: (draft.source || base.source) as PmRequirementSource,
  };
});

// 仅待分析状态可发起/重新发起 AI 分析，其余状态只保留只读方案
const requirementStatus = computed(() => savedRequirement.value?.status ?? 'pending_analysis');
const showAnalyzeEntry = computed(() => requirementStatus.value === 'pending_analysis');
const showPlanEntry = computed(
  () => !showAnalyzeEntry.value
    && (hasAnalysis(props.requirement?.ai_analysis) || Boolean(props.requirement?.design_plan)),
);

// 只读方案：由已保存的结论拼装
const planMarkdown = computed(() => {
  const sections: Array<[string, string]> = [
    [t('pm.requirements.form.value'), analysis.value],
    [t('pm.requirements.form.impact'), analysis.impact],
    [t('pm.requirements.form.risk'), analysis.risk],
    [t('pm.requirements.form.designPlan'), analysis.designPlan],
  ];
  const filled = sections.filter(([, text]) => text.trim());
  return filled.map(([label, text]) => `## ${label}\n${text.trim()}`).join('\n\n');
});

function onAnalysisChange(next: RequirementAnalysis) {
  Object.assign(analysis, next);
}

async function loadAttachments(requirementId: string) {
  try {
    existingAttachments.value = await listPmRequirementAttachments(requirementId);
  } catch {
    existingAttachments.value = [];
  }
}

// 选择文件：本地先做类型与大小校验，重名同大小的文件不重复加入
function onPickFiles(event: Event) {
  const input = event.target as HTMLInputElement;
  const files = Array.from(input.files ?? []);
  input.value = ''; // 清空后才能再次选择同一个文件
  for (const file of files) {
    if (!isAllowedAttachment(file.name)) {
      MessagePlugin.warning(t('pm.requirements.form.attachmentTypeInvalid', { name: file.name }));
      continue;
    }
    if (file.size > ATTACHMENT_MAX_SIZE) {
      MessagePlugin.warning(t('pm.requirements.form.attachmentTooLarge', { name: file.name }));
      continue;
    }
    const duplicated = attachmentItems.value.some((item) => item.name === file.name && item.size === file.size);
    if (!duplicated) stagedFiles.value.push(file);
  }
}

async function removeAttachment(item: AttachmentItem) {
  if (item.file) {
    stagedFiles.value = stagedFiles.value.filter((file) => file !== item.file);
    return;
  }
  const attachment = item.attachment;
  if (!attachment || saving.value) return;
  try {
    await deletePmRequirementAttachment(attachment.requirement_id, attachment.id);
    existingAttachments.value = existingAttachments.value.filter((current) => current.id !== attachment.id);
  } catch {
    MessagePlugin.error(t('pm.requirements.form.attachmentRemoveFailed'));
  }
}

// 需求落库后再上传暂存附件；上传后刷新列表，保证右栏上下文与界面一致
async function uploadStagedAttachments(requirementId: string) {
  const files = [...stagedFiles.value];
  if (files.length) {
    stagedFiles.value = [];
    let failed = 0;
    for (const file of files) {
      try {
        await uploadPmRequirementAttachment(requirementId, file);
      } catch {
        failed += 1;
      }
    }
    if (failed) {
      MessagePlugin.warning(t('pm.requirements.form.attachmentUploadFailed', { count: failed }));
    }
  }
  await loadAttachments(requirementId);
}

watch(
  () => props.visible,
  (visible) => {
    if (!visible) return;
    showError.value = false;
    saving.value = false;
    panelOpen.value = false;
    panelTouched.value = false;
    planVisible.value = false;
    created.value = null;
    existingAttachments.value = [];
    stagedFiles.value = [];
    Object.assign(analysis, emptyRequirementAnalysis());
    const requirement = props.requirement;
    Object.assign(draft, requirement
      ? {
        title: requirement.title,
        description: requirement.description,
        source: requirement.source,
        proposer: requirement.proposer,
        priority: requirement.priority,
      }
      : emptyRequirementDraft());
    if (requirement) {
      Object.assign(analysis, readRequirementAnalysis(requirement.ai_analysis), {
        designPlan: requirement.design_plan || '',
      });
      void loadAttachments(requirement.id);
    }
  },
);

// 基础信息请求体；来源已通过必填校验，此处可安全断言
function basePayload() {
  return {
    title: draft.title.trim(),
    description: draft.description.trim(),
    priority: draft.priority,
    source: draft.source as PmRequirementSource,
    proposer: draft.proposer.trim(),
  };
}

// 草稿请求体：仅提交已填写字段，来源留空由后端补为「内部提出」
function draftPayload(): CreatePmRequirementInput {
  return {
    title: draft.title.trim(),
    description: draft.description.trim(),
    priority: draft.priority,
    proposer: draft.proposer.trim(),
    ...(draft.source ? { source: draft.source } : {}),
  };
}

// 新建或更新基础信息，并上传附件；返回已落库需求
async function persistBase(payload: CreatePmRequirementInput): Promise<PmRequirement> {
  let requirement = created.value ?? props.requirement;
  if (requirement) {
    await updatePmRequirement(requirement.id, payload);
  } else {
    requirement = await createPmRequirement(props.projectId, payload);
    created.value = requirement;
  }
  await uploadStagedAttachments(requirement.id);
  return requirement;
}

// 「AI 分析」入口：静默落库为草稿后展开右栏
async function openAnalysis() {
  if (saving.value) return;
  if (!canAnalyzeRequirement(draft)) {
    showError.value = true;
    return;
  }
  showError.value = false;
  saving.value = true;
  try {
    await persistBase(basePayload());
    panelTouched.value = true;
    panelOpen.value = true;
    emit('saved');
  } catch {
    MessagePlugin.error(t('pm.requirements.form.saveFailed'));
  } finally {
    saving.value = false;
  }
}

// 保存出口：草稿仅校验标题；确认/编辑保存校验标题与来源
async function submit(mode: 'draft' | 'confirm' | 'edit') {
  if (saving.value) return;
  if (mode === 'draft' ? !canSaveDraft(draft) : !canAnalyzeRequirement(draft)) {
    showError.value = true;
    return;
  }
  showError.value = false;
  const isNew = !savedRequirement.value;
  saving.value = true;
  try {
    const requirement = await persistBase(mode === 'draft' ? draftPayload() : basePayload());
    const patch: UpdatePmRequirementInput = {};
    if (mode === 'confirm') patch.status = 'confirmed';
    if (panelTouched.value) {
      const record = buildAnalysisRecord(analysis);
      const filled = Object.values(record).some((value) => typeof value === 'string' && value.trim() !== '');
      if (filled) {
        patch.ai_analysis = record;
        patch.design_plan = analysis.designPlan.trim();
      }
    }
    if (Object.keys(patch).length) await updatePmRequirement(requirement.id, patch);
    MessagePlugin.success(t(
      mode === 'draft'
        ? (isNew ? 'pm.requirements.form.draftSuccess' : 'pm.requirements.form.saveSuccess')
        : mode === 'confirm'
          ? 'pm.requirements.form.confirmSuccess'
          : 'pm.requirements.form.saveSuccess',
    ));
    emit('saved');
    close();
  } catch {
    MessagePlugin.error(t(
      mode === 'draft'
        ? 'pm.requirements.form.draftFailed'
        : mode === 'confirm'
          ? 'pm.requirements.form.confirmFailed'
          : 'pm.requirements.form.saveFailed',
    ));
  } finally {
    saving.value = false;
  }
}

function close() {
  emit('update:visible', false);
}
</script>

<style lang="less" scoped>
/* 单步布局：右侧展开后固定高度，两栏各自滚动 */
.rq-layout {
  display: flex;
  gap: var(--app-space-4);

  &.is-split {
    height: 66vh;
    min-height: 460px;
  }
}

.rq-main {
  flex: 1;
  min-width: 0;

  .rq-layout.is-split & {
    overflow-y: auto;
    padding-right: var(--app-space-3);
  }
}

.rq-panel {
  flex: 0 0 480px;
  min-width: 0;
  height: 100%;
}

.rq-row {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: var(--app-space-3);
}

.rq-hint {
  color: var(--td-text-color-placeholder);
}

.rq-error {
  margin: 0;
  font-size: var(--app-text-sm);
  color: var(--td-error-color);
}

/* 优先级胶囊选择器：未选中为中性描边，选中按语义色着色 */
.rq-priority {
  display: flex;
  gap: var(--app-space-2);

  &__item {
    min-width: 64px;
    height: 32px;
    padding: 0 var(--app-space-4);
    font-family: inherit;
    font-size: var(--app-text-sm);
    font-weight: 500;
    color: var(--td-text-color-secondary);
    background: var(--td-bg-color-container);
    border: 1px solid var(--td-component-stroke);
    border-radius: var(--td-radius-medium);
    cursor: pointer;
    transition: color 0.2s, border-color 0.2s, background-color 0.2s;

    &:hover {
      color: var(--rq-priority-color);
      border-color: var(--rq-priority-color);
    }

    &.is-active {
      font-weight: 600;
      color: var(--rq-priority-color);
      background: var(--rq-priority-bg);
      border-color: var(--rq-priority-color);
    }

    /* 语义色映射与需求详情中的优先级标签一致 */
    &--danger {
      --rq-priority-bg: var(--td-error-color-1);
      --rq-priority-color: var(--td-error-color);
    }

    &--warning {
      --rq-priority-bg: var(--td-warning-color-1);
      --rq-priority-color: var(--td-warning-color);
    }

    &--primary {
      --rq-priority-bg: var(--td-brand-color-1);
      --rq-priority-color: var(--td-brand-color);
    }

    &--default {
      --rq-priority-bg: var(--td-bg-color-secondarycontainer);
      --rq-priority-color: var(--td-gray-color-9);
    }
  }
}

/* 附件：列表展示已选文件，选择入口用 label 包住隐藏的原生 input */
.rq-attach {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-2);
  width: 100%;

  &__list {
    display: flex;
    flex-direction: column;
    gap: 6px;
    margin: 0;
    padding: 0;
    list-style: none;
  }

  &__item {
    display: flex;
    align-items: center;
    gap: var(--app-space-2);
    padding: 6px var(--app-space-2);
    border: 1px solid var(--td-component-stroke);
    border-radius: var(--td-radius-default);
    background: var(--td-bg-color-secondarycontainer);
  }

  &__name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    font-size: var(--app-text-sm);
    color: var(--td-text-color-primary);
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  &__size {
    flex-shrink: 0;
    font-size: var(--app-text-xs);
    color: var(--td-text-color-placeholder);
  }

  &__remove {
    flex-shrink: 0;
    width: 20px;
    height: 20px;
    padding: 0;
    font-size: 16px;
    line-height: 1;
    color: var(--td-text-color-placeholder);
    background: transparent;
    border: 0;
    border-radius: var(--td-radius-circle);
    cursor: pointer;

    &:hover:not(:disabled) {
      color: var(--td-error-color);
      background: var(--td-error-color-1);
    }

    &:disabled {
      cursor: not-allowed;
    }
  }

  &__picker {
    align-self: flex-start;
    padding: 0 12px;
    height: 32px;
    font-size: var(--app-text-sm);
    line-height: 30px;
    color: var(--td-brand-color);
    background: var(--td-bg-color-container);
    border: 1px dashed var(--td-brand-color-4);
    border-radius: var(--td-radius-default);
    cursor: pointer;

    &:hover {
      background: var(--td-brand-color-1);
    }

    &.is-disabled {
      color: var(--td-text-color-disabled);
      border-color: var(--td-component-stroke);
      cursor: not-allowed;
    }

    input {
      display: none;
    }
  }
}

.rq-foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--app-space-2);
  padding-top: var(--app-space-4);
}

.rq-foot__actions {
  display: flex;
  gap: var(--app-space-2);
}
</style>
