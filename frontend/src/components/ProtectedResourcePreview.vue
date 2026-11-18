<script setup lang="ts">
import { onMounted, onUnmounted, ref, shallowRef } from 'vue'
import DocumentPreview from './document-preview.vue'
import { RESOURCE_PREVIEW_EVENT, type LoadedProtectedFile } from '@/utils/protectedResource'

const file = shallowRef<LoadedProtectedFile | null>(null)
const visible = ref(false)
const version = ref(0)
function open(event: Event) {
  const detail = (event as CustomEvent<LoadedProtectedFile>).detail
  if (!detail || !(detail.blob instanceof Blob) || !detail.blobURL?.startsWith('blob:')) return
  event.preventDefault()
  file.value = detail
  version.value++
  visible.value = true
}
onMounted(() => window.addEventListener(RESOURCE_PREVIEW_EVENT, open))
onUnmounted(() => window.removeEventListener(RESOURCE_PREVIEW_EVENT, open))
</script>

<template>
  <!--
    只在真正打开时渲染：t-drawer 在 visible=false 时仍会往 body 挂一个根节点，
    该节点带 tabindex="0"、铺满视口且 z-index 高于编辑器；它在别的页面（如工作台
    编辑器）会占住键盘焦点，表现为「点正文打不进字、焦点停在一个空抽屉上」。
  -->
  <t-drawer v-if="visible" v-model:visible="visible" :header="file?.fileName" size="70%"
    :z-index="2200" attach="body" :footer="false" destroy-on-close>
    <template v-if="file">
      <a :href="file.blobURL" :download="file.fileName">{{ $t('agent.artifactDrawer.download') }}</a>
      <DocumentPreview :key="version" :source-blob="file.blob" :file-name="file.fileName"
        :file-type="file.blob.type" :active="visible" />
    </template>
  </t-drawer>
</template>

<style>
.protected-resource-card {
  display: inline-flex;
  align-items: center;
  max-width: 100%;
  padding: 14px 18px;
  border: 1px solid var(--td-component-border);
  border-radius: var(--app-radius-lg);
  color: var(--td-text-color-primary);
  background: var(--td-bg-color-container);
  overflow-wrap: anywhere;
  text-decoration: none;
}
.protected-resource-card::before { content: '↗'; margin-right: 12px; }
.protected-resource-card:hover { border-color: var(--td-brand-color); }
</style>
