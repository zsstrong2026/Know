<template>
  <!-- 版本历史：列出检查点，可恢复到任意历史版本（恢复本身也会写新版本） -->
  <t-dialog
    v-model:visible="visible"
    :header="$t('workbench.version.title')"
    :footer="false"
    width="520px"
    attach="body"
  >
    <div v-if="loading" class="vh-loading">
      <t-loading :loading="true" size="small" />
    </div>
    <div v-else-if="versions.length === 0" class="vh-empty">
      {{ $t('workbench.version.empty') }}
    </div>
    <ul v-else class="vh-list">
      <li v-for="row in rows" :key="row.version" class="vh-row">
        <span class="vh-version">v{{ row.version }}</span>
        <span class="vh-time">{{ formatTime(row.created_at) }}</span>
        <span class="vh-size">{{ formatSize(row.size_bytes) }}</span>
        <span class="spacer" />
        <t-button
          size="small"
          variant="outline"
          :disabled="row.version === currentVersion"
          :loading="restoring === row.version"
          @click="emit('restore', row.version)"
        >
          {{ row.version === currentVersion ? $t('workbench.version.current') : $t('workbench.version.restore') }}
        </t-button>
      </li>
    </ul>
  </t-dialog>
</template>

<script setup lang="ts">
/**
 * 版本历史弹窗。
 *
 * 只做展示与“请求恢复”的上抛：恢复要重建编辑器、推进 base_version，
 * 那些副作用留在编辑页处理，弹窗保持无状态。
 */
import { computed, ref, watch } from 'vue'
import { listWorkbenchVersions } from '@/api/workbench'
import type { WorkbenchVersionSummary } from '@/types/workbench'

const props = defineProps<{
  itemId: string
  /** 当前 snapshot_version，用于标记“当前版本” */
  currentVersion: number
}>()

const emit = defineEmits<{ restore: [version: number] }>()

const visible = ref(false)
const loading = ref(false)
const restoring = ref(0)
const versions = ref<WorkbenchVersionSummary[]>([])

/**
 * 展示用列表：按版本号倒序，最新在上。
 *
 * 直接显示服务端版本号，不再本地重排编号：归档徽标记的就是 snapshot_version
 * （如「已归档 · v13」），两处编号必须能对上，否则用户会以为归档的不是最新版本。
 * 服务端会把「当前快照」补成最新一条，所以第一条永远是当前版本。
 */
const rows = computed(() => [...versions.value].sort((a, b) => b.version - a.version))

/** 每次打开都重新拉一次，避免显示过期列表。 */
watch(visible, async (open) => {
  if (!open) return
  loading.value = true
  try {
    versions.value = await listWorkbenchVersions(props.itemId, 100)
  } catch {
    versions.value = []
  } finally {
    loading.value = false
  }
})

// 恢复开始/结束时由父组件通过 restoreVersion 控制 loading 态
watch(
  () => props.currentVersion,
  () => {
    restoring.value = 0
  },
)

function open() {
  visible.value = true
}

function setRestoring(version: number) {
  restoring.value = version
}

function formatTime(raw: string): string {
  const time = new Date(raw)
  return Number.isNaN(time.getTime()) ? '' : time.toLocaleString()
}

function formatSize(bytes: number): string {
  if (!bytes) return '—'
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}

defineExpose({ open, setRestoring })
</script>

<style lang="less" scoped>
.vh-loading,
.vh-empty {
  display: grid;
  place-items: center;
  min-height: 96px;
  font-size: var(--app-text-md);
  color: var(--td-text-color-placeholder);
}

.vh-list {
  list-style: none;
  margin: 0;
  padding: 0;
  max-height: 360px;
  overflow-y: auto;
}

.vh-row {
  display: flex;
  align-items: center;
  gap: var(--app-space-3);
  padding: var(--app-space-2) 0;
  border-bottom: 1px solid var(--td-component-stroke);
  font-size: var(--app-text-sm);
  color: var(--td-text-color-secondary);

  &:last-child {
    border-bottom: 0;
  }
}

.vh-version {
  min-width: 44px;
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.vh-time {
  font-variant-numeric: tabular-nums;
}

.vh-size {
  color: var(--td-text-color-placeholder);
}

.spacer {
  flex: 1;
}
</style>
