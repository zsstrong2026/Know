<template>
  <!-- 会议室一体化页面：左侧会议卡片列表常驻，右侧为子路由出口（空态 / 会议详情） -->
  <div class="meeting-shell">
    <aside class="room-sidebar">
      <div class="sb-head">
        <div class="sb-title-row">
          <span class="sb-title">{{ $t('menu.meetings') }}</span>
          <t-button size="small" theme="primary" @click="showCreate = true">
            <template #icon><t-icon name="add" /></template>
            {{ $t('meeting.createShort') }}
          </t-button>
        </div>
        <!-- 搜索仅在本地过滤已加载的会议，避免每次输入都打接口 -->
        <t-input v-model="keyword" class="sb-search" clearable
          :placeholder="$t('meeting.searchPlaceholder')">
          <template #prefix-icon><t-icon name="search" /></template>
        </t-input>
      </div>

      <!-- 状态筛选：切换即重新拉取列表 -->
      <div class="sb-filter">
        <button v-for="f in filterOptions" :key="f.value" type="button" class="chip-filter"
          :class="{ 'is-active': filter === f.value }" @click="setFilter(f.value)">
          {{ f.label }}
        </button>
      </div>

      <div class="room-cards">
        <div v-for="room in filteredRooms" :key="room.id" class="room-card"
          :class="{ 'is-active': room.id === activeRoomId }" @click="openRoom(room.id)">
          <div class="rc-title">
            <span>{{ room.title }}</span>
          </div>
          <div v-if="room.description" class="rc-desc">{{ room.description }}</div>
          <!-- 卡片页脚：状态标签 + 管理操作（紧随状态右侧）+ 时间，避免长标题与操作重叠 -->
          <div class="rc-foot">
            <t-tag :theme="statusTheme(room.status)" size="small" variant="light">
              {{ statusLabel(room.status) }}
            </t-tag>
            <!-- 管理操作：紧随状态标签右侧，hover 或选中时浮现，@click.stop 阻断卡片跳转 -->
            <div class="rc-actions" @click.stop>
              <t-button size="small" variant="text" @click="openDetail(room)">
                {{ $t('meeting.detail') }}
              </t-button>
              <t-button v-if="room.status !== 'archived'" size="small" variant="text" theme="primary"
                @click="archive(room)">
                {{ $t('meeting.archive') }}
              </t-button>
              <t-button size="small" variant="text" theme="danger" @click="remove(room)">
                {{ $t('meeting.delete') }}
              </t-button>
            </div>
            <span class="rc-time">{{ formatTime(room.created_at) }}</span>
          </div>
        </div>
        <div v-if="!filteredRooms.length" class="rc-empty">
          {{ keyword ? $t('meeting.emptySearch') : $t('meeting.emptyRooms') }}
        </div>
      </div>
    </aside>

    <section class="room-main">
      <RouterView />
    </section>

    <MeetingCreateDialog v-model:visible="showCreate" @created="onCreated" />
    <MeetingDetailDialog v-model:visible="showDetail" :room="detailRoom" @saved="refresh" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRoute, useRouter } from 'vue-router';
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next';
import { useMeetingStore } from '@/stores/meeting';
import { archiveMeeting, deleteMeeting, type MeetingRoom } from '@/api/meeting';
import MeetingCreateDialog from './components/MeetingCreateDialog.vue';
import MeetingDetailDialog from './components/MeetingDetailDialog.vue';

const store = useMeetingStore();
const router = useRouter();
const route = useRoute();
const { t } = useI18n();

const keyword = ref('');
const filter = ref(''); // ''=全部 / active / archived
const showCreate = ref(false);
const showDetail = ref(false);
const detailRoom = ref<MeetingRoom | null>(null);

// 筛选档位文案随语言切换刷新，故放 computed
const filterOptions = computed(() => [
  { value: '', label: t('meeting.filterAll') },
  { value: 'active', label: t('meeting.filterActive') },
  // M1 起新建会议默认进入待确认态，单列一档否则会被「进行中」筛掉
  { value: 'awaiting_confirmation', label: t('meeting.filterAwaiting') },
  { value: 'archived', label: t('meeting.filterArchived') },
]);

