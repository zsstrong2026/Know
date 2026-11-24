import { reactive, ref, computed, watch } from 'vue'
import { defineStore } from 'pinia'
import i18n from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { useDeploymentCapabilitiesStore } from '@/stores/deploymentCapabilities'
import type { DeploymentCapabilityKey } from '@/config/deploymentCapabilities'
import type { QuestionOrigin } from '@/utils/questionOrigin'

type MenuChild = Record<string, any>

interface MenuItem {
  title: string
  titleKey?: string
  icon: string
  path: string
  childrenPath?: string
  children?: MenuChild[]
  requiredCapability?: DeploymentCapabilityKey
}

const createMenuChildren = () => reactive<MenuChild[]>([])

export const useMenuStore = defineStore('menuStore', () => {
  const menuArr = reactive<MenuItem[]>([
    {
      title: '',
      titleKey: 'menu.newChat',
      icon: 'prefixIcon',
      path: 'creatChat',
      childrenPath: 'chat',
      children: createMenuChildren()
    },
    { title: '', titleKey: 'menu.knowledgeBase', icon: 'zhishiku', path: 'knowledge-bases' },
    // 工作台：个人私有的画布/数据库/日历。入口靠后端 workbench 路由自身鉴权，
    // 不做能力位过滤（P1 不引入 workbench_enabled 开关）。
    { title: '', titleKey: 'menu.workbench', icon: 'workbench', path: 'workbench' },
    { title: '', titleKey: 'menu.docAssistant', icon: 'doc-assistant', path: 'doc-assistant' },
    // Artifacts only exist where skills run in a sandbox.
    { title: '', titleKey: 'menu.artifacts', icon: 'artifact', path: 'artifacts', requiredCapability: 'settings.sandbox' },
    { title: '', titleKey: 'menu.agents', icon: 'agent', path: 'agents', requiredCapability: 'agents' },
    { title: '', titleKey: 'menu.meetings', icon: 'meeting', path: 'meetings' },
    // SOP 工作台：沿用 PM 的独立看板图标，避免与其他菜单项图标重复
    { title: '', titleKey: 'menu.sop', icon: 'sop', path: 'sop' },
    { title: '', titleKey: 'menu.automations', icon: 'automation', path: 'automations' },
    { title: '', titleKey: 'toolbox.title', icon: 'toolbox', path: 'toolbox' },
    { title: '', titleKey: 'menu.organizations', icon: 'organization', path: 'organizations', requiredCapability: 'organizations' },
    { title: '', titleKey: 'menu.settings', icon: 'setting', path: 'settings' },
    { title: '', titleKey: 'menu.logout', icon: 'logout', path: 'logout' }
  ])

  const isFirstSession = ref(false)
  /**
   * 工作台总开关（§15.3）。默认 true：system/info 还没回来时不隐藏入口，
   * 避免刷新时菜单闪一下；后端返回 workbench_enabled=false 才真正隐藏。
   */
  const workbenchEnabled = ref(true)
  const setWorkbenchEnabled = (enabled: boolean) => {
    workbenchEnabled.value = enabled
  }
  const firstQuery = ref('')
  const firstMentionedItems = ref<any[]>([])
  const firstModelId = ref('')
  const firstImageFiles = ref<any[]>([])
  const firstAttachmentFiles = ref<any[]>([])
  const firstQuestionOrigin = ref<QuestionOrigin | null>(null)
  const prefillQuery = ref('')

  const applyMenuTranslations = () => {
    menuArr.forEach(item => {
      if (item.titleKey) {
        item.title = i18n.global.t(item.titleKey)
      }
    })
  }

  applyMenuTranslations()

  watch(
    () => i18n.global.locale.value,
    () => {
      applyMenuTranslations()
    }
  )

  const liteHiddenPaths = new Set(['logout'])

  // 共享空间 (organizations) 仅对当前空间的 admin / owner 暴露入口。
  // viewer / contributor 即便在共享空间里拥有资源，也无需自行管理共享关系，
  // 入口在侧栏只会徒增噪音；后端 RBAC 才是权限的最终来源（见 middleware/rbac.go）。
  const visibleMenuArr = computed(() => {
    const authStore = useAuthStore()
    const deploymentCapabilities = useDeploymentCapabilitiesStore()
    return menuArr.filter(item => {
      if (authStore.isLiteMode && liteHiddenPaths.has(item.path)) {
        return false
      }
      if (item.path === 'organizations' && !authStore.hasRole('admin')) {
        return false
      }
      if (!deploymentCapabilities.isSupported(item.requiredCapability)) {
        return false
      }
      // 工作台总开关关闭时隐藏侧栏入口（§15.3）；后端同时不注册路由。
      if (item.path === 'workbench' && !workbenchEnabled.value) {
        return false
      }
      return true
    })
  })

  const chatMenuIndex = menuArr.findIndex(item => item.path === 'creatChat')

  const clearMenuArr = () => {
    const chatMenu = menuArr[chatMenuIndex]
    if (chatMenu && chatMenu.children) {
      chatMenu.children = createMenuChildren()
    }
  }

  const updatemenuArr = (obj: any) => {
    const chatMenu = menuArr[chatMenuIndex]
    if (!chatMenu.children) {
      chatMenu.children = createMenuChildren()
    }
    const exists = chatMenu.children.some((item: MenuChild) => item.id === obj.id)
    if (!exists) {
      chatMenu.children.push(obj)
    }
  }

  const updataMenuChildren = (item: MenuChild) => {
    const chatMenu = menuArr[chatMenuIndex]
    if (!chatMenu.children) {
      chatMenu.children = createMenuChildren()
    }
    chatMenu.children.unshift(item)
  }

  const updatasessionTitle = (sessionId: string, title: string) => {
    const chatMenu = menuArr[chatMenuIndex]
    chatMenu.children?.forEach((item: MenuChild) => {
      if (item.id === sessionId) {
        item.title = title
        item.isNoTitle = false
      }
    })
  }

  const changeIsFirstSession = (payload: boolean) => {
    isFirstSession.value = payload
  }

  const changeFirstQuery = (payload: string, mentionedItems: any[] = [], modelId: string = '', imageFiles: any[] = [], attachmentFiles: any[] = [], questionOrigin: QuestionOrigin | null = null) => {
    firstQuery.value = payload
    firstMentionedItems.value = mentionedItems
    firstModelId.value = modelId
    firstImageFiles.value = imageFiles
    firstAttachmentFiles.value = attachmentFiles
    firstQuestionOrigin.value = questionOrigin
  }

  const setPrefillQuery = (q: string) => {
    prefillQuery.value = q
  }

  const consumePrefillQuery = () => {
    const q = prefillQuery.value
    prefillQuery.value = ''
    return q
  }

  return {
    menuArr,
    visibleMenuArr,
    workbenchEnabled,
    setWorkbenchEnabled,
    isFirstSession,
    firstQuery,
    firstMentionedItems,
    firstModelId,
    firstImageFiles,
    firstAttachmentFiles,
    firstQuestionOrigin,
    prefillQuery,
    clearMenuArr,
    updatemenuArr,
    updataMenuChildren,
    updatasessionTitle,
    changeIsFirstSession,
    changeFirstQuery,
    setPrefillQuery,
    consumePrefillQuery
  }
})
