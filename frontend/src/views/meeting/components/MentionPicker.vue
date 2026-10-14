<template>
  <t-popup
    v-model:visible="visible"
    trigger="click"
    placement="top-left"
    :overlay-inner-class-name="'meeting-mention-popup'"
  >
    <t-button size="small" shape="circle" variant="outline" class="mention-trigger">@</t-button>
    <template #content>
      <div class="mention-list">
        <div
          v-for="p in agentParticipants"
          :key="p.agent_id"
          class="meeting-mention-item"
          @click="pick(p)"
        >
          {{ p.display_name }}
        </div>
        <div v-if="!agentParticipants.length" class="meeting-mention-empty">
          {{ $t('meeting.noAgents') }}
        </div>
      </div>
    </template>
  </t-popup>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue';
import { useMeetingStore } from '@/stores/meeting';
import type { MeetingParticipant } from '@/api/meeting';

const emit = defineEmits<{ pick: [p: MeetingParticipant] }>();
const store = useMeetingStore();
const visible = ref(false);

// 仅 agent 角色可被 @（主持人默认关注全场，不可 @）
const agentParticipants = computed(() =>
  (store.currentRoom?.participants ?? []).filter(p => p.kind === 'agent'));

// 选中后收起候选列表，已选列表由父组件维护
function pick(p: MeetingParticipant) {
  emit('pick', p);
  visible.value = false;
}
</script>

<style scoped>
.mention-trigger {
  flex-shrink: 0;
}
</style>

<style>
/* 候选浮层被 Teleport 到 body，需全局样式；用唯一类名避免与其它 mention 组件冲突 */
.meeting-mention-popup {
  padding: 4px;
  min-width: 180px;
  max-width: 280px;
}
.meeting-mention-popup .mention-list {
  max-height: 260px;
  overflow-y: auto;
}
.meeting-mention-popup .meeting-mention-item {
  padding: 6px 10px;
  border-radius: var(--td-radius-medium);
  color: var(--td-text-color-primary);
  font-size: var(--td-font-size-body-medium);
  cursor: pointer;
}
.meeting-mention-popup .meeting-mention-item:hover {
  background: var(--td-bg-color-container-hover);
}
.meeting-mention-popup .meeting-mention-empty {
  padding: 16px 12px;
  text-align: center;
  color: var(--td-text-color-placeholder);
  font-size: var(--td-font-size-body-small);
}
</style>
