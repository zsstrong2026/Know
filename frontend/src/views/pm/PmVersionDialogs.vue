<template>
  <!-- 版本级弹窗集合：记录评审意见 / 定稿 / 生成或重新生成产物 / 搁置 -->
  <div>
    <!-- 记录评审意见 -->
    <t-dialog
      :visible="action === 'review'"
      :header="$t('pm.version.dialogs.review.title')"
      width="620px"
      placement="center"
      :footer="false"
      @close="close"
    >
      <t-form label-align="top">
        <t-form-item :label="$t('pm.version.dialogs.review.round')">
          <t-input :value="$t('pm.version.dialogs.review.roundAuto', { round: nextRound })" disabled />
        </t-form-item>
        <t-form-item :label="$t('pm.version.dialogs.review.conclusion')" required-mark>
          <t-radio-group v-model="reviewForm.conclusion" class="vd-conclusion" variant="default-filled">
            <t-radio-button
              v-for="item in VERSION_CONCLUSIONS"
              :key="item"
              :value="item"
              :class="`vd-conclusion__item vd-conclusion__item--${item}`"
            >
              {{ $t(`pm.version.conclusion.${item}`) }}
            </t-radio-button>
          </t-radio-group>
        </t-form-item>
        <t-form-item :label="$t('pm.version.dialogs.review.reviewers')">
          <t-input
            v-model="reviewForm.reviewers"
            :maxlength="100"
            :placeholder="$t('pm.version.dialogs.review.reviewersPlaceholder')"
          />
        </t-form-item>
        <t-form-item :label="$t('pm.version.dialogs.review.opinions')" required-mark>
          <t-textarea
            v-model="reviewForm.opinions"
            :autosize="{ minRows: 8, maxRows: 8 }"
            :maxlength="1000"
            :placeholder="$t('pm.version.dialogs.review.opinionsPlaceholder')"
          />
        </t-form-item>
        <t-form-item :label="$t('pm.version.dialogs.review.changeNote')">
          <t-textarea
            v-model="reviewForm.changeNote"
            :autosize="{ minRows: 6, maxRows: 6 }"
            :maxlength="1000"
            :placeholder="$t('pm.version.dialogs.review.changeNotePlaceholder')"
          />
        </t-form-item>
      </t-form>
      <div class="vd-foot">
        <t-button variant="outline" @click="close">{{ $t('pm.version.dialogs.cancel') }}</t-button>
        <t-button theme="primary" :loading="saving" @click="submitReview">
          {{ $t('pm.version.dialogs.review.submit') }}
        </t-button>
      </div>
    </t-dialog>

    <!-- 定稿 -->
    <t-dialog
      :visible="action === 'finalize'"
      :header="$t('pm.version.dialogs.finalize.title')"
      width="620px"
      placement="center"
      :footer="false"
      @close="close"
    >
      <t-form label-align="top">
        <t-form-item :label="$t('pm.version.dialogs.finalize.versionNo')" required-mark>
          <t-input
            v-model="finalizeForm.versionNo"
            :placeholder="$t('pm.version.dialogs.finalize.versionNoPlaceholder')"
          />
        </t-form-item>
        <t-form-item :label="$t('pm.version.dialogs.finalize.outline')" required-mark>
          <t-textarea
            v-model="finalizeForm.outline"
            :autosize="{ minRows: 10, maxRows: 10 }"
            :maxlength="2000"
            :placeholder="$t('pm.version.dialogs.finalize.outlinePlaceholder')"
          />
        </t-form-item>
      </t-form>
      <p class="vd-warn">
        {{ $t('pm.version.dialogs.finalize.pendingWarning', { count: pendingOpinions }) }}
      </p>
      <p class="vd-hint">{{ $t('pm.version.dialogs.finalize.hint') }}</p>
      <div class="vd-foot">
        <t-button variant="outline" @click="close">{{ $t('pm.version.dialogs.cancel') }}</t-button>
        <t-button theme="primary" :loading="saving" @click="submitFinalize">
          {{ $t('pm.version.dialogs.finalize.submit') }}
        </t-button>
      </div>
    </t-dialog>

    <!-- 生成 / 重新生成确认 -->
    <t-dialog
      :visible="action === 'generate' || action === 'regenerate'"
      :header="$t(`pm.version.dialogs.${action}.title`)"
      width="520px"
      placement="center"
      :footer="false"
      @close="close"
    >
      <p class="vd-hint vd-hint--block">{{ $t(`pm.version.dialogs.${action}.hint`) }}</p>
      <div class="vd-chapters">
        <div class="vd-chapters__head">
          <span class="vd-chapters__label">{{ $t('pm.version.dialogs.chapters.label') }}</span>
          <span class="vd-chapters__hint">{{ $t('pm.version.dialogs.chapters.hint') }}</span>
        </div>
        <t-checkbox-group v-model="selectedChapters" class="vd-chapters__group">
          <t-checkbox v-for="item in PRD_CHAPTERS" :key="item" :value="item">
            {{ $t(`pm.version.dialogs.chapters.items.${item}`) }}
          </t-checkbox>
        </t-checkbox-group>
      </div>
      <div class="vd-foot">
        <t-button variant="outline" @click="close">{{ $t('pm.version.dialogs.cancel') }}</t-button>
        <t-button theme="primary" :loading="saving" @click="submitGenerate">
          {{ $t(`pm.version.dialogs.${action}.submit`) }}
        </t-button>
      </div>
    </t-dialog>

    <!-- 产物生成进度：复用统一任务轮询组件 -->
    <t-dialog
      :visible="action === 'task'"
      :header="$t('pm.version.dialogs.taskTitle')"
      width="520px"
      placement="center"
      :footer="false"
      @close="close"
    >
      <PmTaskProgress :task-id="taskId" @done="onTaskDone" @failed="onTaskFailed" />
      <div class="vd-foot">
        <t-button variant="outline" @click="close">{{ $t('pm.version.dialogs.close') }}</t-button>
      </div>
    </t-dialog>

    <!-- 版本搁置确认 -->
    <t-dialog
      :visible="action === 'shelve'"
      :header="$t('pm.version.dialogs.shelve.title')"
      width="520px"
      placement="center"
      :footer="false"
      @close="close"
    >
      <p class="vd-hint vd-hint--block">{{ $t('pm.version.dialogs.shelve.hint') }}</p>
      <div class="vd-foot">
        <t-button variant="outline" @click="close">{{ $t('pm.version.dialogs.cancel') }}</t-button>
        <t-button theme="primary" :loading="saving" @click="submitShelve">
          {{ $t('pm.version.dialogs.shelve.submit') }}
        </t-button>
      </div>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  finalizePmVersion,
  generatePmVersionArtifacts,
  reviewPmVersion,
  updatePmVersion,
  type PmReviewConclusion,
  type PmVersion,
} from '@/api/pm';
import {
  VERSION_CONCLUSIONS,
  countPendingOpinions,
  normalizeVersionNo,
  readReviewRounds,
} from './pmVersionState';
import PmTaskProgress from './components/PmTaskProgress.vue';

