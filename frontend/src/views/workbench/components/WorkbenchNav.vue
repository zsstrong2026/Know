<template>
  <!-- 工作台二级导航：与原型 tplNav 对齐；顺序固定，不随数据变化 -->
  <aside class="wb-nav" :aria-label="$t('workbench.nav.title')">
    <div class="wb-nav-head">
      <div class="wb-nav-title">
        {{ $t('workbench.nav.title') }}
        <span>{{ $t('workbench.nav.beta') }}</span>
      </div>
      <!-- 齿轮：工作台设置（二级菜单，不是全局设置，也不是左侧导航项） -->
      <t-dropdown
        :options="settingsOptions"
        trigger="click"
        placement="bottom-right"
        attach="body"
        @click="onSettings"
      >
        <button class="wb-nav-gear" type="button" :aria-label="$t('workbench.settings.open')">
          <t-icon name="setting" size="15px" />
        </button>
      </t-dropdown>
    </div>

    <div class="wb-new-wrap">
      <!-- 「新建」是新建文档的快捷入口：一步建好并跳到编辑页，不再弹类型菜单。 -->
      <button class="wb-new" type="button" @click="createDocument">
        <t-icon name="add" size="15px" />
        <span>{{ $t('workbench.new.label') }}</span>
      </button>
    </div>

    <div class="wb-nav-scroll">
      <RouterLink
        v-for="entry in navEntries"
        :key="entry.name"
        class="nav-item"
        :class="{ 'is-active': isActive(entry.match) }"
        :to="{ name: entry.name }"
      >
        <t-icon :name="entry.icon" size="16px" />
        <span>{{ $t(entry.labelKey) }}</span>
      </RouterLink>

      <div class="nav-group-label">
        <span>{{ $t('workbench.nav.folders') }}</span>
        <button
          class="nav-group-add"
          type="button"
          :aria-label="$t('workbench.nav.newFolder')"
          @click="startCreateFolder"
        >
          <t-icon name="add" size="13px" />
        </button>
      </div>
      <FolderTree
        ref="folderTreeRef"
        :folders="store.folders"
        :active-id="activeFolderId"
        @select="openFolder"
        @create-folder="onCreateFolder"
        @rename-folder="onRenameFolder"
        @move-folder="onMoveFolder"
        @delete-folder="onDeleteFolder"
        @add-existing="onAddExistingDocs"
        @create-item="onCreateItemInFolder"
      />
    </div>

    <div class="wb-nav-foot">
      <!-- 工作知识库入口：仅自己可见，管理员也不可读（§9.1） -->
      <button v-if="store.workKbId" class="wk-card" type="button" @click="openWorkKb">
        <span class="wk-icon"><t-icon name="book" size="15px" /></span>
        <span class="wk-copy">
          <span class="wk-name">{{ store.bootstrap?.work_kb.name }}</span>
          <span class="wk-meta">
            <t-icon name="lock-on" size="10px" />
            {{ $t('workbench.nav.workKbDocs', { count: store.bootstrap?.work_kb.knowledge_count ?? 0 }) }}
          </span>
        </span>
      </button>
    </div>

    <!-- 添加已有文档：文件夹行菜单 → 弹窗多选后批量移入 -->
    <AddDocsToFolderDialog
      v-model="addDocsOpen"
      :folder-id="addDocsFolder?.id ?? ''"
      :folder-name="addDocsFolder?.name ?? ''"
      @added="onDocsAdded"
    />
  </aside>
</template>

<script setup lang="ts">
/**
 * 工作台导航。
 *
 * 只负责导航与“新建”动作；文件夹数据由 layout 统一拉取后放进 store，
 * 避免每个子页面各自请求一次。
 */
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import { useWorkbenchStore } from '@/stores/workbench'
import FolderTree from './FolderTree.vue'
import AddDocsToFolderDialog from './AddDocsToFolderDialog.vue'
import { todayISO } from '@/workbench/journals/date-only'

const props = defineProps<{
  /** 当前选中的文件夹 id（来自路由参数）；为空表示不在文件夹视图 */
  activeFolderId?: string
}>()

const emit = defineEmits<{
  /** 新建成功后通知 layout 刷新列表 */
  created: []
}>()

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const store = useWorkbenchStore()

// 列表页「新建文件夹」只派发事件，由导航统一触发文件夹树的行内输入。
onMounted(() => {
  window.addEventListener('workbench:create-folder', onCreateFolderRequested)
})

