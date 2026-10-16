<!--
  ArtifactKnowledgeEntry — 产物行内的「加入知识库」入口与状态标注。

  未加入：显示图标按钮，点击交给 useArtifactKnowledgeEntry 分流（文本编辑 / 二进制上传）；
  已加入：显示状态标签（草稿 / 已发布），文本型产物可点击回到编辑器继续编辑。
  面板与抽屉共用，保证两处入口表现一致。
-->
<template>
  <span
    v-if="link"
    class="artifact-kb-tag"
    :class="{ 'is-editable': editable }"
    :title="tooltip"
    :role="editable ? 'button' : undefined"
    :tabindex="editable ? 0 : undefined"
    @click.stop="editable && emit('open')"
    @keydown.enter.stop="editable && emit('open')"
  >
    <t-tag :theme="link.status === 'draft' ? 'warning' : 'success'" variant="light" size="small">
      {{ link.status === 'draft' ? $t('agent.artifactDrawer.kbTagDraft') : $t('agent.artifactDrawer.kbTagPublished') }}
    </t-tag>
  </span>
  <t-button
    v-else
    class="artifact-kb-add"
    variant="text"
    shape="square"
    size="small"
    :title="$t('agent.artifactDrawer.addToKb')"
    :aria-label="$t('agent.artifactDrawer.addToKb')"
    :loading="busy"
    @click.stop="emit('add')"
  >
    <template #icon>
      <t-icon name="folder-add" size="16px" />
    </template>
  </t-button>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { ArtifactKnowledgeLink } from '@/stores/artifactKnowledge'

const props = withDefaults(
  defineProps<{
    link: ArtifactKnowledgeLink | null
    /** 正在处理该产物（下载 / 上传中）。 */
    busy?: boolean
    /** 已加入的产物是否可回到编辑器继续编辑。 */
    editable?: boolean
  }>(),
  { busy: false, editable: false },
)

const emit = defineEmits<{
  (e: 'add'): void
  (e: 'open'): void
}>()

const { t } = useI18n()

const tooltip = computed(() => {
  const kb = props.link?.kbName || ''
  return props.editable
    ? t('agent.artifactDrawer.kbTagTooltipEdit', { kb })
    : t('agent.artifactDrawer.kbTagTooltip', { kb })
})
</script>

<style scoped lang="less">
.artifact-kb-tag {
  flex-shrink: 0;
  display: inline-flex;
  align-items: center;

  &.is-editable {
    cursor: pointer;

    &:focus-visible {
      outline: 2px solid var(--td-text-color-secondary);
      outline-offset: 2px;
      border-radius: var(--app-radius-xs);
    }
  }
}

.artifact-kb-add.t-button {
  flex-shrink: 0;
  width: 30px;
  height: 30px;
  border-radius: 7px;
  color: var(--td-text-color-secondary);
  transition: background-color var(--app-motion-fast) ease, color var(--app-motion-fast) ease;

  &:not(.t-is-disabled):not(.t-is-loading):hover {
    background: color-mix(in srgb, var(--td-text-color-primary) 10%, var(--td-bg-color-container));
    color: var(--td-text-color-primary);
  }

  &:not(.t-is-disabled):not(.t-is-loading):active {
    background: color-mix(in srgb, var(--td-text-color-primary) 16%, var(--td-bg-color-container));
  }

  &:focus-visible {
    outline: 2px solid var(--td-text-color-secondary);
    outline-offset: 2px;
  }

  :deep(.t-button__icon) {
    margin: 0;
  }
}
</style>