type VersionDialogAction = '' | 'review' | 'finalize' | 'generate' | 'regenerate' | 'task' | 'shelve';
type VersionDialogRequest = Exclude<VersionDialogAction, '' | 'task'>;

// PRD 章节 key：与后端 pmPRDChapters 固定顺序保持一致
const PRD_CHAPTERS = [
  'overview', 'requirements', 'features', 'interaction',
  'data_api', 'nonfunctional', 'acceptance', 'review',
] as const;

const emit = defineEmits<{ changed: [] }>();
const { t } = useI18n();

const action = ref<VersionDialogAction>('');
const version = ref<PmVersion | null>(null);
const saving = ref(false);
const taskId = ref('');
// 默认全选：用户可按章节裁剪本次生成范围
const selectedChapters = ref<string[]>([...PRD_CHAPTERS]);

const reviewForm = reactive({
  conclusion: '' as PmReviewConclusion | '',
  reviewers: '',
  opinions: '',
  changeNote: '',
});
const finalizeForm = reactive({ versionNo: '', outline: '' });

// 评审轮次由后端自动递增，弹窗只做展示
const nextRound = computed(() => (version.value?.review_round ?? 0) + 1);
const pendingOpinions = computed(() => (
  countPendingOpinions(readReviewRounds(version.value?.review_opinions))
));

// 父组件通过 open(action, version) 打开对应弹窗
function open(next: VersionDialogRequest, target: PmVersion) {
  version.value = target;
  saving.value = false;
  if (next === 'review') {
    reviewForm.conclusion = '';
    reviewForm.reviewers = target.reviewers || '';
    reviewForm.opinions = '';
    reviewForm.changeNote = '';
  } else if (next === 'finalize') {
    finalizeForm.versionNo = target.version_no ? normalizeVersionNo(target.version_no) : '';
    finalizeForm.outline = target.update_outline || '';
  } else if (next === 'generate' || next === 'regenerate') {
    selectedChapters.value = [...PRD_CHAPTERS];
  }
  action.value = next;
}

function close() {
  action.value = '';
  taskId.value = '';
}

// 评审意见按行拆分为多条，便于详情页逐条跟踪处理状态
function splitOpinions(raw: string) {
  return raw
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
    .map((text) => ({ text, status: 'pending', note: '' }));
}

async function submitReview() {
  if (!version.value || saving.value) return;
  if (!reviewForm.conclusion || !reviewForm.opinions.trim()) {
    MessagePlugin.warning(t('pm.version.dialogs.review.required'));
    return;
  }
  saving.value = true;
  try {
    await reviewPmVersion(version.value.id, {
      conclusion: reviewForm.conclusion,
      reviewers: reviewForm.reviewers.trim(),
      opinions: {
        items: splitOpinions(reviewForm.opinions),
        change_note: reviewForm.changeNote.trim(),
      },
    });
    MessagePlugin.success(t('pm.version.dialogs.review.success'));
    close();
    emit('changed');
  } catch {
    MessagePlugin.error(t('pm.version.dialogs.review.failed'));
  } finally {
    saving.value = false;
  }
}