onUnmounted(() => {
  window.removeEventListener('workbench:create-folder', onCreateFolderRequested)
})

/** 文件夹树的“新建”输入由子组件管理；头部 + 复用同一套行内输入。 */
const folderTreeRef = ref<InstanceType<typeof FolderTree> | null>(null)

/** 「添加已有文档」弹窗：目标文件夹在打开时确定。 */
const addDocsOpen = ref(false)
const addDocsFolder = ref<{ id: string; name: string } | null>(null)

/** 导航行：Journals 受发布门禁控制，关闭时不出现（§9.1）。 */
const navEntries = computed(() =>
  [
    { name: 'workbenchRecent', labelKey: 'workbench.nav.recent', icon: 'history', match: 'workbenchRecent' },
    { name: 'workbenchJournals', labelKey: 'workbench.nav.journals', icon: 'calendar', match: 'workbenchJournals', gate: 'journals' },
    { name: 'workbenchDatabases', labelKey: 'workbench.nav.databases', icon: 'table', match: 'workbenchDatabases' },
    { name: 'workbenchInsights', labelKey: 'workbench.nav.insights', icon: 'chart-analytics', match: 'workbenchInsights' },
    { name: 'workbenchFavorites', labelKey: 'workbench.nav.favorites', icon: 'star', match: 'workbenchFavorites' },
    { name: 'workbenchTemplates', labelKey: 'workbench.nav.templates', icon: 'template', match: 'workbenchTemplates', gate: 'templates' },
    { name: 'workbenchTrash', labelKey: 'workbench.nav.trash', icon: 'delete', match: 'workbenchTrash' },
  ].filter((entry) => !entry.gate || store.features[entry.gate as keyof typeof store.features]),
)

/** 齿轮二级菜单：只有「编辑器」与「工作台 AI」两项（§9.14.1）。 */
const settingsOptions = computed(() => [
  { content: t('workbench.settings.tab.editor'), value: 'editor' },
  { content: t('workbench.settings.tab.ai'), value: 'ai' },
])

function onSettings(data: { value?: string | number }) {
  const tab = String(data.value ?? 'editor')
  void router.push({ name: 'workbenchSettings', query: { tab } })
}

function isActive(match: string): boolean {
  const name = String(route.name ?? '')
  // 事项是单例，落在条目编辑路由上，用 focus 区分「事项」与「最近」
  const onThings = name === 'workbenchItem' && String(route.query.focus ?? '') === 'database'
  if (match === 'workbenchRecent') {
    // 条目编辑页也属于“最近”语境，高亮它避免侧栏失去选中态
    return name === 'workbenchRecent' || (name === 'workbenchItem' && !onThings)
  }
  if (match === 'workbenchDatabases') return onThings
  if (match === 'workbenchJournals') {
    return name === 'workbenchJournals' || name === 'workbenchJournalDay'
  }
  return name === match
}

/** 头部「+」：复用文件夹树的行内输入，在根级新建文件夹。 */
function startCreateFolder() {
  folderTreeRef.value?.startCreate(null)
}

/**
 * 侧栏「新建」：新建文档的快捷入口。
 *
 * 直接建一篇 page 形态的文档并跳到编辑页，同时把 journal_date 写成当天，
 * 让它在 Journals 的「今天」里能直接看到。空标题仍按编辑器偏好生成。
 */
async function createDocument() {
  try {
    const item = await store.createItem(
      'canvas',
      '',
      props.activeFolderId || null,
      'page',
      todayISO(),
    )
    emit('created')
    await router.push({
      name: 'workbenchItem',
      params: { itemId: item.id },
      ...(store.features.templates ? { query: { template: '1' } } : {}),
      // 标记这次是「新建跳转」：模板弹窗点关闭时要据此撤销这条空文档
      state: { wbCreated: store.features.templates },
    })
  } catch {
    MessagePlugin.error(t('workbench.editor.loadFailed'))
  }
}

/** 列表页的「新建文件夹」也走同一条行内输入（跨组件用事件，避免层层透传）。 */
function onCreateFolderRequested() {
  startCreateFolder()
}