// 当前选中的会议（由子路由参数驱动高亮）
const activeRoomId = computed(() => (route.params.roomId as string) || '');

// 搜索词只在已加载列表内做标题/描述匹配
const filteredRooms = computed(() => {
  const kw = keyword.value.trim().toLowerCase();
  if (!kw) return store.rooms;
  return store.rooms.filter(
    (r) => r.title?.toLowerCase().includes(kw) || r.description?.toLowerCase().includes(kw),
  );
});

onMounted(refresh);

async function refresh() {
  try {
    await store.fetchRooms(filter.value);
  } catch {
    MessagePlugin.error(t('meeting.loadFailed'));
  }
}

function setFilter(value: string) {
  if (filter.value === value) return;
  filter.value = value;
  void refresh();
}

function openRoom(roomId: string) {
  if (roomId === activeRoomId.value) return;
  router.push({ name: 'meetingRoom', params: { roomId } });
}

// 打开详情弹窗（@click.stop 已由 rc-actions 容器承担阻断跳转）
function openDetail(room: MeetingRoom) {
  detailRoom.value = room;
  showDetail.value = true;
}

// 会议状态标签：三态（进行中 / 待确认 / 已归档）
function statusLabel(status: MeetingRoom['status']) {
  switch (status) {
    case 'active': return t('meeting.filterActive');
    case 'awaiting_confirmation': return t('meeting.filterAwaiting');
    default: return t('meeting.filterArchived');
  }
}

function statusTheme(status: MeetingRoom['status']): 'success' | 'warning' | 'default' {
  switch (status) {
    case 'active': return 'success';
    case 'awaiting_confirmation': return 'warning';
    default: return 'default';
  }
}

// 列表时间展示：1 分钟内“刚刚”，当天按分钟/小时，跨天显示日期
function formatTime(iso: string): string {
  const ts = iso ? new Date(iso).getTime() : NaN;
  if (Number.isNaN(ts)) return '';
  const minutes = Math.floor((Date.now() - ts) / 60000);
  if (minutes < 1) return t('meeting.timeJustNow');
  if (minutes < 60) return t('meeting.timeMinutesAgo', { n: minutes });
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return t('meeting.timeHoursAgo', { n: hours });
  if (hours < 48) return t('meeting.timeYesterday');
  const d = new Date(ts);
  const pad = (v: number) => String(v).padStart(2, '0');
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

// 新建成功后刷新列表并进入新会议
function onCreated(room: MeetingRoom) {
  showCreate.value = false;
  void refresh();
  router.push({ name: 'meetingRoom', params: { roomId: room.id } });
}

function archive(room: MeetingRoom) {
  const confirm = DialogPlugin.confirm({
    header: t('meeting.archive'),
    body: t('meeting.archiveConfirm'),
    confirmBtn: { content: t('meeting.archive'), theme: 'primary' },
    cancelBtn: t('meeting.cancel'),
    onConfirm: async () => {
      try {
        await archiveMeeting(room.id);
        confirm.destroy();
        MessagePlugin.success(t('meeting.archiveDone'));
        await refresh();
      } catch (e) {
        confirm.destroy();
        MessagePlugin.error((e as Error)?.message || t('meeting.loadFailed'));
      }
    },
  });
}

function remove(room: MeetingRoom) {
  const confirm = DialogPlugin.confirm({
    header: t('meeting.delete'),
    body: t('meeting.deleteConfirm'),
    confirmBtn: { content: t('meeting.delete'), theme: 'danger' },
    cancelBtn: t('meeting.cancel'),
    onConfirm: async () => {
      try {
        await deleteMeeting(room.id);
        confirm.destroy();
        MessagePlugin.success(t('meeting.deleteDone'));
        // 删掉的正是当前打开的房间时回到空态，避免详情页停留在已删除数据上
        if (room.id === activeRoomId.value) {
          router.replace({ name: 'meetingList' });
        }
        await refresh();
      } catch (e) {
        confirm.destroy();
        MessagePlugin.error((e as Error)?.message || t('meeting.loadFailed'));
      }
    },
  });
}
</script>

<style lang="less" scoped>
.meeting-shell {
  flex: 1;
  min-height: 0;
  display: flex;
  background: var(--td-bg-color-container);
}

/* ---------- 左侧会议卡片列表 ---------- */
.room-sidebar {
  width: 300px;
  flex: 0 0 300px;
  display: flex;
  flex-direction: column;
  border-right: 1px solid var(--td-component-stroke);
  background: var(--td-bg-color-container);
}

.sb-head {
  padding: var(--app-space-4) var(--app-space-4) var(--app-space-3);
}

.sb-title-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--app-space-2);
}

