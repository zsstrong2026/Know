<template>
  <!-- 会议详情弹窗：会议设置总入口（标题/描述/参会智能体/讨论模式/路由/护栏/介入/共识） -->
  <t-dialog :visible="visible" :header="$t('meeting.detailTitle')" :width="640"
    :close-on-overlay-click="false" attach="body"
    :confirm-btn="{ content: $t('meeting.save'), loading: saving, disabled: isArchived }"
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
        <t-select v-model="form.agentIds" multiple filterable :disabled="isArchived"
          :placeholder="$t('meeting.selectAgents')">
          <t-option v-for="a in agents" :key="a.id" :value="a.id" :label="a.name" />
        </t-select>
        <div v-if="participantsLockedHint" class="field-hint">{{ $t('meeting.participantsLocked') }}</div>
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
  replaceParticipants, updateMeeting,
  type BroadcastMode, type RoutingMode, type MeetingRoom,
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
  form.title = props.room.title;
  form.description = props.room.description ?? '';
  form.agentIds = (props.room.participants ?? [])
    .filter((p) => p.kind === 'agent' && p.agent_id)
    .map((p) => p.agent_id);
  initialAgentIds = [...form.agentIds];
  form.broadcastMode = props.room.broadcast_mode ?? 'sequential';
  form.routingMode = props.room.routing_mode ?? 'host-assign';
  form.guardMaxExchange = props.room.guard_max_exchange ?? 3;
  form.hostEveryNRounds = props.room.host_every_n_rounds ?? 0;
  form.consensusDetectionEnabled = props.room.consensus_detection_enabled ?? false;
  participantsLockedHint.value = false;
  void loadOptions();
});

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
        const msg = (e as Error)?.message || '';
        if (msg.includes('409') || msg.includes('讨论') || msg.includes('Conflict')) {
          participantsLockedHint.value = true;
          return; // 保留弹窗与用户输入
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
</style>
