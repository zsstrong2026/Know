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
      <t-dropdown :options="createOptions" trigger="click" placement="bottom-left" @click="onCreate">
        <button class="wb-new" type="button">
          <t-icon name="add" size="15px" />
          <span>{{ $t('workbench.new.label') }}</span>
          <t-icon name="chevron-down" size="13px" class="wb-new-caret" />
        </button>
      </t-dropdown>
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
        <!-- Journals 行右端日历按钮：开/关右侧面板；不在 Journals 时先进入再打开 -->
        <button
          v-if="entry.name === 'workbenchJournals'"
          class="nav-cal-btn"
          type="button"
          :aria-label="$t('workbench.journals.togglePanel')"
          :aria-pressed="store.journalPanelOpen"
          @click.prevent.stop="toggleJournalPanel"
        >
          <t-icon name="calendar" size="13px" />
        </button>
      </RouterLink>

      <div class="nav-group-label">
        <span>{{ $t('workbench.nav.folders') }}</span>
        <button
          class="nav-group-add"
          type="button"
          :aria-label="$t('workbench.nav.newFolder')"
          @click="createFolder"
        >
          <t-icon name="add" size="13px" />
        </button>
      </div>
      <FolderTree :folders="store.folders" :active-id="activeFolderId" @select="openFolder" />
    </div>

    <div class="wb-nav-foot">
      <!-- 工作知识库入口：仅自己可见，管理员也不可读（§9.1） -->
      <button v-if="store.workKbId" class="wk-card" type="button" @click="openWorkKb">
        <span class="wk-icon"><t-icon name="book" size="15px" /></span>
        <span class="wk-copy">
          <span class="wk-name">{{ store.bootstrap?.work_kb.name }}</span>
          <span class="wk-meta">
            <t-icon name="lock-on" size="10px" />
            {{ $t('workbench.list.total', { count: store.bootstrap?.counts.items ?? 0 }) }}
          </span>
        </span>
      </button>
    </div>
  </aside>
</template>

<script setup lang="ts">
/**
 * 工作台导航。
 *
 * 只负责导航与“新建”动作；文件夹数据由 layout 统一拉取后放进 store，
 * 避免每个子页面各自请求一次。
 */
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { MessagePlugin } from 'tdesign-vue-next'
import { useWorkbenchStore } from '@/stores/workbench'
import FolderTree from './FolderTree.vue'

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

/** 导航行：Journals 受发布门禁控制，关闭时不出现（§9.1）。 */
const navEntries = computed(() =>
  [
    { name: 'workbenchRecent', labelKey: 'workbench.nav.recent', icon: 'history', match: 'workbenchRecent' },
    { name: 'workbenchJournals', labelKey: 'workbench.nav.journals', icon: 'calendar', match: 'workbenchJournals', gate: 'journals' },
    { name: 'workbenchDatabases', labelKey: 'workbench.nav.databases', icon: 'table', match: 'workbenchDatabases' },
    { name: 'workbenchFavorites', labelKey: 'workbench.nav.favorites', icon: 'star', match: 'workbenchFavorites' },
    { name: 'workbenchTrash', labelKey: 'workbench.nav.trash', icon: 'delete', match: 'workbenchTrash' },
  ].filter((entry) => !entry.gate || store.features[entry.gate as keyof typeof store.features]),
)

const createOptions = computed(() => [
  // 「新建文档」不是新的 item kind：它就是 page 形态的普通条目，
  // 只是创建后带 ?template=1 走模板选择器（§9.16.2）。模板关闭时不出现。
  ...(store.features.templates ? [{ content: t('workbench.new.document'), value: 'document' }] : []),
  { content: t('workbench.new.canvas'), value: 'canvas' },
  { content: t('workbench.new.database'), value: 'database' },
  { content: t('workbench.new.folder'), value: 'folder' },
])

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
  if (match === 'workbenchRecent') {
    // 条目编辑页也属于“最近”语境，高亮它避免侧栏失去选中态
    return name === 'workbenchRecent' || name === 'workbenchItem'
  }
  if (match === 'workbenchJournals') {
    return name === 'workbenchJournals' || name === 'workbenchJournalDay'
  }
  return name === match
}

async function onCreate(data: { value?: string | number }) {
  const value = String(data.value ?? '')
  if (value === 'folder') {
    await createFolder()
    return
  }
  if (value === 'document') {
    await createDocument()
    return
  }
  if (value !== 'canvas' && value !== 'database') return
  try {
    const item = await store.createItem(value, undefined, props.activeFolderId || null)
    emit('created')
    await router.push({ name: 'workbenchItem', params: { itemId: item.id } })
  } catch {
    MessagePlugin.error(t('workbench.editor.loadFailed'))
  }
}

/**
 * 新建文档 → 模板选择器。
 *
 * 普通「新建画布」即使编辑器偏好里默认形态是 Page，也不带 ?template=1，不弹模板。
 */
