import { createRouter, createWebHistory } from 'vue-router'
import { defineComponent } from 'vue'
import type { RouteLocationNormalized } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useDeploymentCapabilitiesStore } from '@/stores/deploymentCapabilities'
import { useEditorResourcesStore } from '@/stores/editorResources'
import { autoSetup, userInfoFromApi } from '@/api/auth'
import type { DeploymentCapabilityKey } from '@/config/deploymentCapabilities'
import { MessagePlugin } from 'tdesign-vue-next'
import i18n from '@/i18n'
import { normalizeSettingsSection } from '@/config/settingsRoute'
import { isToolboxSection, toolboxLocation } from '@/config/toolbox'

/** Lite /桌面 WebView 硬刷新时可能只打开 `/`，用 session 记住上次页面以便恢复 */
const LITE_LAST_PATH_KEY = 'weknora_lite_last_path'

// views/platform/index.vue always mounts the settings modal and opens it when
// the path is /platform/settings, so this route only has to own the URL.
// Rendering Settings.vue here would mount a second, independent copy.
const SettingsRouteOutlet = defineComponent({ name: 'SettingsRouteOutlet', render: () => null })

function isLiteEdition(authStore: ReturnType<typeof useAuthStore>) {
  return authStore.isLiteMode || localStorage.getItem('weknora_lite_mode') === 'true'
}

function isLiteSpaDefaultEntry(to: RouteLocationNormalized) {
  return (
    to.path === '/' ||
    to.path === '/platform' ||
    to.path === '/platform/knowledge-bases' ||
    to.name === 'knowledgeBaseList'
  )
}

function isSafeLiteRestoreTarget(path: string) {
  return path.startsWith('/platform/') && !path.startsWith('/platform/organizations')
}

function hasPendingOIDCCallback() {
  if (typeof window === 'undefined') return false
  const hash = window.location.hash || ''
  return hash.includes('oidc_result=') || hash.includes('oidc_error=')
}

