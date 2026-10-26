<template>
  <!-- 需求详情抽屉：基础信息 / AI 分析 / 设计方案 / 关联版本 / 上线验证 -->
  <t-drawer
    :visible="visible"
    size="580px"
    :header="$t('pm.requirements.detail.title')"
    :footer="false"
    @close="close"
  >
    <div v-if="requirement" class="rd">
      <div class="rd-head">
        <h3 class="rd-title">{{ requirement.title }}</h3>
        <div class="rd-tags">
          <t-tag :theme="priorityTheme(requirement.priority)" variant="light" size="small">
            {{ requirement.priority }}
          </t-tag>
          <t-tag :theme="statusMeta.theme" variant="light" size="small">
            {{ $t(statusMeta.labelKey) }}
          </t-tag>
          <t-tag variant="outline" size="small">
            {{ $t(sourceLabelKey(requirement.source)) }}
          </t-tag>
        </div>
      </div>

      <section class="rd-section">
        <h4 class="rd-section__title">{{ $t('pm.requirements.detail.base') }}</h4>
        <dl class="rd-grid">
          <div>
            <dt>{{ $t('pm.requirements.form.proposer') }}</dt>
            <dd>{{ requirement.proposer || '—' }}</dd>
          </div>
          <div>
            <dt>{{ $t('pm.requirements.columns.version') }}</dt>
            <dd class="rd-muted">{{ $t('pm.requirements.versionUnassigned') }}</dd>
          </div>
          <div>
            <dt>{{ $t('pm.requirements.detail.createdAt') }}</dt>
            <dd>{{ formatPmTime(requirement.created_at) }}</dd>
          </div>
          <div>
            <dt>{{ $t('pm.requirements.detail.updatedAt') }}</dt>
            <dd>{{ formatPmTime(requirement.updated_at) }}</dd>
          </div>
        </dl>
        <p class="rd-text">{{ requirement.description || '—' }}</p>
      </section>

      <section class="rd-section">
        <h4 class="rd-section__title">{{ $t('pm.requirements.detail.attachments') }}</h4>
        <p v-if="!attachments.length" class="rd-muted">{{ $t('pm.requirements.detail.noAttachments') }}</p>
        <ul v-else class="rd-attach">
          <li v-for="item in attachments" :key="item.id" class="rd-attach__item">
            <!-- 图片直接内联缩略图，其余类型显示扩展名；点击统一在新窗口打开 -->
            <button type="button" class="rd-attach__file" :title="item.file_name" @click="openAttachment(item)">
              <img
                v-if="previews[item.id]"
                :src="previews[item.id]"
                :alt="item.file_name"
                class="rd-attach__thumb"
              />
              <span v-else class="rd-attach__icon">{{ extensionLabel(item.file_name) }}</span>
              <span class="rd-attach__meta">
                <span class="rd-attach__name">{{ item.file_name }}</span>
                <span class="rd-attach__size">{{ formatFileSize(item.size) }}</span>
              </span>
            </button>
          </li>
        </ul>
      </section>

      <section class="rd-section">
        <h4 class="rd-section__title">{{ $t('pm.requirements.detail.analysis') }}</h4>
        <dl v-if="analysisRows.length" class="rd-grid rd-grid--one">
          <div v-for="row in analysisRows" :key="row.key">
            <dt>{{ $t(row.labelKey) }}</dt>
            <dd class="rd-text">{{ row.value }}</dd>
          </div>
        </dl>
        <p v-else class="rd-muted">{{ $t('pm.requirements.detail.emptyAnalysis') }}</p>
      </section>

      <section class="rd-section">
        <h4 class="rd-section__title">{{ $t('pm.requirements.detail.designPlan') }}</h4>
        <PmMarkdown v-if="requirement.design_plan" :content="requirement.design_plan" />
        <p v-else class="rd-muted">{{ $t('pm.requirements.detail.noDesignPlan') }}</p>
      </section>

      <section class="rd-section">
        <h4 class="rd-section__title">{{ $t('pm.requirements.detail.launch') }}</h4>
        <!-- 已上线：可提交价值达成回填 -->
        <t-form v-if="canVerify(requirement.status)" label-align="top">
          <t-form-item :label="$t('pm.requirements.detail.valueAchieved')" required-mark>
            <!-- 价值达成胶囊选择器：选中按语义色着色，与新建需求弹窗的优先级选择器保持一致 -->
            <div class="rd-value" role="radiogroup" :aria-label="$t('pm.requirements.detail.valueAchieved')">
              <button
                v-for="item in REQUIREMENT_VALUES"
                :key="item"
                type="button"
                role="radio"
                class="rd-value__item"
                :class="[`rd-value__item--${valueTheme(item)}`, { 'is-active': verifyForm.value === item }]"
                :aria-checked="verifyForm.value === item"
                @click="verifyForm.value = item"
              >
                {{ $t(`pm.requirements.value.${item}`) }}
              </button>
            </div>
          </t-form-item>
          <t-form-item :label="$t('pm.requirements.detail.effectNotes')">
            <t-textarea
              v-model="verifyForm.effectNotes"
              :autosize="{ minRows: 3, maxRows: 6 }"
              :maxlength="NOTE_MAX_LEN"
            />
          </t-form-item>
          <t-form-item :label="$t('pm.requirements.detail.retroNotes')">
            <t-textarea
              v-model="verifyForm.retroNotes"
              :autosize="{ minRows: 3, maxRows: 6 }"
              :maxlength="NOTE_MAX_LEN"
            />
          </t-form-item>
          <t-button theme="primary" :loading="saving" @click="submitVerify">
            {{ $t('pm.requirements.detail.verify') }}
          </t-button>
        </t-form>
        <!-- 已回填：展示结论；其余状态提示上线后开放 -->
        <template v-else-if="requirement.value_achieved">
          <div class="rd-verify">
            <t-tag theme="success" variant="light" size="small">
              {{ $t('pm.requirements.detail.verified') }}
            </t-tag>
            <t-tag :theme="valueTheme(requirement.value_achieved)" variant="light" size="small">
              {{ $t(valueLabelKey(requirement.value_achieved)) }}
            </t-tag>
            <span v-if="requirement.value_achieved_at" class="rd-muted">
              {{ $t('pm.requirements.detail.verifiedAt', { time: formatPmTime(requirement.value_achieved_at) }) }}
            </span>
          </div>
          <dl v-if="requirement.effect_notes || requirement.retro_notes" class="rd-grid rd-grid--one">
            <div v-if="requirement.effect_notes">
              <dt>{{ $t('pm.requirements.detail.effectNotes') }}</dt>
              <dd class="rd-text">{{ requirement.effect_notes }}</dd>
            </div>
            <div v-if="requirement.retro_notes">
              <dt>{{ $t('pm.requirements.detail.retroNotes') }}</dt>
              <dd class="rd-text">{{ requirement.retro_notes }}</dd>
            </div>
          </dl>
        </template>
        <p v-else class="rd-muted">{{ $t('pm.requirements.detail.verifyLocked') }}</p>
      </section>
    </div>

    <div class="rd-foot">
      <t-button variant="outline" @click="close">{{ $t('pm.requirements.detail.close') }}</t-button>
      <!-- 待分析需求可确认：确认后状态交由版本进度自动流转 -->
      <t-button
        v-if="requirement && canConfirm(requirement.status)"
        theme="primary"
        :loading="confirming"
        @click="submitConfirm"
      >
        {{ $t('pm.requirements.detail.confirm') }}
      </t-button>
    </div>
  </t-drawer>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  downloadPmRequirementAttachment,
  listPmRequirementAttachments,
  updatePmRequirement,
  verifyPmRequirementLaunch,
  type PmRequirement,
  type PmRequirementAttachment,
  type PmValueAchieved,
} from '@/api/pm';
import {
  REQUIREMENT_VALUES,
  canConfirm,
  canVerify,
  formatFileSize,
  formatPmTime,
  isImageAttachment,
  priorityTheme,
  readRequirementAnalysis,
  sourceLabelKey,
  valueLabelKey,
  valueTheme,
} from './pmRequirementState';
import { REQUIREMENT_STATUS_MAP } from './pmStatus';
import PmMarkdown from './components/PmMarkdown.vue';

