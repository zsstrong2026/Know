<template>
  <!-- 会议详情弹窗：只维护名称、补充背景与参会智能体；讨论规则由系统统一内置 -->
  <t-dialog :visible="visible" :header="$t('meeting.detailTitle')" :width="640"
    dialog-class-name="meeting-form-dialog"
    :close-on-overlay-click="false" attach="body"
    :confirm-btn="{ content: $t('meeting.save'), loading: saving, disabled: !canSubmit }"
    :cancel-btn="{ content: $t('meeting.cancel') }" @confirm="submit" @close="emit('update:visible', false)">
    <t-form :data="form" :label-width="130" class="detail-form">
      <t-form-item :label="$t('meeting.title')" required>
        <t-input v-model="form.title" :maxlength="100" :disabled="isArchived" />
      </t-form-item>
      <t-form-item :label="$t('meeting.topic')">
        <t-textarea v-model="form.description" :autosize="{ minRows: 2, maxRows: 4 }"
          :disabled="isArchived" :placeholder="$t('meeting.topicPlaceholder')" />
      </t-form-item>
      <t-form-item :label="$t('meeting.selectAgents')" required>
        <t-select v-model="form.agentIds" multiple filterable
          :disabled="isArchived || detailState !== 'ready'"
          :placeholder="$t('meeting.selectAgents')">
          <t-option v-for="a in agents" :key="a.id" :value="a.id" :label="a.name" />
        </t-select>
        <div v-if="participantsLockedHint" class="field-hint">{{ $t('meeting.participantsLocked') }}</div>
        <!-- 详情补拉失败：参会人未知，禁止提交并提供重试，避免以空列表覆盖原有参会人 -->
        <div v-else-if="detailState === 'failed'" class="field-hint">
          {{ $t('meeting.loadFailed') }}
          <a class="field-hint-link" @click="loadDetail">{{ $t('meeting.retry') }}</a>
        </div>
      </t-form-item>
      <p class="rules-hint">{{ $t('meeting.autoRulesHint') }}</p>
      <!-- 旧会议升级入口：不静默迁移，由用户显式切换为当前的一整套默认规则 -->
      <div v-if="needsRuleUpgrade" class="upgrade-rules">
        <t-button size="small" theme="default" variant="outline"
          :loading="upgrading" :disabled="saving || isArchived" @click="upgradeRules">
          {{ $t('meeting.upgradeRules') }}
        </t-button>
        <span class="field-hint">{{ $t('meeting.upgradeRulesHint') }}</span>
      </div>
    </t-form>
  </t-dialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  getMeeting, replaceParticipants, updateMeeting,
  type MeetingParticipant, type MeetingRoom,
} from '@/api/meeting';
import { listAgents, type CustomAgent } from '@/api/agent';

const props = defineProps<{ visible: boolean; room: MeetingRoom | null }>();
const emit = defineEmits<{ 'update:visible': [boolean]; saved: [] }>();

const { t } = useI18n();
const agents = ref<CustomAgent[]>([]);
const saving = ref(false);
// 409 提示为一次性状态：提交被拒后置位，改动后清除
const participantsLockedHint = ref(false);
let optionsLoaded = false;

const form = reactive({
  title: '',
  description: '',
  agentIds: [] as string[],
});

const isArchived = computed(() => props.room?.status === 'archived');
// 参会者回填状态：卡片列表对象不带 participants，须补拉详情成功后才能提交，
// 否则保存会走「全量替换」把原有参会人清空。idle 表示参会者未知且尚未发起补拉。
const detailState = ref<'idle' | 'loading' | 'ready' | 'failed'>('idle');
const canSubmit = computed(() => !isArchived.value && detailState.value === 'ready');
const upgrading = ref(false);
// 旧会议仍在使用关闭态/旧路由：给出显式升级入口，不做静默迁移
const needsRuleUpgrade = computed(() => {
  const r = props.room;
  if (!r || isArchived.value) return false;
  // 自由模式本就不使用主持人治理，不能按主持模式默认值提示升级
  if (r.mode === 'free') return false;
  return !r.consensus_detection_enabled
    || !r.agenda_enabled
    || !r.web_search_enabled
    || r.routing_mode !== 'host-assign'
    || r.host_every_n_rounds !== 0
    || r.admission_mode !== 'observe'
    || r.broadcast_mode !== 'sequential';
});

let initialAgentIds: string[] = [];

