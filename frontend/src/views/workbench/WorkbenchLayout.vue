<template>
  <!-- 工作台壳：左侧二级导航 + 右侧内容区；BlockSuite 只随条目编辑页异步加载 -->
  <div class="workbench-shell" :style="editorStyle" :data-canvas-scheme="editorPrefs.canvasScheme">
    <WorkbenchNav :active-folder-id="activeFolderId" @created="onCreated" />
    <main class="workbench-main">
      <WorkbenchNoticeBanner
        :notices="store.notices"
        @read="onNoticeRead"
        @read-all="onNoticeReadAll"
      />
      <RouterView />
    </main>
  </div>
</template>

<script setup lang="ts">
/**
 * 工作台布局。
 *
 * 职责只有三段：二级导航、内容出口、进入时拉一次 bootstrap 与文件夹树。
 * 刻意不静态 import 任何 @blocksuite/*，保证首屏不加载编辑器代码。
 *
 * 编辑器偏好在这里以 CSS 变量 + data 属性的形式挂在根节点上：
 * 只影响渲染，不写 Y.Doc、不产生 snapshot_version（§9.14.3）。
 */
import { computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { MessagePlugin } from 'tdesign-vue-next'
import { useI18n } from 'vue-i18n'
import { RouterView } from 'vue-router'
import WorkbenchNav from './components/WorkbenchNav.vue'
import WorkbenchNoticeBanner from './components/WorkbenchNoticeBanner.vue'
import { useWorkbenchStore } from '@/stores/workbench'
import { useWorkbenchPrefs } from '@/workbench/use-workbench-prefs'

const route = useRoute()
const { t } = useI18n()
const store = useWorkbenchStore()
const { editorPrefs, editorStyle } = useWorkbenchPrefs()

const activeFolderId = computed(() => String(route.params.folderId ?? ''))

onMounted(async () => {
  // bootstrap 失败通常意味着工作知识库不可用（未登录/无成员权限），
  // 此时只提示一次，列表页自己会显示空态，不阻塞导航。
  try {
    await store.fetchBootstrap()
  } catch {
    MessagePlugin.warning(t('workbench.editor.loadFailed'))
  }
  try {
    await store.fetchFolders()
  } catch {
    // 文件夹树拉取失败不影响条目列表，静默降级为空树。
  }
  // 通知是用户级资源（tenantless），独立拉取：失败只是不显示通知条。
  void store.fetchNotices()
})

/** 导航栏新建条目后，子列表页需要重新拉取；用事件触发一次即可。 */
function onCreated() {
  window.dispatchEvent(new CustomEvent('workbench:items-changed'))
}

function onNoticeRead(id: string) {
  void store.markNoticeRead(id)
}

function onNoticeReadAll() {
  void store.markAllNoticesRead()
}
</script>

<style lang="less" scoped>
.workbench-shell {
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
  background: var(--td-bg-color-page);
}

/* 画布配色（§9.14.2）：只改 Edgeless 的背景与网格变量，不重着色已有元素。
   app 保持主题自带值，因此这里只覆盖 plain / contrast 两种。 */
.workbench-shell[data-canvas-scheme='plain'] {
  --affine-edgeless-grid-color: transparent;
  --affine-background-primary-color: #ffffff;
}

.workbench-shell[data-canvas-scheme='contrast'] {
  --affine-edgeless-grid-color: var(--td-text-color-placeholder);
  --affine-background-primary-color: #ffffff;
}

.workbench-main {
  flex: 1;
  min-width: 0;
  min-height: 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
</style>
