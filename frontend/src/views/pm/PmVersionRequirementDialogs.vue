<template>
  <!-- 版本内需求集合操作：取消（移出）/ 搁置 / 变更字段或更换需求 -->
  <div>
    <!-- 取消 · 移出本版本 -->
    <t-dialog
      :visible="action === 'remove'"
      :header="$t('pm.version.dialogs.removeRequirement.title')"
      width="520px"
      placement="center"
      :footer="false"
      @close="close"
    >
      <p class="vrd-hint">
        {{ $t('pm.version.dialogs.removeRequirement.hint', {
          title: requirement?.title || '',
          version: versionLabel,
        }) }}
      </p>
      <div class="vrd-foot">
        <t-button variant="outline" @click="close">{{ $t('pm.version.dialogs.back') }}</t-button>
        <t-button theme="primary" :loading="saving" @click="submitRemove">
          {{ $t('pm.version.dialogs.removeRequirement.submit') }}
        </t-button>
      </div>
    </t-dialog>

    <!-- 搁置 · 评审不通过 -->
    <t-dialog
      :visible="action === 'shelve'"
      :header="$t('pm.version.dialogs.shelveRequirement.title')"
      width="560px"
      placement="center"
      :footer="false"
      @close="close"
    >
      <t-form label-align="top">
        <t-form-item :label="$t('pm.version.dialogs.shelveRequirement.reason')" required-mark>
          <t-textarea
            v-model="shelveReason"
            :autosize="{ minRows: 6, maxRows: 6 }"
            :maxlength="200"
            :placeholder="$t('pm.version.dialogs.shelveRequirement.reasonPlaceholder')"
          />
        </t-form-item>
      </t-form>
      <p class="vrd-hint">{{ $t('pm.version.dialogs.shelveRequirement.impacts') }}</p>
      <div class="vrd-foot">
        <t-button variant="outline" @click="close">{{ $t('pm.version.dialogs.back') }}</t-button>
        <t-button theme="primary" :loading="saving" @click="submitShelve">
          {{ $t('pm.version.dialogs.shelveRequirement.submit') }}
        </t-button>
      </div>
    </t-dialog>

    <!-- 变更需求 -->
    <t-dialog
      :visible="action === 'change'"
      :header="$t('pm.version.dialogs.changeRequirement.title')"
      width="640px"
      placement="center"
      :footer="false"
      @close="close"
    >
      <t-form label-align="top">
        <t-form-item :label="$t('pm.version.dialogs.changeRequirement.mode')" required-mark>
          <t-radio-group v-model="changeMode" variant="default-filled">
            <t-radio-button value="fields">
              {{ $t('pm.version.dialogs.changeRequirement.modeFields') }}
            </t-radio-button>
            <t-radio-button value="replace">
              {{ $t('pm.version.dialogs.changeRequirement.modeReplace') }}
            </t-radio-button>
          </t-radio-group>
        </t-form-item>

        <!-- 变更字段 -->
        <template v-if="changeMode === 'fields'">
          <t-form-item :label="$t('pm.version.dialogs.changeRequirement.priority')">
            <t-select v-model="changePriority" :options="priorityOptions" />
          </t-form-item>
          <t-form-item :label="$t('pm.version.dialogs.changeRequirement.description')">
            <t-textarea
              v-model="changeDescription"
              :autosize="{ minRows: 8, maxRows: 8 }"
              :maxlength="2000"
              :placeholder="$t('pm.version.dialogs.changeRequirement.descriptionPlaceholder')"
            />
          </t-form-item>
        </template>

        <!-- 更换需求：仅可选项为项目内「已确认」需求 -->
        <t-form-item
          v-else
          :label="$t('pm.version.dialogs.changeRequirement.replacement')"
          required-mark
        >
          <t-select
            v-model="replacementId"
            :options="replacementOptions"
            :placeholder="$t('pm.version.dialogs.changeRequirement.replacementPlaceholder')"
            :loading="loadingOptions"
          />
          <p v-if="!loadingOptions && !replacementOptions.length" class="vrd-muted">
            {{ $t('pm.version.dialogs.changeRequirement.replacementEmpty') }}
          </p>
        </t-form-item>
      </t-form>
      <p class="vrd-hint">{{ $t('pm.version.dialogs.changeRequirement.hint') }}</p>
      <div class="vrd-foot">
        <t-button variant="outline" @click="close">{{ $t('pm.version.dialogs.back') }}</t-button>
        <t-button theme="primary" :loading="saving" @click="submitChange">
          {{ $t('pm.version.dialogs.changeRequirement.submit') }}
        </t-button>
      </div>
    </t-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  changePmVersionRequirement,
  listPmRequirements,
  removePmVersionRequirement,
  replacePmVersionRequirement,
  shelvePmVersionRequirement,
  type PmPriority,
  type PmRequirement,
  type PmVersion,
} from '@/api/pm';
import { REQUIREMENT_PRIORITIES } from './pmRequirementState';
import { versionDisplayNo } from './pmVersionState';

