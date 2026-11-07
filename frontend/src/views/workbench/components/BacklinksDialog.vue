<template>
  <!-- 反向链接（P4-2）：谁在正文里提到过本条目 -->
  <t-dialog
    :visible="visible"
    :header="t('workbench.backlinks.title')"
    width="560px"
    :confirm-btn="t('workbench.backlinks.done')"
    :cancel-btn="null"
    @confirm="close"
    @close="close"
  >
    <div class="wbl">
      <p class="wbl-hint">{{ t('workbench.backlinks.hint') }}</p>

      <div v-if="loading" class="wbl-empty">{{ t('workbench.tags.loading') }}</div>
      <div v-else-if="links.length === 0" class="wbl-empty">{{ t('workbench.backlinks.empty') }}</div>
      <div v-else class="wbl-list">
        <button
          v-for="link in links"
          :key="link.id"
          class="wbl-row"
          type="button"
          @click="open(link.id)"
        >
          <t-icon :name="link.kind === 'database' ? 'view-list' : 'file'" size="14px" />
          <span class="wbl-title">{{ link.title || t('workbench.item.untitled') }}</span>
          <span class="wbl-time">{{ formatTime(link.updated_at) }}</span>
          <t-icon name="chevron-right" size="13px" class="wbl-go" />
        </button>
      </div>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
/**
 * 反向链接对话框（P4-2）。
 *
 * 口径：正文（投影文本）里出现另一个条目的标题即算引用——单字标题不参与匹配，
 * 避免单字如「日」「周」产生海量误报。点击列表项跳转到引用方。
 */
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { MessagePlugin } from 'tdesign-vue-next'

import { getWorkbenchBacklinks } from '@/api/workbench'
import type { WorkbenchBacklink } from '@/types/workbench'

const props = defineProps<{
  visible: boolean
  itemId: string
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
}>()

const { t } = useI18n()
const router = useRouter()
const links = ref<WorkbenchBacklink[]>([])
const loading = ref(false)

watch(
  () => props.visible,
  (visible) => {
    if (!visible) return
    void load()
  },
  { immediate: true },
)

async function load() {
  loading.value = true
  try {
    links.value = await getWorkbenchBacklinks(props.itemId)
  } catch {
    MessagePlugin.error(t('workbench.backlinks.failed'))
    links.value = []
  } finally {
    loading.value = false
  }
}

function open(itemId: string) {
  close()
  void router.push({ name: 'workbenchItem', params: { itemId } })
}

function formatTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  return date.toLocaleString(undefined, { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

function close() {
  emit('update:visible', false)
}
</script>

<style scoped lang="less">
.wbl {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-3);
}

.wbl-hint {
  margin: 0;
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}

.wbl-empty {
  padding: var(--app-space-4) 0;
  text-align: center;
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}

.wbl-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
  max-height: 300px;
  overflow-y: auto;
}

.wbl-row {
  display: flex;
  align-items: center;
  gap: 8px;
  height: 34px;
  padding: 0 var(--app-space-2);
  border: none;
  border-radius: var(--app-radius-sm);
  background: transparent;
  color: var(--td-text-color-primary);
  cursor: pointer;
  text-align: left;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }
}

.wbl-title {
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: var(--app-text-xs);
}

.wbl-time {
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}

.wbl-go {
  color: var(--td-text-color-placeholder);
}
</style>
