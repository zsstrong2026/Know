<template>
  <t-drawer :visible="visible" :header="$t('meeting.artifacts')" size="46%" :footer="false"
    :z-index="2600" attach="body" @close="emit('update:visible', false)">
    <!-- 并排对比：恰好勾选两个版本时切换为左右两栏正文 -->
    <div v-if="compareList.length === 2" class="compare-view">
      <div class="compare-toolbar">
        <t-button size="small" variant="outline" @click="compareIds = []">
          {{ $t('meeting.exitCompare') }}
        </t-button>
      </div>
      <div class="compare-cols">
        <div v-for="a in compareList" :key="a.id" class="compare-col">
          <div class="artifact-head">
            <t-tag :theme="isCurrent(a) ? 'success' : 'default'" variant="light">v{{ a.version }}</t-tag>
            <t-tag v-if="isCurrent(a)" size="small" theme="success" variant="light">
              {{ $t('meeting.currentSolution') }}
            </t-tag>
          </div>
          <div class="artifact-content">{{ a.content }}</div>
        </div>
      </div>
    </div>

    <t-loading :loading="loading" show-overlay>
      <div v-if="!loading && artifacts.length === 0" class="empty">
        {{ $t('meeting.artifactsEmpty') }}
      </div>
      <template v-else>
        <div class="compare-hint">{{ $t('meeting.compareHint') }}</div>
        <div v-for="a in artifacts" :key="a.id" class="artifact-item">
          <div class="artifact-head">
            <!-- 对比勾选：最多保留两个，超出时顶掉最早勾选的一个 -->
            <t-checkbox :checked="compareIds.includes(a.id)" @change="toggleCompare(a.id)" />
            <t-tag :theme="isCurrent(a) ? 'success' : 'default'" variant="light">v{{ a.version }}</t-tag>
            <t-tag v-if="isCurrent(a)" size="small" theme="success" variant="light">
              {{ $t('meeting.currentSolution') }}
            </t-tag>
            <span class="counts">
              {{ $t('meeting.adoptedCount', { n: a.adopted_count }) }} /
              {{ $t('meeting.rejectedCount', { n: a.rejected_count }) }}
            </span>
            <t-button v-if="!isCurrent(a)" size="small" theme="primary" variant="text"
              @click="rollback(a)">{{ $t('meeting.rollbackTo') }}</t-button>
          </div>
          <span class="created-at">{{ a.created_at }}</span>
          <t-collapse expand-mutex>
            <t-collapse-item :value="a.id" :header="$t('meeting.viewContent')">
              <div class="artifact-content">{{ a.content }}</div>
            </t-collapse-item>
          </t-collapse>
        </div>
      </template>
    </t-loading>
  </t-drawer>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next';
import { listArtifacts, type MeetingArtifact } from '@/api/meeting';
import { useMeetingStore } from '@/stores/meeting';

const props = defineProps<{ visible: boolean; roomId: string; currentSolutionMessageId: string }>();
const emit = defineEmits<{ 'update:visible': [boolean] }>();

const { t } = useI18n();
const store = useMeetingStore();
const artifacts = ref<MeetingArtifact[]>([]);
const loading = ref(false);
const compareIds = ref<string[]>([]);
// 勾选中的产物实体（按 compareIds 顺序对应左右两栏）
const compareList = computed(() => artifacts.value.filter((a) => compareIds.value.includes(a.id)));

watch(() => props.visible, (v) => {
  if (v) fetchArtifacts(); // 每次打开重拉，保证 is_current 标记为最新
});

async function fetchArtifacts() {
  loading.value = true;
  try {
    artifacts.value = await listArtifacts(props.roomId);
  } finally {
    loading.value = false;
  }
}

// 当前方案指针指向该产物的消息时视为“当前版本”
function isCurrent(a: MeetingArtifact): boolean {
  return Boolean(props.currentSolutionMessageId) && a.solution_message_id === props.currentSolutionMessageId;
}

function toggleCompare(id: string) {
  const i = compareIds.value.indexOf(id);
  if (i >= 0) compareIds.value.splice(i, 1);
  else {
    if (compareIds.value.length >= 2) compareIds.value.shift();
    compareIds.value.push(id);
  }
}

// 回滚：确认后调 store（HTTP 成功即重拉房间与消息），再刷新抽屉内列表更新“当前”标记
function rollback(a: MeetingArtifact) {
  const confirm = DialogPlugin.confirm({
    header: t('meeting.rollback'),
    body: t('meeting.rollbackConfirm', { v: a.version }),
    confirmBtn: { content: t('meeting.rollbackTo'), theme: 'danger' },
    cancelBtn: t('meeting.cancel'),
    onConfirm: async () => {
      try {
        await store.rollback(a.version);
        confirm.destroy();
        MessagePlugin.success(t('meeting.rollbackDone'));
        await fetchArtifacts();
      } catch (e) {
        confirm.destroy();
        MessagePlugin.error((e as Error)?.message || t('meeting.loadFailed'));
      }
    },
  });
}
</script>

<style scoped>
.compare-toolbar {
  display: flex;
  justify-content: flex-end;
  margin-bottom: 8px;
}

.compare-cols {
  display: flex;
  gap: 12px;
  align-items: stretch;
}

.compare-col {
  flex: 1;
  min-width: 0;
  padding: 8px;
  border: 1px solid var(--td-component-border, #e7e7e7);
  border-radius: 6px;
}

.artifact-head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.counts {
  font-size: 13px;
  color: var(--td-text-color-secondary, #666);
}

.created-at {
  display: block;
  margin: 4px 0 6px;
  font-size: 12px;
  color: var(--td-text-color-placeholder, #999);
}

.artifact-item {
  padding: 10px 0;
  border-bottom: 1px solid var(--td-component-border, #e7e7e7);
}

.compare-hint {
  margin-bottom: 8px;
  font-size: 12px;
  color: var(--td-text-color-placeholder, #999);
}

.empty {
  padding: 40px 0;
  text-align: center;
  color: var(--td-text-color-placeholder, #999);
}

/* 方案正文：与消息气泡一致的纯文本展示，保留换行便于阅读 */
.artifact-content {
  white-space: pre-wrap;
  word-break: break-word;
  font-size: 13px;
  line-height: 1.6;
}
</style>
