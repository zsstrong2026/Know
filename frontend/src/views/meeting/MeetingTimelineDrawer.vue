<template>
  <!-- 会议时间线：事件只追加，按时间正序展示阶段推进与关键动作 -->
  <t-drawer :visible="visible" :header="$t('meeting.timelineTitle')" size="38%" :footer="false"
    :z-index="2600" attach="body" @close="emit('update:visible', false)">
    <div class="timeline-body">
      <t-loading :loading="loading" show-overlay>
        <div v-if="loadFailed" class="empty">{{ $t('meeting.timelineLoadFailed') }}</div>
        <div v-else-if="!loading && !events.length" class="empty">{{ $t('meeting.timelineEmpty') }}</div>
        <ul v-else class="timeline-list">
          <li v-for="ev in events" :key="ev.id" class="timeline-item">
            <span class="dot" :class="`is-${ev.phase || 'none'}`" />
            <div class="item-main">
              <div class="item-head">
                <span class="item-summary">{{ ev.summary || ev.type }}</span>
                <t-tag v-if="ev.phase" size="small" variant="light" :theme="meetingPhaseTheme(ev.phase)">
                  {{ phaseText(ev.phase) }}
                </t-tag>
              </div>
              <div class="item-meta">
                <span>{{ actorLabel(ev.actor_kind) }}</span>
                <span>· {{ formatArtifactDateTime(ev.created_at) }}</span>
              </div>
              <div v-if="payloadEntries(ev).length" class="item-payload">
                <span v-for="[k, v] in payloadEntries(ev)" :key="k" class="payload-chip">{{ k }}: {{ v }}</span>
              </div>
            </div>
          </li>
        </ul>
      </t-loading>
    </div>
  </t-drawer>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { listMeetingEvents } from '@/api/meeting';
import type { MeetingEvent, MeetingPhase } from '@/api/meeting';
import { meetingPhaseLabelKey, meetingPhaseTheme } from '@/utils/meetingPhase';
import { formatArtifactDateTime } from '@/utils/sessionArtifacts';

const props = defineProps<{ visible: boolean; roomId: string }>();
const emit = defineEmits<{ (e: 'update:visible', v: boolean): void }>();
const { t } = useI18n();

const events = ref<MeetingEvent[]>([]);
const loading = ref(false);
const loadFailed = ref(false);

// 打开抽屉或切换会议时按需拉取；事件只增，不做长期缓存
async function load() {
  if (!props.roomId) return;
  loading.value = true;
  loadFailed.value = false;
  try {
    events.value = await listMeetingEvents(props.roomId);
  } catch {
    loadFailed.value = true;
  } finally {
    loading.value = false;
  }
}

watch(
  () => [props.visible, props.roomId] as const,
  ([visible]) => { if (visible) load(); },
  { immediate: true },
);

function phaseText(phase: MeetingPhase): string {
  const key = meetingPhaseLabelKey(phase);
  return key ? t(key) : phase;
}

const ACTOR_KEYS: Record<string, string> = {
  user: 'meeting.timelineActorUser',
  agent: 'meeting.timelineActorAgent',
  host: 'meeting.timelineActorHost',
  system: 'meeting.timelineActorSystem',
};

function actorLabel(kind: string): string {
  return t(ACTOR_KEYS[kind] || ACTOR_KEYS.system);
}

// payload 仅展示标量字段并截断长文本，避免长正文撑爆时间线
function payloadEntries(ev: MeetingEvent): Array<[string, string]> {
  return Object.entries(ev.payload || {})
    .filter(([, v]) => ['string', 'number', 'boolean'].includes(typeof v) && v !== '')
    .slice(0, 4)
    .map(([k, v]) => {
      const text = String(v);
      return [k, text.length > 60 ? `${text.slice(0, 60)}…` : text] as [string, string];
    });
}
</script>

<style scoped>
.timeline-body {
  padding: var(--app-space-2) var(--app-space-1);
}

.empty {
  padding: var(--app-space-8) 0;
  text-align: center;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-sm);
}

.timeline-list {
  margin: 0;
  padding: 0;
  list-style: none;
}

.timeline-item {
  position: relative;
  display: flex;
  gap: var(--app-space-3);
  padding: 0 0 var(--app-space-5) var(--app-space-1);
}

/* 竖向连线：最后一条不绘制，避免尾部悬空 */
.timeline-item:not(:last-child)::before {
  content: '';
  position: absolute;
  left: 7px;
  top: 16px;
  bottom: 0;
  width: 1px;
  background: var(--td-component-stroke);
}

.dot {
  flex: 0 0 8px;
  width: 8px;
  height: 8px;
  margin-top: 6px;
  border-radius: 50%;
  background: var(--td-text-color-placeholder);
}

.dot.is-discussing { background: var(--td-brand-color); }
.dot.is-converging { background: var(--td-success-color); }
.dot.is-opening,
.dot.is-confirming,
.dot.is-producing { background: var(--td-warning-color); }

.item-main {
  flex: 1;
  min-width: 0;
}

.item-head {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
}

.item-summary {
  font-size: var(--app-text-sm);
  font-weight: 500;
  color: var(--td-text-color-primary);
}

.item-meta {
  margin-top: 2px;
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}

.item-payload {
  display: flex;
  flex-wrap: wrap;
  gap: var(--app-space-1);
  margin-top: var(--app-space-2);
}

.payload-chip {
  padding: 1px 6px;
  border-radius: 4px;
  background: var(--td-bg-color-container-hover);
  font-size: var(--app-text-xs);
  color: var(--td-text-color-secondary);
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