.sb-title {
  font-size: var(--app-text-xl);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.sb-search {
  margin-top: var(--app-space-3);
}

.sb-filter {
  display: flex;
  gap: var(--app-space-1);
  padding: 0 var(--app-space-4) var(--app-space-2);
}

.chip-filter {
  height: 26px;
  padding: 0 var(--app-space-3);
  border: 1px solid transparent;
  border-radius: var(--app-radius-pill);
  background: var(--td-bg-color-page);
  color: var(--td-text-color-secondary);
  font-size: var(--app-text-sm);
  cursor: pointer;
  transition: background-color var(--app-motion-fast), color var(--app-motion-fast);

  &:hover {
    color: var(--td-text-color-primary);
  }

  &.is-active {
    background: color-mix(in srgb, var(--td-brand-color) 8%, transparent);
    color: var(--td-brand-color);
    font-weight: 500;
  }
}

.room-cards {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: var(--app-space-2) var(--app-space-3) var(--app-space-4);
  display: flex;
  flex-direction: column;
  gap: var(--app-space-2);
}

.rc-empty {
  padding: var(--app-space-8) var(--app-space-4);
  text-align: center;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-md);
}

.room-card {
  position: relative;
  padding: var(--app-space-3) var(--app-space-3) 10px var(--app-space-3);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-lg);
  background: var(--td-bg-color-container);
  cursor: pointer;
  transition: border-color var(--app-motion-fast), background-color var(--app-motion-fast);

  &:hover {
    border-color: color-mix(in srgb, var(--td-brand-color) 35%, transparent);
  }

  &.is-active {
    background: color-mix(in srgb, var(--td-brand-color) 8%, transparent);
    border-color: color-mix(in srgb, var(--td-brand-color) 35%, transparent);

    // 选中态左侧色条
    &::before {
      content: '';
      position: absolute;
      left: 0;
      top: 10px;
      bottom: 10px;
      width: 3px;
      border-radius: 0 var(--app-radius-xs) var(--app-radius-xs) 0;
      background: var(--td-brand-color);
    }

    .rc-actions {
      opacity: 1;
    }
  }

  &:hover .rc-actions {
    opacity: 1;
  }
}

.rc-title {
  display: flex;
  align-items: center;
  gap: var(--app-space-1);
  min-width: 0;
  font-size: var(--app-text-base);
  font-weight: 600;
  color: var(--td-text-color-primary);

  span {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
}

.rc-desc {
  margin-top: var(--app-space-1);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  font-size: var(--app-text-sm);
  line-height: 18px;
  color: var(--td-text-color-secondary);
}

.rc-foot {
  margin-top: var(--app-space-2);
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--app-space-1) var(--app-space-2);
}

.rc-time {
  margin-left: auto;
  font-size: var(--app-text-sm);
  color: var(--td-text-color-placeholder);
  white-space: nowrap;
}

.rc-actions {
  display: flex;
  align-items: center;
  gap: 0;
  opacity: 0;
  transition: opacity var(--app-motion-fast);

  // 文本按钮在卡片页脚内更紧凑，避免挤占时间显示
  :deep(.t-button) {
    padding: 0 var(--app-space-1);
    height: 22px;
  }
}

/* ---------- 右侧子路由出口 ---------- */
.room-main {
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
</style>
