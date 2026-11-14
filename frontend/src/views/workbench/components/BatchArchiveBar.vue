<template>
  <!-- 批量归档工具条（§9.8.1）：已选计数 + 全选可归档 + 归档所选 + 取消 -->
  <div class="batch-bar" role="region" :aria-label="t('workbench.batchArchive.barLabel')">
    <span class="bb-count">
      {{ t('workbench.batchArchive.selected', { count: selectedCount }) }}
      <span v-if="actionableCount > 0" class="bb-hint">
        {{ t('workbench.batchArchive.actionable', { count: actionableCount }) }}
      </span>
      <span v-if="skippedCount > 0" class="bb-skip">
        {{ t('workbench.batchArchive.skipped', { count: skippedCount }) }}
      </span>
    </span>
    <span class="spacer" />
    <t-button variant="text" size="small" @click="emit('selectAllActionable')">
      {{ t('workbench.batchArchive.selectAll') }}
    </t-button>
    <t-button
      theme="primary"
      size="small"
      :disabled="actionableCount === 0"
      @click="emit('archive')"
    >
      {{ t('workbench.batchArchive.archiveSelected') }}
    </t-button>
    <!-- 批量移入回收站：对选中项整体可逆删除，不区分归档状态 -->
    <t-button
      v-if="trashableCount > 0"
      theme="danger"
      variant="outline"
      size="small"
      @click="emit('trash')"
    >
      {{ t('workbench.batchArchive.trashSelected') }}
    </t-button>
    <t-button variant="outline" size="small" @click="emit('cancel')">
      {{ t('workbench.batchArchive.cancel') }}
    </t-button>
  </div>
</template>

<script setup lang="ts">
/**
 * 批量归档工具条。
 *
 * 只做计数展示与事件上抛：哪些条目被选中、能不能归档，全部由列表页决定。
 * 计数分三块（已选 / 可归档 / 将跳过），避免用户以为自己选错了。
 */
import { useI18n } from 'vue-i18n'

withDefaults(defineProps<{
  selectedCount: number
  actionableCount: number
  skippedCount: number
  /** 可移入回收站的条目数（通常等于已选数）；不传表示当前列表不支持批量删除 */
  trashableCount?: number
}>(), { trashableCount: 0 })

const emit = defineEmits<{
  selectAllActionable: []
  archive: []
  trash: []
  cancel: []
}>()

const { t } = useI18n()
</script>

<style lang="less" scoped>
.batch-bar {
  position: sticky;
  top: 0;
  z-index: 5;
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  padding: var(--app-space-2) var(--app-space-3);
  border: 1px solid var(--td-brand-color-3);
  border-radius: var(--app-radius-md);
  background: var(--td-brand-color-1);
  font-size: var(--app-text-xs);
  color: var(--td-text-color-primary);
}

.bb-count {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
}

.bb-hint {
  color: var(--td-brand-color);
}

.bb-skip {
  color: var(--td-text-color-placeholder);
}

.spacer {
  flex: 1;
}
</style>
