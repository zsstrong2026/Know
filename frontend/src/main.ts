import { createApp } from "vue";
import { createPinia } from "pinia";
import App from "./App.vue";
import router from "./router";
import TDesign from "tdesign-vue-next";
// 引入组件库的少量全局样式变量
import "tdesign-vue-next/dist/tdesign.css";
import "@/assets/theme/theme.css";
import "@/assets/theme/tdesign-overrides.less";
import "@/assets/dropdown-menu.less";
import "@/components/css/chat-hljs-theme.less";
import "@/components/css/assistant-page-fill.less";
// vue-virtual-scroller ships its own tiny stylesheet — required for
// RecycleScroller/DynamicScroller to size their viewport correctly.
// Without it the scroller computes 0 height and renders no items.
import "vue-virtual-scroller/dist/vue-virtual-scroller.css";
import i18n, { initI18n } from "./i18n";
import { initTheme } from "@/composables/useTheme";
import { initFont } from "@/composables/useFont";
import { installTDesignIconOfflineGuard } from "@/utils/tdesign-icon-offline";
import { installAutofillGuard } from "@/utils/disable-autofill";
import { useAuthStore } from "@/stores/auth";

// 必须在 Vue 组件挂载之前执行，避免 tdesign-icons 运行时请求 tdesign.gtimg.com
installTDesignIconOfflineGuard();

initTheme();
initFont();

async function bootstrap() {
  const app = createApp(App);

  // 全局错误处理：捕获未处理的组件错误，防止白屏
  app.config.errorHandler = (err, instance, info) => {
    console.error("[WeKnora] Unhandled Vue error:", err, "\nComponent:", instance, "\nInfo:", info);
  };

  app.use(TDesign);
  const pinia = createPinia();
  app.use(pinia);

  try {
    await initI18n();
  } catch (err) {
    console.error("[WeKnora] Failed to load initial locale bundle:", err);
  }

  // Capabilities (can_create_tenant, auto_accept_invitation) are not cached
  // in localStorage — reconcile once when a session exists.
  //
  // 刻意不 await：登录态在 authStore 初始化时已同步从 localStorage 恢复，
  // 首屏渲染不依赖这次校准的结果（能力值只影响少数入口的显隐）。
  // 过去在这里 await 会把它和下面的 router.isReady() 串成两次连续往返，
  // 期间 #app 一直是空的，表现为整段白屏。
  const authStore = useAuthStore();
  const authMeSync = localStorage.getItem("weknora_token")
    ? authStore.refreshFromAuthMe().catch(() => false)
    : Promise.resolve(false);

  app.use(router);
  app.use(i18n);

  // 等首屏路由（含导航守卫、Lite 自动登录）完成后再挂载，避免先闪默认页再跳转
  await router.isReady();
  app.mount("#app");
  installAutofillGuard();

  // 校准结果可能在挂载后才到达；等待它结束只为让启动顺序可预测，
  // 失败已在上面被吞掉，不会影响已经渲染的界面。
  await authMeSync;
}

bootstrap();