async function submitFinalize() {
  if (!version.value || saving.value) return;
  const versionNo = finalizeForm.versionNo.trim();
  // 人工填写时校验 X.Y.Z 格式；留空则由后端按 minor 自动递增
  if (versionNo && !normalizeVersionNo(versionNo)) {
    MessagePlugin.warning(t('pm.version.dialogs.finalize.errVersionNo'));
    return;
  }
  if (!finalizeForm.outline.trim()) {
    MessagePlugin.warning(t('pm.version.dialogs.finalize.errOutline'));
    return;
  }
  saving.value = true;
  try {
    await finalizePmVersion(version.value.id, {
      version_no: versionNo,
      update_outline: finalizeForm.outline.trim(),
    });
    MessagePlugin.success(t('pm.version.dialogs.finalize.success'));
    close();
    emit('changed');
  } catch {
    MessagePlugin.error(t('pm.version.dialogs.finalize.failed'));
  } finally {
    saving.value = false;
  }
}

async function submitGenerate() {
  if (!version.value || saving.value) return;
  if (selectedChapters.value.length === 0) {
    MessagePlugin.warning(t('pm.version.dialogs.chapters.required'));
    return;
  }
  saving.value = true;
  try {
    const task = await generatePmVersionArtifacts(
      version.value.id,
      action.value === 'regenerate',
      [...selectedChapters.value],
    );
    taskId.value = task.id;
    action.value = 'task';
  } catch {
    MessagePlugin.error(t('pm.version.dialogs.generate.failed'));
  } finally {
    saving.value = false;
  }
}

function onTaskDone() {
  MessagePlugin.success(t('pm.version.dialogs.generate.success'));
  emit('changed');
}

function onTaskFailed() {
  MessagePlugin.error(t('pm.version.dialogs.generate.failed'));
}

async function submitShelve() {
  if (!version.value || saving.value) return;
  saving.value = true;
  try {
    await updatePmVersion(version.value.id, { status: 'shelved' });
    MessagePlugin.success(t('pm.version.dialogs.shelve.success'));
    close();
    emit('changed');
  } catch {
    MessagePlugin.error(t('pm.version.dialogs.shelve.failed'));
  } finally {
    saving.value = false;
  }
}

defineExpose({ open });
</script>

<style lang="less" scoped>
/* 评审结论：隐藏原生滑块，选中项按结论语义填充实色，强化选中识别度 */
.vd-conclusion {
  display: flex;
  width: 100%;
  gap: var(--app-space-1);
  padding: 4px;
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-secondarycontainer);

  // 原生滑块无法按选项分色，改为由选中项自身绘制背景
  :deep(.t-radio-group__bg-block) {
    display: none;
  }

  // 去掉相邻选项之间的 1px 分隔线
  :deep(.t-radio-button::before) {
    display: none;
  }

  :deep(.t-radio-button) {
    flex: 1 1 0;
    min-width: 0;
    border-radius: 6px;
    background: transparent;
    color: var(--td-text-color-secondary);
    transition: color 0.2s ease, background-color 0.2s ease;
  }

  :deep(.t-radio-button:not(.t-is-checked):hover) {
    color: var(--td-text-color-primary);
    background: var(--td-bg-color-container);
  }

  // 选中态公共部分：白色文字加粗，底色按语义分色
  :deep(.t-radio-button.t-is-checked) {
    color: var(--td-text-color-anti);
    font-weight: 600;
  }

  :deep(.t-radio-button.vd-conclusion__item--pass.t-is-checked) {
    background: var(--td-brand-color);
  }

  :deep(.t-radio-button.vd-conclusion__item--conditional.t-is-checked) {
    background: var(--td-warning-color);
  }

  :deep(.t-radio-button.vd-conclusion__item--reject.t-is-checked) {
    background: var(--td-error-color);
  }
}

.vd-foot {
  display: flex;
  justify-content: flex-end;
  gap: var(--app-space-2);
  margin-top: var(--app-space-4);
}

.vd-hint {
  margin: 0;
  font-size: var(--app-text-sm);
  line-height: 1.8;
  color: var(--td-text-color-secondary);

  &--block {
    padding: var(--app-space-3) var(--app-space-4);
    border-radius: var(--app-radius-md);
    background: var(--td-bg-color-page);
  }
}

.vd-warn {
  margin: 0 0 var(--app-space-2);
  font-size: var(--app-text-sm);
  color: var(--td-error-color);
}

// 章节勾选：两列排布，避免 8 个选项纵向拉长弹窗
.vd-chapters {
  margin-top: var(--app-space-3);
}

.vd-chapters__head {
  display: flex;
  align-items: baseline;
  gap: var(--app-space-2);
  margin-bottom: var(--app-space-2);
}

.vd-chapters__label {
  font-size: var(--app-text-sm);
  font-weight: 500;
  color: var(--td-text-color-primary);
}

.vd-chapters__hint {
  font-size: var(--app-text-xs);
  color: var(--td-text-color-secondary);
}

.vd-chapters__group {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--app-space-1) var(--app-space-3);
}
</style>