const router = createRouter({
  history: createWebHistory(import.meta.env.BASE_URL),
  routes: [
    {
      path: "/",
      redirect: "/platform/knowledge-bases",
    },
    {
      path: "/login",
      name: "login",
      component: () => import("../views/auth/Login.vue"),
      meta: { requiresAuth: false, requiresInit: false }
    },
    // Embed chat is a separate entry (embed.html + embed-main.ts), not this SPA.
    {
      path: "/register",
      name: "registerByInvite",
      // Share-link landing page reuses the Login form: the same Vue
      // component renders both modes and detects ?token=xxx on mount
      // to switch into invite-register flow. Avoids a parallel page
      // that would duplicate the OIDC / language-switch / styling
      // surface for one extra field.
      component: () => import("../views/auth/Login.vue"),
      meta: { requiresAuth: false, requiresInit: false }
    },
    {
      path: "/onboarding/workspace",
      name: "workspaceOnboarding",
      component: () => import("../views/auth/WorkspaceOnboarding.vue"),
      meta: { requiresAuth: true, requiresInit: false, requiresTenant: false }
    },
    {
      path: "/join",
      name: "joinOrganization",
      // 重定向到组织列表页，并将 code 参数转换为 invite_code
      redirect: (to) => {
        const code = to.query.code as string
        return {
          path: '/platform/organizations',
          query: code ? { invite_code: code } : {}
        }
      },
      meta: { requiresInit: true, requiresAuth: true }
    },
    {
      path: "/knowledgeBase",
      name: "home",
      component: () => import("../views/knowledge/KnowledgeBase.vue"),
      meta: { requiresInit: true, requiresAuth: true }
    },
    {
      path: "/platform",
      name: "Platform",
      redirect: "/platform/knowledge-bases",
      component: () => import("../views/platform/index.vue"),
      meta: { requiresInit: true, requiresAuth: true },
      children: [
        {
          path: "tenant",
          redirect: "/platform/settings"
        },
        {
          path: "settings",
          name: "settings",
          component: SettingsRouteOutlet,
          meta: { requiresInit: true, requiresAuth: true }
        },
        {
          path: "knowledge-bases",
          name: "knowledgeBaseList",
          component: () => import("../views/knowledge/KnowledgeBaseList.vue"),
          meta: { requiresInit: true, requiresAuth: true }
        },
        {
          path: "knowledge-bases/:kbId",
          name: "knowledgeBaseDetail",
          component: () => import("../views/knowledge/KnowledgeBase.vue"),
          meta: { requiresInit: true, requiresAuth: true }
        },
        {
          path: "knowledge-search",
          // 旧路径保留为重定向，打开全局命令面板（⌘K），带上可选的 q 参数
          redirect: (to) => {
            const q = to.query.q
            return {
              path: '/platform/knowledge-bases',
              query: typeof q === 'string' ? { cmdk: q } : { cmdk: '' },
            }
          },
        },
        {
          path: "artifacts",
          name: "artifactLibrary",
          component: () => import("../views/artifacts/ArtifactLibrary.vue"),
          meta: { requiresInit: true, requiresAuth: true, requiredCapability: 'settings.sandbox' }
        },
        {
          path: "toolbox/:section?",
          name: "toolbox",
          component: () => import("../views/toolbox/Toolbox.vue"),
          meta: { requiresInit: true, requiresAuth: true }
        },
        {
          path: "automations/:automationId?",
          name: "automationList",
          component: () => import("../views/automation/AutomationList.vue"),
          meta: { requiresInit: true, requiresAuth: true }
        },
        {
          path: "agents",
          name: "agentList",
          component: () => import("../views/agent/AgentList.vue"),
          meta: { requiresInit: true, requiresAuth: true, requiredCapability: 'agents' }
        },
        {
          // 会议室一体化页面：左侧会议卡片列表常驻，右侧为子路由出口（空态 / 详情）
          path: "meetings",
          component: () => import("../views/meeting/MeetingLayoutView.vue"),
          meta: { requiresInit: true, requiresAuth: true },
          children: [
            {
              path: "",
              name: "meetingList",
              component: () => import("../views/meeting/MeetingEmptyView.vue"),
              meta: { requiresInit: true, requiresAuth: true }
            },
            {
              path: ":roomId",
              name: "meetingRoom",
              component: () => import("../views/meeting/MeetingRoomView.vue"),
              meta: { requiresInit: true, requiresAuth: true }
            },
          ],
        },
        {
          // SOP 工作台：顶部五 Tab + 项目切换（项目 id 走 store/localStorage，不进路由）。
          // 子页用 meta.sopTab 保持所属主 Tab 高亮。
          path: "sop",
          component: () => import("../views/sop/SopLayoutView.vue"),
          meta: { requiresInit: true, requiresAuth: true, sopTab: "items" },
          children: [
            {
              path: "",
              redirect: { name: "sopItems" },
            },
            {
              path: "items",
              name: "sopItems",
              component: () => import("../views/sop/SopItemBoard.vue"),
              meta: { requiresInit: true, requiresAuth: true, sopTab: "items" },
            },
            {
              path: "templates",
              name: "sopTemplates",
              component: () => import("../views/sop/SopTemplateList.vue"),
              meta: { requiresInit: true, requiresAuth: true, sopTab: "templates" },
            },
            {
              path: "templates/designer/:id",
              name: "sopDesigner",
              component: () => import("../views/sop/SopDesigner.vue"),
              meta: { requiresInit: true, requiresAuth: true, sopTab: "templates" },
            },
            {
              path: "runs",
              name: "sopRuns",
              component: () => import("../views/sop/SopRunList.vue"),
              meta: { requiresInit: true, requiresAuth: true, sopTab: "runs" },
            },
            {
              path: "runs/:id",
              name: "sopRunDetail",
              component: () => import("../views/sop/SopRunDetail.vue"),
              meta: { requiresInit: true, requiresAuth: true, sopTab: "runs" },
            },
            {
              path: "approvals",
              name: "sopApprovals",
              component: () => import("../views/sop/SopApprovalCenter.vue"),
              meta: { requiresInit: true, requiresAuth: true, sopTab: "approvals" },
            },
            {
              path: "tasks",
              name: "sopTasks",
              component: () => import("../views/sop/SopTriggerTab.vue"),
              meta: { requiresInit: true, requiresAuth: true, sopTab: "tasks" },
            },
            {
              path: "settings/project",
              name: "sopProjectSettings",
              component: () => import("../views/sop/SopProjectSettings.vue"),
              meta: { requiresInit: true, requiresAuth: true, sopTab: "items" },
            },
            {
              path: "settings/cli",
              name: "sopSettings",
              component: () => import("../views/sop/SopSettings.vue"),
              meta: { requiresInit: true, requiresAuth: true, sopTab: "items" },
            },
            {
              path: "archive",
              name: "sopArchive",
              component: () => import("../views/sop/PmArchive.vue"),
              meta: { requiresInit: true, requiresAuth: true, sopTab: "items" },
            },
          ],
        },
        {
          // PM 工作站并入 SOP：旧链接（含事项定位参数）一律带到事项页，保留 query
          path: "pm",
          redirect: (to) => ({ path: "/platform/sop/items", query: to.query }),
          meta: { requiresInit: true, requiresAuth: true },
        },
        {
          // PM 的真实地址一直是 /platform/pm/:projectId/...：不带通配符只兜住空路径，
          // 所有书签都会落到空 RouterView。整段旧子路径统一重定向并保留 query。
          path: "pm/:legacy(.*)*",
          redirect: (to) => ({ path: "/platform/sop/items", query: to.query }),
          meta: { requiresInit: true, requiresAuth: true },
        },
        {
          path: "integrations",
          redirect: (to) => {
            const tab = typeof to.query.tab === 'string' ? to.query.tab : undefined
            const incoming = typeof to.query.section === 'string' ? to.query.section : 'integrations'
            const rest = { ...to.query }
            delete rest.tab
            return {
              path: '/platform/settings',
              query: {
                ...rest,
                section: normalizeSettingsSection(incoming, tab),
              },
            }
          },
          meta: { requiresInit: true, requiresAuth: true }
        },
        {
          path: "creatChat",
          name: "globalCreatChat",
          component: () => import("../views/creatChat/creatChat.vue"),
          meta: { requiresInit: true, requiresAuth: true }
        },
        {
          path: "knowledge-bases/:kbId/creatChat",
          name: "kbCreatChat",
          component: () => import("../views/creatChat/creatChat.vue"),
          meta: { requiresInit: true, requiresAuth: true }
        },
        {
          path: "doc-assistant",
          redirect: "/platform/doc-assistant/tasks",
          meta: { requiresInit: true, requiresAuth: true }
        },
        {
          path: "doc-assistant/templates",
          name: "docAssistantTemplates",
          component: () => import("../views/doc-assistant/TemplateList.vue"),
          meta: { requiresInit: true, requiresAuth: true }
        },
        {
          path: "doc-assistant/generate",
          name: "docAssistantGenerate",
          component: () => import("../views/doc-assistant/GenerateDocument.vue"),
          meta: { requiresInit: true, requiresAuth: true }
        },
        {
          path: "doc-assistant/tasks",
          name: "docAssistantTasks",
          component: () => import("../views/doc-assistant/TaskHistory.vue"),
          meta: { requiresInit: true, requiresAuth: true }
        },
        {
          path: "doc-assistant/documents/:id",
          name: "docAssistantDocumentDetail",
          component: () => import("../views/doc-assistant/DocumentDetail.vue"),
          meta: { requiresInit: true, requiresAuth: true }
        },
        {
          path: "chat/:chatid",
          name: "chat",
          component: () => import("../views/chat/index.vue"),
          meta: { requiresInit: true, requiresAuth: true }
        },
        {
          path: "organizations",
          name: "organizationList",
          component: () => import("../views/organization/OrganizationList.vue"),
          meta: { requiresInit: true, requiresAuth: true, requiredCapability: 'organizations' }
        },
        // Compatibility redirects for /platform/system/* URLs. System
        // administration surfaces live as dedicated sections inside the
        // standard Settings modal; keep stable URLs for bookmarks and
        // external links.
        {
          path: "system",
          redirect: { path: "/platform/settings", query: { section: "system-global" } },
          meta: { requiresInit: true, requiresAuth: true, requiresSystemAdmin: true },
        },
        {
          path: "system/settings",
          name: "systemSettings",
          redirect: { path: "/platform/settings", query: { section: "system-global" } },
          meta: { requiresInit: true, requiresAuth: true, requiresSystemAdmin: true },
        },
        {
          path: "system/admins",
          name: "systemAdmins",
          redirect: { path: "/platform/settings", query: { section: "system-global" } },
          meta: { requiresInit: true, requiresAuth: true, requiresSystemAdmin: true },
        },
        {
          path: "system/queues",
          name: "systemQueues",
          redirect: { path: "/platform/settings", query: { section: "runtime-queues" } },
          meta: { requiresInit: true, requiresAuth: true, requiresSystemAdmin: true },
        },
        {
          // 工作台：左侧工作台二级导航 + 右侧内容区；BlockSuite 只随 workbenchItem 异步加载。
          path: "workbench",
          component: () => import("../views/workbench/WorkbenchLayout.vue"),
          // requiresWorkbench：总开关（§15.3）由 system/info 的 workbench_enabled 下发，
          // 关闭时不让用户停留在 /platform/workbench/*，回到知识库列表。
          meta: { requiresInit: true, requiresAuth: true, requiresWorkbench: true },
          children: [
            {
              path: "",
              redirect: "/platform/workbench/recent",
            },
            {
              path: "recent",
              name: "workbenchRecent",
              component: () => import("../views/workbench/WorkbenchList.vue"),
              props: { scope: "recent" },
              meta: { requiresInit: true, requiresAuth: true },
            },
            {
              path: "insights",
              name: "workbenchInsights",
              component: () => import("../views/workbench/InsightsView.vue"),
              meta: { requiresInit: true, requiresAuth: true },
            },
            {
              path: "journals",
              name: "workbenchJournals",
              component: () => import("../views/workbench/JournalsView.vue"),
              meta: { requiresInit: true, requiresAuth: true },
            },
            {
              // Journals 选中某天：日期只走 YYYY-MM-DD，不做时区换算
              path: "journals/:date",
              name: "workbenchJournalDay",
              component: () => import("../views/workbench/JournalsView.vue"),
              props: (route: RouteLocationNormalized) => ({ date: String(route.params.date || "") }),
              meta: { requiresInit: true, requiresAuth: true },
            },
            {
              // 章节文档：入口在工作知识库详情页，不挂二级导航（§9.9）
              path: "docs",
              name: "workbenchDocs",
              component: () => import("../views/workbench/WorkbenchDocs.vue"),
              meta: { requiresInit: true, requiresAuth: true },
            },
            {
              path: "databases",
              name: "workbenchDatabases",
              component: () => import("../views/workbench/WorkbenchList.vue"),
              props: { scope: "database" },
              meta: { requiresInit: true, requiresAuth: true },
            },
            {
              path: "favorites",
              name: "workbenchFavorites",
              component: () => import("../views/workbench/WorkbenchList.vue"),
              props: { scope: "favorites" },
              meta: { requiresInit: true, requiresAuth: true },
            },
            {
              path: "trash",
              name: "workbenchTrash",
              component: () => import("../views/workbench/WorkbenchList.vue"),
              props: { scope: "trash" },
              meta: { requiresInit: true, requiresAuth: true },
            },
            {
              path: "folders/:folderId",
              name: "workbenchFolder",
              component: () => import("../views/workbench/WorkbenchList.vue"),
              props: (route: RouteLocationNormalized) => ({
                scope: "folder",
                folderId: String(route.params.folderId || ""),
              }),
              meta: { requiresInit: true, requiresAuth: true },
            },
            {
              // 模板管理：个人模板的列表/改名/删除；发布门禁关闭时入口不出现
              path: "templates",
              name: "workbenchTemplates",
              component: () => import("../views/workbench/WorkbenchTemplates.vue"),
              meta: { requiresInit: true, requiresAuth: true },
            },
            {
              // 编辑页：唯一会加载 BlockSuite 的路由
              path: "items/:itemId",
              name: "workbenchItem",
              component: () => import("../views/workbench/WorkbenchItemView.vue"),
              meta: { requiresInit: true, requiresAuth: true },
            },
            {
              // 工作台设置：个人偏好（编辑器 15 项 + 工作台 AI 4 项）；?tab=editor|ai
              path: "settings",
              name: "workbenchSettings",
              component: () => import("../views/workbench/WorkbenchSettings.vue"),
              meta: { requiresInit: true, requiresAuth: true },
            },
          ],
        },
      ],
    },
    // Dev-only markdown rendering test page
    ...(import.meta.env.DEV ? [{
      path: '/platform/dev/markdown',
      name: 'markdownTest',
      component: () => import('../views/dev/MarkdownTestPage.vue'),
      meta: { requiresAuth: false, requiresInit: false }
    }] : []),
  ],
});

