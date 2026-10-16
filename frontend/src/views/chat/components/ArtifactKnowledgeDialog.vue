<!--
  ArtifactKnowledgeDialog — 二进制产物「加入知识库」的选库弹窗。

  与手工知识编辑器不同，二进制文件（PDF / Word / Excel / 图片 / 音视频等）
  不做在线编辑，选定知识库后直接以原文件上传入库，因此这里只负责“选哪个库”。
-->
<template>
  <t-dialog
    class="artifact-kb-dialog"
    :visible="visible"
    :footer="false"
    width="440px"
    :close-on-overlay-click="!uploading"
    :close-on-esc-keydown="!uploading"
    :destroy-on-close="true"
    :z-index="2600"
    attach="body"
    @close="handleClose"
  >
    <template #header>
      <div class="artifact-kb-dialog__heading">
        <t-icon name="folder-add" size="16px" aria-hidden="true" />
        <span>{{ $t('agent.artifactDrawer.addToKbTitle') }}</span>
      </div>
    </template>

    <p class="artifact-kb-dialog__desc">
      {{ $t('agent.artifactDrawer.addToKbDesc', { name: fileName }) }}
    </p>

    <t-select
      v-model="kbId"
      class="artifact-kb-dialog__select"
      :loading="loading"
      :options="options"
      :disabled="uploading"
      :placeholder="$t('agent.artifactDrawer.addToKbPlaceholder')"
      :aria-label="$t('agent.artifactDrawer.addToKbLabel')"
      :popup-props="{ attach: 'body', zIndex: 2700 }"
      clearable
    >
      <template #empty>
        <div class="artifact-kb-dialog__empty">
          {{ $t('agent.artifactDrawer.addToKbEmpty') }}
        </div>
      </template>
    </t-select>

    <div class="artifact-kb-dialog__footer">
      <t-button variant="outline" :disabled="uploading" @click="handleClose">
        {{ $t('common.cancel') }}
      </t-button>
      <t-button
        theme="primary"
        :loading="uploading"
        :disabled="!kbId || loading"
        @click="handleConfirm"
      >
        {{ $t('agent.artifactDrawer.addToKbConfirm') }}
      </t-button>
    </div>
  </t-dialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { fetchWritableDocumentKnowledgeBases, type KnowledgeBaseOption } from '@/utils/knowledgeBaseOptions'

const props = withDefaults(
  defineProps<{
    visible: boolean
    fileName: string
    /** 上传中：锁定弹窗交互，避免重复提交。 */
    uploading?: boolean
  }>(),
  { uploading: false },
)

const emit = defineEmits<{
  (e: 'update:visible', value: boolean): void
  (e: 'confirm', payload: { kbId: string; kbName?: string }): void
}>()

const kbId = ref('')
const options = ref<KnowledgeBaseOption[]>([])
const loading = ref(false)

watch(
  () => props.visible,
  async (open) => {
    if (!open) return
    kbId.value = ''
    loading.value = true
    try {
      options.value = await fetchWritableDocumentKnowledgeBases()
      kbId.value = options.value[0]?.value ?? ''
    } catch (err) {
      console.error('[ArtifactKnowledgeDialog] load knowledge bases failed:', err)
      options.value = []
    } finally {
      loading.value = false
    }
  },
  { immediate: true },
)

function handleConfirm() {
  if (!kbId.value || props.uploading) return
  const selected = options.value.find((option) => option.value === kbId.value)
  emit('confirm', { kbId: kbId.value, kbName: selected?.label })
}

function handleClose() {
  if (props.uploading) return
  emit('update:visible', false)
}
</script>

<style scoped lang="less">
.artifact-kb-dialog__heading {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: var(--app-text-lg);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.artifact-kb-dialog__desc {
  margin: 0 0 16px;
  font-size: var(--app-text-base);
  line-height: 1.6;
  color: var(--td-text-color-secondary);
  word-break: break-all;
}

.artifact-kb-dialog__select {
  width: 100%;
}

.artifact-kb-dialog__empty {
  padding: 20px;
  text-align: center;
  color: var(--td-text-color-placeholder);
}

.artifact-kb-dialog__footer {
  margin-top: 24px;
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
</style>