type RequirementDialogAction = '' | 'remove' | 'shelve' | 'change';

const emit = defineEmits<{ changed: [] }>();
const { t } = useI18n();

const action = ref<RequirementDialogAction>('');
const version = ref<PmVersion | null>(null);
const requirement = ref<PmRequirement | null>(null);
const saving = ref(false);

const shelveReason = ref('');
const changeMode = ref<'fields' | 'replace'>('fields');
const changePriority = ref<PmPriority>('P2');
const changeDescription = ref('');
const replacementId = ref('');
const loadingOptions = ref(false);
const confirmedRequirements = ref<PmRequirement[]>([]);

const versionLabel = computed(() => (
  version.value ? versionDisplayNo(version.value) || t('pm.version.unfinalized') : ''
));

const priorityOptions = REQUIREMENT_PRIORITIES.map((value) => ({ label: value, value }));

// 更换需求时排除当前需求本身
const replacementOptions = computed(() => confirmedRequirements.value
  .filter((item) => item.id !== requirement.value?.id)
  .map((item) => ({ label: `${item.title}（${item.priority}）`, value: item.id })));

function open(next: Exclude<RequirementDialogAction, ''>, targetVersion: PmVersion, target: PmRequirement) {
  version.value = targetVersion;
  requirement.value = target;
  saving.value = false;
  shelveReason.value = '';
  changeMode.value = 'fields';
  changePriority.value = target.priority || 'P2';
  changeDescription.value = target.description || '';
  replacementId.value = '';
  action.value = next;
  if (next === 'change') void loadConfirmed();
}

function close() {
  action.value = '';
}

async function loadConfirmed() {
  const projectId = version.value?.project_id;
  if (!projectId) return;
  loadingOptions.value = true;
  try {
    confirmedRequirements.value = await listPmRequirements(projectId, { status: 'confirmed' });
  } catch {
    confirmedRequirements.value = [];
  } finally {
    loadingOptions.value = false;
  }
}

async function submitRemove() {
  if (!version.value || !requirement.value || saving.value) return;
  saving.value = true;
  try {
    await removePmVersionRequirement(version.value.id, requirement.value.id);
    MessagePlugin.success(t('pm.version.dialogs.removeRequirement.success'));
    close();
    emit('changed');
  } catch {
    MessagePlugin.error(t('pm.version.dialogs.removeRequirement.failed'));
  } finally {
    saving.value = false;
  }
}

async function submitShelve() {
  if (!version.value || !requirement.value || saving.value) return;
  const reason = shelveReason.value.trim();
  if (!reason) {
    MessagePlugin.warning(t('pm.version.dialogs.shelveRequirement.errReason'));
    return;
  }
  saving.value = true;
  try {
    await shelvePmVersionRequirement(version.value.id, requirement.value.id, reason);
    MessagePlugin.success(t('pm.version.dialogs.shelveRequirement.success'));
    close();
    emit('changed');
  } catch {
    MessagePlugin.error(t('pm.version.dialogs.shelveRequirement.failed'));
  } finally {
    saving.value = false;
  }
}

async function submitChange() {
  if (!version.value || !requirement.value || saving.value) return;
  saving.value = true;
  try {
    if (changeMode.value === 'replace') {
      if (!replacementId.value) {
        MessagePlugin.warning(t('pm.version.dialogs.changeRequirement.errReplacement'));
        return;
      }
      await replacePmVersionRequirement(
        version.value.id, requirement.value.id, replacementId.value,
      );
    } else {
      const priority = changePriority.value !== requirement.value.priority
        ? changePriority.value
        : '';
      const description = changeDescription.value.trim();
      const changedDescription = description !== (requirement.value.description || '').trim();
      // 两个字段都未变化时不发请求，避免产生无意义的「变更」记录
      if (!priority && !changedDescription) {
        MessagePlugin.warning(t('pm.version.dialogs.changeRequirement.noChange'));
        return;
      }
      await changePmVersionRequirement(version.value.id, requirement.value.id, {
        ...(priority ? { priority } : {}),
        // 空串在后端表示保持原值，因此仅在确有内容时提交
        ...(description ? { description } : {}),
      });
    }
    MessagePlugin.success(t('pm.version.dialogs.changeRequirement.success'));
    close();
    emit('changed');
  } catch {
    MessagePlugin.error(t('pm.version.dialogs.changeRequirement.failed'));
  } finally {
    saving.value = false;
  }
}

defineExpose({ open });
</script>

<style lang="less" scoped>
.vrd-foot {
  display: flex;
  justify-content: flex-end;
  gap: var(--app-space-2);
  margin-top: var(--app-space-4);
}

.vrd-hint {
  margin: 0;
  padding: var(--app-space-3) var(--app-space-4);
  border-radius: var(--app-radius-md);
  background: var(--td-bg-color-page);
  font-size: var(--app-text-sm);
  line-height: 1.8;
  color: var(--td-text-color-secondary);
}

.vrd-muted {
  margin: var(--app-space-1) 0 0;
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}
</style>