// 每次打开：用房间当前值回填表单，并懒加载智能体选项
watch(() => props.visible, (v) => {
  if (!v || !props.room) return;
  applyRoom(props.room);
  participantsLockedHint.value = false;
  void loadOptions();
  void loadDetail();
});

// 只回填用户可维护的字段；讨论规则不再展示
function applyRoom(room: MeetingRoom) {
  form.title = room.title;
  form.description = room.description ?? '';
  setAgentIds(room.participants);
  // 详情接口已带出参会者时直接可用，否则等 loadDetail 补拉
  detailState.value = room.participants ? 'ready' : 'idle';
}

// 参会者回填：列表卡片对象不带 participants，故打开时补拉一次详情
async function loadDetail() {
  const room = props.room;
  if (!room || room.participants) return; // 已由详情接口带出则无需重复请求
  const roomId = room.id; // 捕获本次请求对应的房间，用于丢弃切换房间后的陈旧响应
  detailState.value = 'loading';
  try {
    const detail = await getMeeting(roomId);
    if (props.room?.id !== roomId || !props.visible) return;
    setAgentIds(detail.participants);
    detailState.value = 'ready';
  } catch {
    if (props.room?.id !== roomId || !props.visible) return;
    detailState.value = 'failed';
    MessagePlugin.error(t('meeting.loadFailed'));
  }
}

// 仅取 agent 型参会者（主持人不可移除）；记录初始值用于变更判定
function setAgentIds(participants?: MeetingParticipant[]) {
  form.agentIds = (participants ?? [])
    .filter((p) => p.kind === 'agent' && p.agent_id)
    .map((p) => p.agent_id);
  initialAgentIds = [...form.agentIds];
}

watch(() => form.agentIds, () => { participantsLockedHint.value = false; });

async function loadOptions() {
  if (optionsLoaded) return;
  try {
    const res = await listAgents();
    agents.value = res?.data ?? [];
    optionsLoaded = true;
  } catch {
    MessagePlugin.error(t('meeting.loadFailed'));
  }
}

// 旧会议显式升级：只重置治理规则，不动名称/描述/成员
async function upgradeRules() {
  if (!props.room || isArchived.value) return;
  upgrading.value = true;
  try {
    await updateMeeting(props.room.id, {
      title: form.title.trim() || props.room.title,
      description: form.description,
      reset_rules: true,
    });
    MessagePlugin.success(t('meeting.upgradeRulesDone'));
    emit('update:visible', false);
    emit('saved');
  } catch (e) {
    MessagePlugin.error((e as Error)?.message || t('meeting.saveFailed'));
  } finally {
    upgrading.value = false;
  }
}

async function submit() {
  if (!props.room) return;
  if (!canSubmit.value) return; // 参会人未就绪（加载中/加载失败）时不允许提交
  if (!form.title.trim()) {
    MessagePlugin.warning(t('meeting.title'));
    return;
  }
  if (!form.agentIds.length) {
    MessagePlugin.warning(t('meeting.selectAgents'));
    return;
  }
  saving.value = true;
  try {
    // 参会人变更走独立端点；仅在确实发生增删时触发
    const changed = form.agentIds.length !== initialAgentIds.length
      || form.agentIds.some((id) => !initialAgentIds.includes(id));
    if (changed) {
      try {
        await replaceParticipants(props.room.id, form.agentIds);
      } catch (e) {
        // 讨论进行中：后端返回 409，仅提示并保留用户输入，不关闭弹窗
        if ((e as { status?: number })?.status === 409) {
          participantsLockedHint.value = true;
          return;
        }
        throw e;
      }
    }
    // 设置类字段只提交标题与描述；其余治理参数由创建时的内置规则决定
    await updateMeeting(props.room.id, {
      title: form.title.trim(),
      description: form.description,
    });
    emit('update:visible', false);
    emit('saved');
  } catch (e) {
    MessagePlugin.error((e as Error)?.message || t('meeting.saveFailed'));
  } finally {
    saving.value = false;
  }
}
</script>

<style lang="less" scoped>
.detail-form {
  padding-top: var(--app-space-2);
}

.field-hint {
  margin-top: 6px;
  font-size: 12px;
  line-height: 18px;
  color: var(--td-warning-color);
}

.field-hint-link {
  margin-left: 6px;
  color: var(--td-brand-color);
  cursor: pointer;
}

.rules-hint {
  margin: 4px 0 0;
  font-size: 12px;
  line-height: 18px;
  color: var(--td-text-color-placeholder);
}

.upgrade-rules {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 4px;
}
</style>