// 持久化 auto-setup / login 返回的认证信息到 store
function persistLoginResponse(authStore: ReturnType<typeof useAuthStore>, response: any) {
  const activeTenant = response.active_tenant || response.tenant
  if (response.user && response.token) {
    const homeTenantId = response.user.tenant_id ?? activeTenant?.id ?? ''
    authStore.setUser(userInfoFromApi(response.user, homeTenantId))
    authStore.setToken(response.token)
    if (response.refresh_token) {
      authStore.setRefreshToken(response.refresh_token)
    }
    if (activeTenant) {
      authStore.setTenant({
        id: String(activeTenant.id) || '',
        name: activeTenant.name || '',
        owner_id: response.user.id || '',
        created_at: activeTenant.created_at || new Date().toISOString(),
        updated_at: activeTenant.updated_at || new Date().toISOString()
      })
    } else {
      authStore.setTenant(null)
    }
    if (Array.isArray(response.memberships)) {
      authStore.setMemberships(response.memberships)
    }
  }
}

async function hydrateSessionFromToken(authStore: ReturnType<typeof useAuthStore>) {
  const token = localStorage.getItem('weknora_token')
  if (!token) return false

  if (!authStore.token) {
    authStore.setToken(token)
  }

  const storedRefreshToken = localStorage.getItem('weknora_refresh_token')
  if (storedRefreshToken && !authStore.refreshToken) {
    authStore.setRefreshToken(storedRefreshToken)
  }

  // /auth/me 的落库逻辑只在 auth store 里维护一份（user / tenant / memberships /
  // capabilities），这里只负责先把 token 放进 store。请求本身与启动、侧栏共用去重。
  return authStore.refreshFromAuthMe()
}

