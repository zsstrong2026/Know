<template>
  <!-- 移动条目到文件夹：从文件夹列表中选择目标（参考 AFFiNE 的移动弹窗） -->
  <t-dialog
    v-model:visible="visible"
    :header="t('workbench.moveToFolder.title', { title: item?.title ?? '' })"
    width="480px"
    :footer="false"
    :close-on-overlay-click="!saving"
    :close-on-esc-keydown="!saving"
    @close="onClose"
  >
    <div class="mtf-body">
      <t-input
        v-model="keyword"
        class="mtf-search"
        :placeholder="t('workbench.moveToFolder.searchPlaceholder')"
        clearable
      >
        <template #prefix-icon><t-icon name="search" /></template>
      </t-input>

      <div v-if="filteredFolders.length === 0" class="mtf-empty">
        <t-icon name="folder" size="28px" />
        <p>{{ keyword ? t('workbench.moveToFolder.emptySearch') : t('workbench.moveToFolder.empty') }}</p>
      </div>

      <div v-else class="mtf-list">
        <div
          v-for="folder in filteredFolders"
          :key="folder.id"
          class="mtf-row"
          :class="{ 'is-checked': selectedId === folder.id }"
          @click="selectedId = folder.id"
        >
          <t-icon name="folder" size="16px" class="mtf-icon" />
          <span class="mtf-name" :title="folder.name">{{ folder.name }}</span>
          <span v-if="folder.id === item?.folder_id" class="mtf-current">
            {{ t('workbench.moveToFolder.current') }}
          </span>
          <span class="mtf-count">{{ folder.item_count }}</span>
        </div>
      </div>

      <div class="mtf-footer">
        <span class="spacer" />
        <t-button variant="outline" :disabled="saving" @click="onClose">
          {{ t('workbench.moveToFolder.cancel') }}
        </t-button>
        <t-button
          theme="primary"
          :loading="saving"
          :disabled="!selectedId"
          @click="onConfirm"
        >
          {{ t('workbench.moveToFolder.confirm') }}
        </t-button>
      </div>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
/**
 * 「移动到文件夹」：从侧栏同一份文件夹列表里选择目标。
 *
 * 只做单选移动，不引入拖拽树；搜索在本地过滤名称，避免为一次操作
 * 拉一套带层级的目录选择器。
 */
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { useWorkbenchStore } from '@/stores/workbench'
import type { WorkbenchItem, WorkbenchFolder } from '@/types/workbench'

const props = defineProps<{
  modelValue: boolean
  item: WorkbenchItem | null
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  moved: []
}>()

const { t } = useI18n()
const store = useWorkbenchStore()

const visible = computed({
  get: () => props.modelValue,
  set: (value: boolean) => emit('update:modelValue', value),
})

const keyword = ref('')
const selectedId = ref('')
const saving = ref(false)

/** 本地按名称过滤；folders 由 store 统一维护，无需额外请求。 */
const filteredFolders = computed<WorkbenchFolder[]>(() => {
  const kw = keyword.value.trim().toLowerCase()
  if (!kw) return store.folders
  return store.folders.filter((folder) => folder.name.toLowerCase().includes(kw))
})

function onClose() {
  if (saving.value) return
  visible.value = false
}

async function onConfirm() {
  if (!selectedId.value || saving.value || !props.item) return
  saving.value = true
  try {
    await store.moveItemToFolder(props.item.id, selectedId.value)
    const folder = store.folders.find((row) => row.id === selectedId.value)
    MessagePlugin.success(t('workbench.moveToFolder.success', { folder: folder?.name ?? '' }))
    emit('moved')
    visible.value = false
  } catch {
    MessagePlugin.error(t('workbench.moveToFolder.failed'))
  } finally {
    saving.value = false
  }
}

// 打开时重置选择；关闭不保留上次选中，避免误确认到旧目标
watch(
  () => props.modelValue,
  (open) => {
    if (open) {
      keyword.value = ''
      selectedId.value = ''
    }
  },
)
</script>

<style lang="less" scoped>
.mtf-body {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-3);
}

.mtf-search {
  width: 100%;
}

.mtf-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
  max-height: 320px;
  margin: 0 -4px;
  padding: 2px 4px;
  overflow-y: auto;
  scrollbar-width: thin;
}

.mtf-row {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: 44px;
  padding: 6px 8px;
  border-radius: var(--app-radius-md);
  color: var(--td-text-color-primary);
  cursor: pointer;
  transition: background-color var(--app-motion-fast) ease;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }

  &.is-checked {
    background: var(--td-brand-color-1);
  }
}

.mtf-icon {
  flex: none;
  color: var(--td-text-color-secondary);
}

.mtf-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  font-size: var(--app-text-md);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.mtf-current {
  flex: none;
  padding: 2px var(--app-space-2);
  border-radius: var(--app-radius-pill);
  background: var(--td-bg-color-secondarycontainer);
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-2xs);
  white-space: nowrap;
}

.mtf-count {
  flex: none;
  color: var(--td-text-color-placeholder);
  font-size: var(--app-text-xs);
}

.mtf-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  padding: var(--app-space-8) 0;
  color: var(--td-text-color-placeholder);

  p {
    margin: 0;
    color: var(--td-text-color-secondary);
    font-size: var(--app-text-md);
  }
}

.mtf-footer {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  padding-top: var(--app-space-3);
  border-top: 1px solid var(--td-component-stroke);

  .spacer {
    flex: 1;
  }
}
</style>