const props = defineProps<{
  visible: boolean;
  requirement: PmRequirement | null;
}>();
const emit = defineEmits<{ 'update:visible': [value: boolean]; changed: [] }>();

const { t } = useI18n();
// 与后端 LaunchVerificationNoteMaxLen 保持一致
const NOTE_MAX_LEN = 2000;
const saving = ref(false);
const confirming = ref(false);
const verifyForm = reactive<{ value: PmValueAchieved | ''; effectNotes: string; retroNotes: string }>({
  value: '',
  effectNotes: '',
  retroNotes: '',
});
// 附件列表与图片预览的 objectURL（关闭抽屉时统一释放）
const attachments = ref<PmRequirementAttachment[]>([]);
const previews = reactive<Record<string, string>>({});

const statusMeta = computed(() => (
  props.requirement
    ? REQUIREMENT_STATUS_MAP[props.requirement.status]
    : REQUIREMENT_STATUS_MAP.pending_analysis
));

// 只展示有内容的分析字段，避免详情抽屉出现整片空标签
const analysisRows = computed(() => {
  const analysis = readRequirementAnalysis(props.requirement?.ai_analysis);
  const rows: Array<{ key: string; labelKey: string; value: string }> = [
    { key: 'value', labelKey: 'pm.requirements.form.value', value: analysis.value },
    { key: 'impact', labelKey: 'pm.requirements.form.impact', value: analysis.impact },
    { key: 'risk', labelKey: 'pm.requirements.form.risk', value: analysis.risk },
  ];
  return rows.filter((row) => row.value.trim() !== '');
});

