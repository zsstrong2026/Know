<template>
  <div class="meeting-list">
    <div class="header">
      <h2>{{ $t('menu.meetings') }}</h2>
      <!-- 状态筛选：三档切换即刷新列表 -->
      <t-radio-group v-model="filter" variant="default-filled" @change="refresh">
        <t-radio-button value="">{{ $t('meeting.filterAll') }}</t-radio-button>
        <t-radio-button value="active">{{ $t('meeting.filterActive') }}</t-radio-button>
        <t-radio-button value="archived">{{ $t('meeting.filterArchived') }}</t-radio-button>
      </t-radio-group>
      <t-button theme="primary" @click="$router.push({ name: 'meetingCreate' })">
        {{ $t('meeting.create') }}
      </t-button>
    </div>
    <t-table
      row-key="id"
      :data="store.rooms"
      :columns="columns"
      size="medium"
      hover
      stripe
      :loading="loading"
      @row-click="onRowClick"
    >
      <!-- 状态列：active 用 success 主题，其余用默认主题 -->
      <template #status="{ row }">
        <t-tag :theme="row.status === 'active' ? 'success' : 'default'" size="small">
          {{ row.status }}
        </t-tag>
      </template>
      <!-- 操作列：@click.stop 阻断行点击跳转 -->
      <template #op="{ row }">
        <t-button v-if="row.status === 'active'" size="small" variant="text" theme="primary"
          @click.stop="archive(row)">
          {{ $t('meeting.archive') }}
        </t-button>
        <t-button size="small" variant="text" theme="danger" @click.stop="remove(row)">
          {{ $t('meeting.delete') }}
        </t-button>
      </template>
    </t-table>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next';
import { useMeetingStore } from '@/stores/meeting';
import { archiveMeeting, deleteMeeting } from '@/api/meeting';
import type { MeetingRoom } from '@/api/meeting';

const store = useMeetingStore();
const router = useRouter();
const { t } = useI18n();
const loading = ref(false);
const filter = ref(''); // 状态筛选：''=全部 / active / archived

// 列定义放在 computed 中以随语言切换刷新标题
const columns = computed(() => [
  { colKey: 'title', title: t('meeting.title'), ellipsis: true, minWidth: 160 },
  { colKey: 'description', title: t('meeting.topic'), ellipsis: true, minWidth: 200 },
  { colKey: 'created_at', title: t('meeting.createdAt'), width: 180 },
  { colKey: 'status', title: t('meeting.status'), width: 100 },
  { colKey: 'op', title: t('meeting.actions'), width: 150 },
]);

onMounted(refresh);

// 按当前筛选档位拉取列表（筛选切换与增删改后的刷新共用）
async function refresh() {
  loading.value = true;
  try {
    await store.fetchRooms(filter.value);
  } catch {
    MessagePlugin.error(t('meeting.loadFailed'));
  } finally {
    loading.value = false;
  }
}

// t-table 的行点击回调入参为 { row, index, e }
function onRowClick({ row }: { row: MeetingRoom }) {
  router.push({ name: 'meetingRoom', params: { roomId: row.id } });
}

// 归档（仅 active 行展示）：确认后调接口并刷新，归档后会议进入只读态
function archive(row: MeetingRoom) {
  const confirm = DialogPlugin.confirm({
    header: t('meeting.archive'),
    body: t('meeting.archiveConfirm'),
    confirmBtn: { content: t('meeting.archive'), theme: 'primary' },
    cancelBtn: t('meeting.cancel'),
    onConfirm: async () => {
      try {
        await archiveMeeting(row.id);
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

// 删除：后端级联删除消息/参会者/产物，不可恢复
function remove(row: MeetingRoom) {
  const confirm = DialogPlugin.confirm({
    header: t('meeting.delete'),
    body: t('meeting.deleteConfirm'),
    confirmBtn: { content: t('meeting.delete'), theme: 'danger' },
    cancelBtn: t('meeting.cancel'),
    onConfirm: async () => {
      try {
        await deleteMeeting(row.id);
        confirm.destroy();
        MessagePlugin.success(t('meeting.deleteDone'));
        await refresh();
      } catch (e) {
        confirm.destroy();
        MessagePlugin.error((e as Error)?.message || t('meeting.loadFailed'));
      }
    },
  });
}
</script>

<style scoped>
.header {
  display: flex;
  align-items: center;
  gap: 16px;
  margin-bottom: 16px;
}
</style>
