<template>
  <div class="meeting-list">
    <div class="header">
      <h2>{{ $t('menu.meetings') }}</h2>
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
    </t-table>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useI18n } from 'vue-i18n';
import { useRouter } from 'vue-router';
import { useMeetingStore } from '@/stores/meeting';
import type { MeetingRoom } from '@/api/meeting';

const store = useMeetingStore();
const router = useRouter();
const { t } = useI18n();
const loading = ref(false);

// 列定义放在 computed 中以随语言切换刷新标题
const columns = computed(() => [
  { colKey: 'title', title: t('meeting.title'), ellipsis: true, minWidth: 160 },
  { colKey: 'description', title: t('meeting.topic'), ellipsis: true, minWidth: 200 },
  { colKey: 'created_at', title: t('meeting.createdAt'), width: 180 },
  { colKey: 'status', title: t('meeting.status'), width: 100 },
]);

onMounted(async () => {
  loading.value = true;
  try { await store.fetchRooms(); } finally { loading.value = false; }
});

// t-table 的行点击回调入参为 { row, index, e }
function onRowClick({ row }: { row: MeetingRoom }) {
  router.push({ name: 'meetingRoom', params: { roomId: row.id } });
}
</script>