watch(
  () => props.visible,
  (visible) => {
    if (!visible) {
      resetAttachments();
      return;
    }
    if (!props.requirement) return;
    verifyForm.value = props.requirement.value_achieved || '';
    verifyForm.effectNotes = props.requirement.effect_notes || '';
    verifyForm.retroNotes = props.requirement.retro_notes || '';
    void loadAttachments(props.requirement.id);
  },
);

// 释放所有预览链接并清空附件状态
function resetAttachments() {
  Object.values(previews).forEach((url) => URL.revokeObjectURL(url));
  Object.keys(previews).forEach((key) => delete previews[key]);
  attachments.value = [];
}

async function loadAttachments(requirementId: string) {
  resetAttachments();
  try {
    attachments.value = await listPmRequirementAttachments(requirementId);
  } catch {
    attachments.value = [];
    return;
  }
  // 图片一次性取回 Blob 生成缩略图，失败时降级为文件行
  await Promise.all(
    attachments.value
      .filter((item) => isImageAttachment(item.file_name))
      .map(async (item) => {
        try {
          const blob = await downloadPmRequirementAttachment(requirementId, item.id);
          previews[item.id] = URL.createObjectURL(blob);
        } catch {
          // 缩略图失败不影响列表展示
        }
      }),
  );
}

async function openAttachment(item: PmRequirementAttachment) {
  const cached = previews[item.id];
  if (cached) {
    window.open(cached, '_blank');
    return;
  }
  try {
    const blob = await downloadPmRequirementAttachment(item.requirement_id, item.id);
    const url = URL.createObjectURL(blob);
    window.open(url, '_blank');
    // 新窗口加载完成后才能释放，延迟回收避免空白页
    window.setTimeout(() => URL.revokeObjectURL(url), 60_000);
  } catch {
    MessagePlugin.error(t('pm.requirements.detail.attachmentOpenFailed'));
  }
}

// 非图片附件展示扩展名角标
function extensionLabel(fileName: string): string {
  const index = fileName.lastIndexOf('.');
  return index >= 0 ? fileName.slice(index + 1).toUpperCase() : 'FILE';
}

onBeforeUnmount(resetAttachments);

async function submitVerify() {
  if (!props.requirement || saving.value) return;
  if (!verifyForm.value) {
    MessagePlugin.warning(t('pm.requirements.detail.valueRequired'));
    return;
  }
  saving.value = true;
  try {
    await verifyPmRequirementLaunch(
      props.requirement.id,
      verifyForm.value,
      verifyForm.effectNotes.trim(),
      verifyForm.retroNotes.trim(),
    );
    MessagePlugin.success(t('pm.requirements.detail.verifySuccess'));
    emit('changed');
  } catch {
    MessagePlugin.error(t('pm.requirements.detail.verifyFailed'));
  } finally {
    saving.value = false;
  }
}

// 确认需求：仅推进到「已确认」，后续状态由版本关联与开发进度自动变更
async function submitConfirm() {
  if (!props.requirement || confirming.value) return;
  confirming.value = true;
  try {
    await updatePmRequirement(props.requirement.id, { status: 'confirmed' });
    MessagePlugin.success(t('pm.requirements.detail.confirmSuccess'));
    emit('changed');
  } catch {
    MessagePlugin.error(t('pm.requirements.detail.confirmFailed'));
  } finally {
    confirming.value = false;
  }
}