async function createDocument() {
  try {
    // 标题固定「未命名文档」：套用模板后视图据此判断是否采用模板名（§9.16.2）。
    const item = await store.createItem(
      'canvas',
      t('workbench.editor.untitled'),
      props.activeFolderId || null,
      'page',
    )
    emit('created')
    await router.push({
      name: 'workbenchItem',
      params: { itemId: item.id },
      query: { template: '1' },
    })
  } catch {
    MessagePlugin.error(t('workbench.editor.loadFailed'))
  }
}

/** 新建文件夹用浏览器内置输入，避免为一步操作引入整套对话框。 */
async function createFolder() {
  const name = window.prompt(t('workbench.nav.newFolder'))
  if (!name || !name.trim()) return
  try {
    const folder = await store.addFolder(name.trim(), props.activeFolderId || null)
    await router.push({ name: 'workbenchFolder', params: { folderId: folder.id } })
  } catch {
    MessagePlugin.error(t('workbench.item.renameFailed'))
  }
}

function openFolder(folderId: string) {
  void router.push({ name: 'workbenchFolder', params: { folderId } })
}

function openWorkKb() {
  if (!store.workKbId) return
  void router.push(`/platform/knowledge-bases/${store.workKbId}`)
}

/**
 * 日历按钮（§9.13.1 入口①）。
 *
 * 不在 Journals 时先跳进去再展开面板；已在 Journals 时只切换开合。
 */
async function toggleJournalPanel() {
  const inJournals = route.name === 'workbenchJournals' || route.name === 'workbenchJournalDay'
  if (!inJournals) {
    store.journalPanelOpen = true
    await router.push({ name: 'workbenchJournals' })
    return
  }
  store.journalPanelOpen = !store.journalPanelOpen
}
</script>

<style lang="less" scoped>
.wb-nav {
  width: 216px;
  flex: none;
  display: flex;
  flex-direction: column;
  background: var(--td-bg-color-secondarycontainer);
  border-right: 1px solid var(--td-component-stroke);
  min-height: 0;
}

.wb-nav-head {
  padding: var(--app-space-4) var(--app-space-4) var(--app-space-2);
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.wb-nav-title {
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  font-size: var(--app-text-base);
  font-weight: 600;
  color: var(--td-text-color-primary);
}

.wb-nav-title span {
  font-size: var(--app-text-2xs);
  font-weight: 500;
  color: var(--td-text-color-placeholder);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-xs);
  padding: 0 var(--app-space-1);
  line-height: var(--app-text-xl);
}

.wb-nav-gear {
  display: grid;
  place-items: center;
  width: 24px;
  height: 24px;
  border-radius: var(--app-radius-xs);
  color: var(--td-text-color-placeholder);

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }
}

.wb-new-wrap {
  margin: 0 var(--app-space-3) var(--app-space-3);
}

.wb-new {
  width: 100%;
  display: flex;
  align-items: center;
  gap: 6px;
  height: 32px;
  padding: 0 var(--app-space-2) 0 var(--app-space-3);
  border-radius: var(--app-radius-sm);
  background: var(--td-brand-color);
  color: var(--td-text-color-anti);
  font-size: var(--app-text-md);
  font-weight: 500;

  &:hover {
    background: var(--td-brand-color-hover);
  }
}

.wb-new-caret {
  margin-left: auto;
  opacity: 0.8;
}

.wb-nav-scroll {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: var(--app-space-1) var(--app-space-2);
}

.nav-item {
  display: flex;
  align-items: center;
  gap: 9px;
  height: 30px;
  padding: 0 var(--app-space-2);
  margin-bottom: 1px;
  border-radius: var(--app-radius-xs);
  font-size: var(--app-text-md);
  color: var(--td-text-color-secondary);
  text-decoration: none;

  &:hover {
    background: var(--td-bg-color-container-hover);
    color: var(--td-text-color-primary);
  }

  &.is-active {
    background: var(--td-brand-color-1);
    color: var(--td-brand-color);
    font-weight: 500;
  }
}

.nav-cal-btn {
  margin-left: auto;
  display: grid;
  place-items: center;
  width: 20px;
  height: 20px;
  flex: none;
  border-radius: var(--app-radius-xs);
  color: var(--td-text-color-placeholder);

  &:hover {
    background: var(--td-bg-color-container-active);
    color: var(--td-brand-color);
  }
}

.nav-group-label {
  display: flex;
  align-items: center;
  margin: var(--app-space-3) 0 var(--app-space-1);
  padding: 0 var(--app-space-2);
  font-size: var(--app-text-xs);
  color: var(--td-text-color-placeholder);
}

.nav-group-add {
  margin-left: auto;
  display: grid;
  place-items: center;
  color: var(--td-text-color-placeholder);

  &:hover {
    color: var(--td-text-color-primary);
  }
}

.wb-nav-foot {
  padding: var(--app-space-3);
  border-top: 1px solid var(--td-component-stroke);
}

.wk-card {
  width: 100%;
  display: flex;
  align-items: center;
  gap: var(--app-space-2);
  padding: var(--app-space-2);
  border-radius: var(--app-radius-sm);
  text-align: left;

  &:hover {
    background: var(--td-bg-color-container-hover);
  }
}

.wk-icon {
  width: 26px;
  height: 26px;
  flex: none;
  display: grid;
  place-items: center;
  border-radius: var(--app-radius-sm);
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