/** 文件夹树提交新建（根级或某个文件夹下）。 */
async function onCreateFolder(payload: { name: string; parentId: string | null }) {
  try {
    const folder = await store.addFolder(payload.name, payload.parentId)
    await router.push({ name: 'workbenchFolder', params: { folderId: folder.id } })
  } catch {
    MessagePlugin.error(t('workbench.nav.folderCreateFailed'))
  }
}

/**
 * 拖拽排序 / 跨层移动（P4-2）。
 *
 * 落点由 FolderTree 用纯函数算好（含「不能拖进自己子孙」的拦截），
 * 这里只负责调接口；成功后 store 会回源一次文件夹列表，顺序以服务端为准。
 */
async function onMoveFolder(payload: { id: string; parentId: string | null; position: number }) {
  try {
    await store.moveFolder(payload.id, payload.parentId, payload.position)
  } catch {
    MessagePlugin.error(t('workbench.nav.folderMoveFailed'))
  }
}

/** 行内重命名提交。 */
async function onRenameFolder(payload: { id: string; name: string }) {
  try {
    await store.renameFolder(payload.id, payload.name)
  } catch {
    MessagePlugin.error(t('workbench.nav.folderRenameFailed'))
  }
}

/**
 * 删除文件夹：后端把子文件夹与文档上移到父级，不级联删除。
 * 若正在查看该文件夹，删除后回到父级（没有父级则回最近）。
 */
function onDeleteFolder(folder: { id: string; name: string; parent_id: string | null }) {
  const dialog = DialogPlugin.confirm({
    header: t('workbench.nav.deleteFolderTitle'),
    body: t('workbench.nav.deleteFolderBody', { name: folder.name }),
    theme: 'warning',
    confirmBtn: t('workbench.nav.deleteFolderConfirm'),
    onConfirm: async () => {
      try {
        await store.removeFolder(folder.id)
        window.dispatchEvent(new CustomEvent('workbench:items-changed'))
        MessagePlugin.success(t('workbench.nav.folderDeleted'))
        if (route.name === 'workbenchFolder' && String(route.params.folderId ?? '') === folder.id) {
          if (folder.parent_id) {
            await router.push({ name: 'workbenchFolder', params: { folderId: folder.parent_id } })
          } else {
            await router.push({ name: 'workbenchRecent' })
          }
        }
        dialog.hide()
      } catch {
        MessagePlugin.error(t('workbench.nav.folderDeleteFailed'))
      }
    },
  })
}

/**
 * 从文件夹行直接新建文档。
 *
 * 文件夹只收纳文档；画布是文档的查看模式，不再是独立的创建项。
 */
async function onCreateItemInFolder(payload: { folderId: string }) {
  try {
    const item = await store.createItem(
      'canvas',
      t('workbench.editor.untitled'),
      payload.folderId,
      'page',
    )
    await router.push({
      name: 'workbenchItem',
      params: { itemId: item.id },
      ...(store.features.templates ? { query: { template: '1' } } : {}),
      // 文件夹视图新建同样标记，关闭模板弹窗时撤销空文档
      state: { wbCreated: store.features.templates },
    })
  } catch {
    MessagePlugin.error(t('workbench.editor.loadFailed'))
  }
}

/** 打开「添加已有文档」弹窗。 */
function onAddExistingDocs(folder: { id: string; name: string }) {
  addDocsFolder.value = { id: folder.id, name: folder.name }
  addDocsOpen.value = true
}

/** 弹窗完成批量移动后，刷新当前列表（文件夹视图 / 最近等）。 */
function onDocsAdded() {
  window.dispatchEvent(new CustomEvent('workbench:items-changed'))
}

function openFolder(folderId: string) {
  void router.push({ name: 'workbenchFolder', params: { folderId } })
}

function openWorkKb() {
  if (!store.workKbId) return
  void router.push(`/platform/knowledge-bases/${store.workKbId}`)
}
</script>

<style lang="less" scoped>
/**
 * 胶囊浮岛（方案 A，见 docs/design/workbench-nav-capsule.html）：
 * 把整块二级导航做成独立圆角卡片，菜单项独立胶囊；
 * 选中项填充品牌绿，与全局侧栏的浅灰行拉开层级。
 */
.wb-nav {
  width: 232px;
  flex: none;
  display: flex;
  flex-direction: column;
  box-sizing: border-box;
  margin: 10px 8px 10px 10px;
  padding: 12px 10px 10px;
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: calc(var(--app-radius-xl) + 6px);
  box-shadow: 0 12px 32px rgba(22, 32, 26, 0.10);
  overflow: hidden;
  min-height: 0;
}