let autoSetupAttempted = false
let liteDeepLinkRestoreDone = false

// 路由守卫：检查认证状态和系统初始化状态
router.beforeEach(async (to, from, next) => {
  const authStore = useAuthStore()

  // OIDC 回跳登录结果依赖 App.vue 在挂载后消费 URL hash。
  // 如果这里先按“未登录”拦截到 /login，会导致回调结果没有机会落盘。
  if (hasPendingOIDCCallback()) {
    next()
    return
  }

  // Preserve bookmarks for tools that have moved out of Settings.
  if (to.path === '/platform/settings' && isToolboxSection(to.query.section)) {
    next({ ...toolboxLocation(to.query.section,
      typeof to.query.sandboxId === 'string' ? to.query.sandboxId : undefined), replace: true })
    return
  }

  // Lite：硬刷新后若落在默认首页，恢复本次会话中最后访问的 /platform 子路径
  if (!liteDeepLinkRestoreDone) {
    liteDeepLinkRestoreDone = true
    if (isLiteEdition(authStore)) {
      const saved = sessionStorage.getItem(LITE_LAST_PATH_KEY)
      if (saved && isSafeLiteRestoreTarget(saved) && isLiteSpaDefaultEntry(to)) {
        if (saved !== to.fullPath) {
          next(saved)
          return
        }
      }
    }
  }

  // Tenantless onboarding still requires a valid user token even though it
  // deliberately skips the normal tenant/system-initialization gates.
  if (to.path === '/onboarding/workspace') {
    if (!authStore.isLoggedIn) {
      const restored = await hydrateSessionFromToken(authStore)
      if (!restored) {
        next('/login')
        return
      }
    }
    if (authStore.hasValidTenant) {
      next('/platform/knowledge-bases')
    } else {
      next()
    }
    return
  }

  // 如果访问的是登录页面或初始化页面，直接放行
  if (to.meta.requiresAuth === false || to.meta.requiresInit === false) {
    // 如果已登录用户访问登录页面，重定向到知识库列表页面
    if (to.path === '/login' && authStore.isLoggedIn) {
      next(authStore.hasValidTenant ? '/platform/knowledge-bases' : '/onboarding/workspace')
      return
    }
    next()
    return
  }

  // 检查用户认证状态
  if (to.meta.requiresAuth !== false) {
    if (!authStore.isLoggedIn) {
      const restored = await hydrateSessionFromToken(authStore)
      if (restored) {
        next(
          !authStore.hasValidTenant && to.meta.requiresTenant !== false
            ? '/onboarding/workspace'
            : to.fullPath,
        )
        return
      }

      if (!autoSetupAttempted) {
        autoSetupAttempted = true
        localStorage.removeItem('weknora_auto_setup_failed')
        try {
          const response = await autoSetup()
          if (response.success) {
            persistLoginResponse(authStore, response)
            authStore.setLiteMode(true)
            next(to.fullPath)
            return
          }
        } catch {
          // Auto-setup may be unavailable outside the native Lite shell.
        }
      }
      next('/login')
      return
    }
  }

  if (to.meta.requiresTenant !== false && !authStore.hasValidTenant) {
    next('/onboarding/workspace')
    return
  }

  // 部署能力只描述“后端是否提供该功能”，不反映服务健康或是否已配置。
  // 探测失败时 Store 会 fail-open，真正的权限和可用性仍由后端接口校验。
  //
  // 只有目标路由真的声明了 requiredCapability 才去拉取：绝大多数路由（含整个
  // 工作台）并不需要这份数据，无条件 await 会让每次导航多等一个往返，
  // 首屏表现为整段空白。
  const requiredCapability = to.meta.requiredCapability as DeploymentCapabilityKey | undefined
  if (requiredCapability) {
    const deploymentCapabilities = useDeploymentCapabilitiesStore()
    await deploymentCapabilities.ensureLoaded()
    if (!deploymentCapabilities.isSupported(requiredCapability)) {
      MessagePlugin.warning(i18n.global.t('settings.capabilityUnavailable'))
      next('/platform/knowledge-bases')
      return
    }
  }

  // SystemAdmin gate — checked AFTER auth so a non-admin who's logged
  // out gets redirected to /login first (consistent with how the rest
  // of the auth flow works), and only an authenticated non-admin sees
  // the bounce. This is UI-only; the server enforces the real check.
  if (to.meta.requiresSystemAdmin === true) {
    if (!authStore.isSystemAdmin) {
      next('/platform/knowledge-bases')
      return
    }
  }

  // 工作台总开关（§15.3）：与后端路由注册读同一份配置（system/info 的
  // workbench_enabled）。探测失败时放行，由后端 404 兜底——门禁不应该因为
  // 一次网络抖动把用户锁在登录页之外。
  if (to.meta.requiresWorkbench === true) {
    const editorResources = useEditorResourcesStore()
    try {
      await editorResources.ensureSystemInfo()
    } catch {
      // 忽略：按放行处理
    }
    if (editorResources.systemInfo?.workbench_enabled === false) {
      MessagePlugin.warning(i18n.global.t('settings.capabilityUnavailable'))
      next('/platform/knowledge-bases')
      return
    }
  }

  next()
})

router.afterEach((to) => {
  if (!isLiteEdition(useAuthStore())) return
  if (to.path === '/login') return
  if (!to.path.startsWith('/platform')) return
  sessionStorage.setItem(LITE_LAST_PATH_KEY, to.fullPath)
})

export default router
