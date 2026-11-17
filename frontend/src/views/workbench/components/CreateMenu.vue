<template>
  <!-- 新建菜单（§9.5 / §9.16.2）：导航栏与列表页空态共用，触发器由父组件决定 -->
  <t-dropdown
    :options="options"
    trigger="click"
    placement="bottom-right"
    attach="body"
    @click="onSelect"
  >
    <slot />
  </t-dropdown>
</template>

<script setup lang="ts">
/**
 * 新建菜单：文档 / 画布 / 数据库 / 文件夹四选。
 *
 * 四个入口的语义（与后端 kind 对照）：
 *   - 文档：kind=canvas + current_mode=page，带 ?template=1 让编辑页弹模板选择器；
 *   - 画布：kind=canvas，初始形态走编辑器偏好，不弹模板；
 *   - 数据库：事项是全局单例，这里只负责跳到那一条，不做新建；
 *   - 文件夹：行内输入由导航的文件夹树承担，这里只上报意图。
 */
import { computed, h } from 'vue'
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { Icon as TIcon, MessagePlugin } from 'tdesign-vue-next'
import { useWorkbenchStore } from '@/stores/workbench'
import type { WorkbenchCreateAction } from '@/types/workbench'

const props = withDefaults(
  defineProps<{
    /** 新建条目落在哪个文件夹；空表示根级 */
    folderId?: string | null
  }>(),
  { folderId: null },
)

const emit = defineEmits<{
  /** 条目创建成功并已跳转 */
  created: []
  /** 请求父组件打开文件夹的行内输入 */
  'create-folder': []
}>()

const router = useRouter()
const { t } = useI18n()
const store = useWorkbenchStore()

function option(labelKey: string, value: WorkbenchCreateAction, icon: string) {
  return {
    content: t(labelKey),
    value,
    prefixIcon: () => h(TIcon, { name: icon }),
  }
}

const options = computed(() => [
  option('workbench.new.document', 'document', 'file'),
  option('workbench.new.canvas', 'canvas', 'edit'),
  option('workbench.new.database', 'database', 'table'),
  option('workbench.new.folder', 'folder', 'folder-add'),
])

function onSelect(data: { value?: string | number }) {
  const action = String(data.value ?? '') as WorkbenchCreateAction
  void run(action)
}

async function run(action: WorkbenchCreateAction) {
  switch (action) {
    case 'document':
      await createPage(true)
      break
    case 'canvas':
      await createPage(false)
      break
    case 'database':
      await openThings()
      break
    case 'folder':
      emit('create-folder')
      break
  }
}

/**
 * 新建文档 / 画布。
 *
 * withTemplate 只影响建完后的路由参数：模板入口只对空文档开放，
 * 普通画布即使偏好是 page 也不弹模板（§9.16.2）。
 */
async function createPage(withTemplate: boolean) {
  try {
    // 标题留空：由 store 按编辑器偏好决定（日期自动命名 / 占位「未命名文档」），
    // 占位值同时是模板套用后是否采用模板名的判断依据（§9.16.2）。
    const item = await store.createItem(
      'canvas',
      '',
      props.folderId,
      withTemplate ? 'page' : undefined,
    )
    emit('created')
    await router.push({
      name: 'workbenchItem',
      params: { itemId: item.id },
      ...(withTemplate && store.features.templates ? { query: { template: '1' } } : {}),
      // 标记这次是「新建跳转」：模板弹窗点关闭时要据此撤销这条空文档
      state: { wbCreated: withTemplate },
    })
  } catch {
    MessagePlugin.error(t('workbench.editor.loadFailed'))
  }
}

/** 事项是全局单例：复用同一条，不新建第二条数据库。 */
async function openThings() {
  try {
    const item = await store.ensureThingsItem()
    emit('created')
    await router.push({
      name: 'workbenchItem',
      params: { itemId: item.id },
      query: { focus: 'database' },
    })
  } catch {
    MessagePlugin.error(t('workbench.list.loadFailed'))
  }
}
</script>