:root[theme-mode='dark'] .wb-nav {
  box-shadow: 0 12px 32px rgba(0, 0, 0, 0.42);
}

.wb-nav-head {
  padding: 2px 5px 10px;
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.wb-nav-title {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: var(--app-text-base);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.wb-nav-title span {
  padding: 2px 6px;
  border-radius: var(--app-radius-pill);
  background: var(--td-brand-color-1);
  color: var(--td-brand-color-7);
  font-size: var(--app-text-2xs);
  font-weight: 600;
  line-height: 1.5;
}

.wb-nav-gear {
  display: grid;
  place-items: center;
  width: 26px;
  height: 26px;
  border-radius: var(--app-radius-pill);
  color: var(--td-text-color-placeholder);

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);
  }
}

.wb-new-wrap {
  margin: 0 2px;
}

.wb-new {
  width: 100%;
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  height: 36px;
  padding: 0 var(--app-space-3);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-pill);
  background: var(--td-bg-color-container);
  color: var(--td-text-color-primary);
  font-size: var(--app-text-md);
  font-weight: 500;
  transition: border-color var(--app-motion-fast) ease, box-shadow var(--app-motion-fast) ease;

  &:hover {
    border-color: var(--td-brand-color);
    box-shadow: 0 0 0 3px var(--td-brand-color-1);
  }
}

.wb-new :deep(.t-icon) {
  color: var(--td-brand-color);
}

.wb-nav-scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  /* 向两侧借出卡片内边距，让胶囊的柔和投影不被滚动容器裁掉 */
  margin: 0 -6px;
  padding: 10px 6px 8px;
  scrollbar-width: thin;
  scrollbar-color: transparent transparent;

  &:hover {
    scrollbar-color: var(--td-scrollbar-color) transparent;
  }
}

.nav-item {
  display: flex;
  align-items: center;
  gap: 9px;
  height: 38px;
  padding: 0 var(--app-space-3);
  margin-bottom: 2px;
  border-radius: var(--app-radius-pill);
  font-size: var(--app-text-md);
  color: var(--td-text-color-secondary);
  text-decoration: none;
  transition:
    background-color var(--app-motion-fast) ease,
    color var(--app-motion-fast) ease,
    box-shadow var(--app-motion-fast) ease;

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }

  &.is-active {
    background-image: linear-gradient(135deg, var(--td-brand-color-4), var(--td-brand-color-6));
    color: var(--td-text-color-anti);
    font-weight: 500;
    box-shadow:
      0 10px 20px color-mix(in srgb, var(--td-brand-color) 28%, transparent),
      inset 0 1px 0 rgba(255, 255, 255, 0.18);
  }
}

.nav-group-label {
  display: flex;
  align-items: center;
  margin: 13px 0 5px;
  padding: 11px 9px 0;
  border-top: 1px solid var(--td-component-stroke);
  font-size: var(--app-text-2xs);
  letter-spacing: 0.05em;
  color: var(--td-text-color-placeholder);
}

.nav-group-add {
  margin-left: auto;
  display: grid;
  place-items: center;
  width: 18px;
  height: 18px;
  border-radius: var(--app-radius-sm);
  color: var(--td-text-color-placeholder);

  &:hover {
    background: var(--td-bg-color-secondarycontainer);
    color: var(--td-text-color-primary);
  }
}

.wb-nav-foot {
  padding: 12px 0 0;
}

.wk-card {
  width: 100%;
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  padding: var(--app-space-2);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-xl);
  background: var(--td-bg-color-secondarycontainer);
  text-align: left;
  transition: border-color var(--app-motion-fast) ease;

  &:hover {
    border-color: var(--td-brand-color);
  }
}

.wk-icon {
  width: 28px;
  height: 28px;
  flex: none;
  display: grid;
  place-items: center;
  border-radius: var(--app-radius-md);
  background: var(--td-brand-color-1);
  color: var(--td-brand-color);
}

.wk-copy {
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.wk-name {
  font-size: var(--app-text-sm);
  color: var(--td-text-color-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.wk-meta {
  display: flex;
  align-items: center;
  gap: var(--app-space-1);
  font-size: var(--app-text-2xs);
  color: var(--td-text-color-placeholder);
}
</style>
