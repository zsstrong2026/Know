<template>
  <!-- 条目标签（P4-2）：勾选即生效，不需要「保存」按钮 -->
  <t-dialog
    :visible="visible"
    :header="t('workbench.tags.title')"
    width="520px"
    :confirm-btn="t('workbench.tags.done')"
    :cancel-btn="null"
    @confirm="close"
    @close="close"
  >
    <div class="wbt">
      <p class="wbt-hint">{{ t('workbench.tags.hint') }}</p>

      <div v-if="loading" class="wbt-empty">{{ t('workbench.tags.loading') }}</div>
      <div v-else-if="tags.length === 0" class="wbt-empty">{{ t('workbench.tags.empty') }}</div>
      <div v-else class="wbt-list">
        <div v-for="tag in tags" :key="tag.id" class="wbt-row">
          <button
            class="wbt-chip"
            :class="[`is-${tag.color}`, { 'is-on': selectedIds.includes(tag.id) }]"
            type="button"
            :aria-pressed="selectedIds.includes(tag.id)"
            @click="toggle(tag)"
          >
            <t-icon :name="selectedIds.includes(tag.id) ? 'check' : 'add'" size="12px" />
            <span>{{ tag.name }}</span>
          </button>
          <span class="wbt-count">{{ t('workbench.tags.itemCount', { count: tag.item_count }) }}</span>
          <button class="wbt-del" type="button" :aria-label="t('workbench.tags.deleteTitle')" @click="removeTag(tag)">
            <t-icon name="close" size="12px" />
          </button>
        </div>
      </div>

      <div class="wbt-create">
        <t-input
          v-model="draft"
          size="small"
          :maxlength="40"
          :placeholder="t('workbench.tags.placeholder')"
          @enter="createTag"
        />
        <t-button size="small" variant="outline" :disabled="!draft.trim()" @click="createTag">
          {{ t('workbench.tags.create') }}
        </t-button>
      </div>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
/**
 * 条目标签对话框（P4-2）。
 *
 * 交互取舍：**勾选即生效**，不设「保存」按钮。标签是轻量组织动作，多一步确认
 * 只会让人忘记点；失败时本地回滚并提示，服务端才是权威。
 */
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'

import {
  createWorkbenchTag,
  deleteWorkbenchTag,
  listWorkbenchItemTags,
  listWorkbenchTags,
  setWorkbenchItemTags,
} from '@/api/workbench'
import type { WorkbenchTag } from '@/types/workbench'

const props = defineProps<{
  visible: boolean
  itemId: string
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  /** 标签集合变化：父层据此刷新列表筛选项与计数 */
  changed: [tags: WorkbenchTag[]]
}>()

const { t } = useI18n()
const tags = ref<WorkbenchTag[]>([])
const selectedIds = ref<string[]>([])
const draft = ref('')
const loading = ref(false)

watch(
  () => props.visible,
  (visible) => {
    if (!visible) return
    draft.value = ''
    void load()
  },
  { immediate: true },
)

async function load() {
  loading.value = true
  try {
    const [all, mine] = await Promise.all([listWorkbenchTags(), listWorkbenchItemTags(props.itemId)])
    tags.value = all
    selectedIds.value = mine.map((tag) => tag.id)
  } catch {
    MessagePlugin.error(t('workbench.tags.failed'))
  } finally {
    loading.value = false
  }
}

async function toggle(tag: WorkbenchTag) {
  const next = selectedIds.value.includes(tag.id)
    ? selectedIds.value.filter((id) => id !== tag.id)
    : [...selectedIds.value, tag.id]
  await apply(next)
}

/** 先乐观更新，失败回滚：勾选标签不该让人等一个来回。 */
async function apply(nextIds: string[]) {
  const previous = selectedIds.value
  selectedIds.value = nextIds
  try {
    const saved = await setWorkbenchItemTags(props.itemId, nextIds)
    emit('changed', saved)
    // 计数会变，回源一次列表（标签数量很少，代价可接受）
    tags.value = await listWorkbenchTags()
  } catch {
    selectedIds.value = previous
    MessagePlugin.error(t('workbench.tags.failed'))
  }
}

async function createTag() {
  const name = draft.value.trim()
  if (!name) return
  try {
    const created = await createWorkbenchTag({ name })
    tags.value = [...tags.value, created].sort((a, b) => a.name.localeCompare(b.name))
    draft.value = ''
    await apply([...selectedIds.value, created.id])
  } catch {
    MessagePlugin.error(t('workbench.tags.failed'))
  }
}

function removeTag(tag: WorkbenchTag) {
  const dialog = DialogPlugin.confirm({
    header: t('workbench.tags.deleteTitle'),
    body: t('workbench.tags.deleteBody', { name: tag.name }),
    theme: 'warning',
    confirmBtn: t('workbench.tags.deleteConfirm'),
    onConfirm: async () => {
      dialog.hide()
      try {
        await deleteWorkbenchTag(tag.id)
        tags.value = tags.value.filter((row) => row.id !== tag.id)
        if (selectedIds.value.includes(tag.id)) {
          selectedIds.value = selectedIds.value.filter((id) => id !== tag.id)
        }
        emit('changed', [])
      } catch {
        MessagePlugin.error(t('workbench.tags.failed'))
      }
    },
  })
}

function close() {
  emit('update:visible', false)
}
</script>

<style scoped lang="less">
.wbt {
  display: flex;
  flex-direction: column;
  gap: var(--app-space-3);
}

.wbt-hint {
  margin: 0;
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}

.wbt-empty {
  padding: var(--app-space-4) 0;
  text-align: center;
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}

.wbt-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
  max-height: 260px;
  overflow-y: auto;
}

.wbt-row {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  height: 32px;
  border-radius: var(--app-radius-sm);

  &:hover {
    background: var(--td-bg-color-container-hover);
  }
}

.wbt-chip {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  height: 24px;
  padding: 0 10px;
  border: 1px solid var(--td-component-stroke);
  border-radius: 999px;
  background: transparent;
  font-size: var(--app-text-xs);
  color: var(--td-text-color-secondary);
  cursor: pointer;

  &.is-on {
    border-color: transparent;
    color: #fff;
  }

  /* 颜色只按白名单名字映射，不接收任意色值 */
  &.is-gray.is-on { background: #7b7b7b; }
  &.is-blue.is-on { background: #0052d9; }
  &.is-green.is-on { background: #0f8a5f; }
  &.is-yellow.is-on { background: #b8860b; }
  &.is-red.is-on { background: #c9353f; }
  &.is-purple.is-on { background: #7a3fd0; }
}

.wbt-count {
  margin-left: auto;
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}

.wbt-del {
  width: 20px;
  height: 20px;
  border: none;
  background: transparent;
  color: var(--td-text-color-placeholder);
  cursor: pointer;
  opacity: 0;
  transition: opacity 0.15s;

  .wbt-row:hover & {
    opacity: 1;
  }

  &:hover {
    color: #c9353f;
  }
}

.wbt-create {
  display: flex;
  gap: var(--app-space-2);
  padding-top: var(--app-space-2);
  border-top: 1px solid var(--td-component-stroke);
}
</style>