function close() {
  emit('update:visible', false);
}
</script>

<style lang="less" scoped>
.rd {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-4);
}

.rd-head {
  min-width: 0;
}

.rd-title {
  margin: 0 0 var(--app-space-2);
  font-size: var(--app-text-xl);
  font-weight: 700;
  color: var(--td-text-color-primary);
}

.rd-tags {
  display: flex;
  flex-wrap: wrap;
  gap: var(--app-space-2);
}

.rd-section {
  padding-bottom: var(--app-space-4);
  border-bottom: 1px dashed var(--td-component-stroke);

  &:last-of-type {
    border-bottom: 0;
  }
}

.rd-section__title {
  margin: 0 0 var(--app-space-2);
  font-size: var(--app-text-md);
  font-weight: 700;
  color: var(--td-text-color-primary);
}

.rd-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: var(--app-space-2) var(--app-space-3);
  margin: 0 0 var(--app-space-2);

  &--one {
    grid-template-columns: 1fr;
  }

  dt {
    margin-bottom: 2px;
    font-size: var(--app-text-xs);
    color: var(--td-text-color-placeholder);
  }

  dd {
    margin: 0;
    font-size: var(--app-text-md);
    color: var(--td-text-color-primary);
  }
}

.rd-text {
  margin: var(--app-space-2) 0 0;
  font-size: var(--app-text-md);
  line-height: 1.7;
  color: var(--td-text-color-secondary);
  white-space: pre-wrap;
  word-break: break-word;
}

.rd-muted {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
}

/* 附件列表：缩略图/扩展名角标 + 文件名与大小 */
.rd-attach {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-2);
  margin: 0;
  padding: 0;
  list-style: none;

  &__item {
    min-width: 0;
  }

  &__file {
    display: flex;
    align-items: center;
    gap: var(--app-space-2);
    width: 100%;
    padding: 6px;
    font-family: inherit;
    text-align: left;
    background: var(--td-bg-color-secondarycontainer);
    border: 1px solid var(--td-component-stroke);
    border-radius: var(--td-radius-default);
    cursor: pointer;

    &:hover {
      border-color: var(--td-brand-color);
    }
  }

  &__thumb {
    flex-shrink: 0;
    width: 40px;
    height: 40px;
    object-fit: cover;
    border-radius: var(--td-radius-small);
  }

  &__icon {
    flex-shrink: 0;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 40px;
    height: 40px;
    font-size: 10px;
    font-weight: 600;
    color: var(--td-text-color-secondary);
    background: var(--td-bg-color-container);
    border-radius: var(--td-radius-small);
  }

  &__meta {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }

  &__name {
    overflow: hidden;
    font-size: var(--app-text-sm);
    color: var(--td-text-color-primary);
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  &__size {
    font-size: var(--app-text-xs);
    color: var(--td-text-color-placeholder);
  }
}

/* 价值达成选择：未选中为中性描边，选中按语义色着色（成功/警告/危险） */
.rd-value {
  display: flex;
  gap: var(--app-space-2);

  &__item {
    min-width: 76px;
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
      color: var(--rd-value-color);
      border-color: var(--rd-value-color);
    }

    &.is-active {
      font-weight: 600;
      color: var(--rd-value-color);
      background: var(--rd-value-bg);
      border-color: var(--rd-value-color);
    }

    &--success {
      --rd-value-bg: var(--td-success-color-1);
      --rd-value-color: var(--td-success-color);
    }

    &--warning {
      --rd-value-bg: var(--td-warning-color-1);
      --rd-value-color: var(--td-warning-color);
    }

    &--danger {
      --rd-value-bg: var(--td-error-color-1);
      --rd-value-color: var(--td-error-color);
    }

    /* 兜底：valueTheme 返回 default 时保持中性灰 */
    &--default {
      --rd-value-bg: var(--td-bg-color-secondarycontainer);
      --rd-value-color: var(--td-gray-color-9);
    }
  }
}

.rd-verify {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  margin-bottom: var(--app-space-2);
}

.rd-foot {
  display: flex;
  justify-content: flex-end;
  gap: var(--app-space-2);
  padding-top: var(--app-space-4);
}
</style>
