<template>
  <!-- 会议详情弹窗：会议设置总入口（标题/描述/参会智能体/讨论模式/路由/护栏/介入/共识） -->
  <t-dialog :visible="visible" :header="$t('meeting.detailTitle')" :width="640"
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
      <t-form-item :label="$t('meeting.broadcastMode')">
        <t-radio-group v-model="broadcastMode" :disabled="isArchived">
          <t-radio value="sequential">{{ $t('meeting.modeSequential') }}</t-radio>
          <t-radio value="parallel">{{ $t('meeting.modeParallel') }}</t-radio>
        </t-radio-group>
      </t-form-item>
      <t-form-item :label="$t('meeting.routingMode')">
        <t-radio-group v-model="routingMode" :disabled="isArchived">
          <t-radio value="host-assign">{{ $t('meeting.routingHostAssign') }}</t-radio>
          <t-radio value="mention">{{ $t('meeting.routingMention') }}</t-radio>
          <t-radio value="free">{{ $t('meeting.routingFree') }}</t-radio>
        </t-radio-group>
      </t-form-item>
      <t-form-item :label="$t('meeting.guardMaxExchange')">
        <t-input-number v-model="form.guardMaxExchange" :min="1" :max="10" theme="column"
          :disabled="isArchived" />
      </t-form-item>
      <t-form-item :label="$t('meeting.hostEveryNRounds')">
        <t-select v-model="form.hostEveryNRounds" :disabled="isArchived">
          <t-option :value="0" :label="$t('meeting.hostEveryNRoundsOff')" />
          <t-option v-for="n in HOST_INTERVENE_OPTIONS" :key="n" :value="n"
            :label="$t('meeting.hostInterveneEvery', { n })" />
        </t-select>
      </t-form-item>
      <t-form-item :label="$t('meeting.consensusDetection')">
        <t-switch v-model="form.consensusDetectionEnabled" :disabled="isArchived" />
      </t-form-item>
    </t-form>
  </t-dialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { MessagePlugin } from 'tdesign-vue-next';
import {
  getMeeting, replaceParticipants, updateMeeting,
  type BroadcastMode, type RoutingMode, type MeetingParticipant, type MeetingRoom,
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

const HOST_INTERVENE_OPTIONS = [2, 3, 5, 10];

const form = reactive({
  title: '', description: '',
  agentIds: [] as string[],
  broadcastMode: 'sequential' as BroadcastMode,
  routingMode: 'host-assign' as RoutingMode,
  guardMaxExchange: 3,
  hostEveryNRounds: 0,
  consensusDetectionEnabled: false,
});

const isArchived = computed(() => props.room?.status === 'archived');
// 参会者回填状态：卡片列表对象不带 participants，须补拉详情成功后才能提交，
// 否则保存会走「全量替换」把原有参会人清空。idle 表示参会者未知且尚未发起补拉。
const detailState = ref<'idle' | 'loading' | 'ready' | 'failed'>('idle');
const canSubmit = computed(() => !isArchived.value && detailState.value === 'ready');

// t-radio-group 变更值类型较宽，用带类型 setter 的 computed 收敛回联合类型
const broadcastMode = computed<BroadcastMode>({
  get: () => form.broadcastMode,
  set: (val) => { form.broadcastMode = val; },
});
const routingMode = computed<RoutingMode>({
  get: () => form.routingMode,
  set: (val) => { form.routingMode = val; },
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

// 用房间对象回填全部设置字段
function applyRoom(room: MeetingRoom) {
  form.title = room.title;
  form.description = room.description ?? '';
  form.broadcastMode = room.broadcast_mode ?? 'sequential';
  form.routingMode = room.routing_mode ?? 'host-assign';
  form.guardMaxExchange = room.guard_max_exchange ?? 3;
  form.hostEveryNRounds = room.host_every_n_rounds ?? 0;
  form.consensusDetectionEnabled = room.consensus_detection_enabled ?? false;
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
    // 设置类字段走 PATCH 白名单（后端要求 title/broadcast_mode/guard_max_exchange 必填）
    await updateMeeting(props.room.id, {
      title: form.title.trim(),
      description: form.description,
      broadcast_mode: form.broadcastMode,
      routing_mode: form.routingMode,
      guard_max_exchange: form.guardMaxExchange,
      host_every_n_rounds: form.hostEveryNRounds,
      consensus_detection_enabled: form.consensusDetectionEnabled,
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
</style>
