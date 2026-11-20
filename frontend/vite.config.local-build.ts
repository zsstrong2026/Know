// 一次性构建用配置：继承仓库默认配置，仅把 less 预处理放回主线程执行。
// 原因：Vite 的预处理器 worker 在等待主线程响应超过 5 秒时会抛 "[less] timed-out"，
// 本机构建时 chat/index.vue 的 less 块会稳定命中该假超时；preprocessorMaxWorkers=0
// 会禁用 worker 池、改用同进程编译，绕过该限制。构建完成后可删除本文件。
import baseConfig from './vite.config'

export default async function localBuildConfig(env: Parameters<typeof baseConfig>[0]) {
  const resolved = typeof baseConfig === 'function' ? await baseConfig(env) : baseConfig
  const config = resolved as Record<string, unknown>
  config.css = { ...(config.css as Record<string, unknown> | undefined), preprocessorMaxWorkers: 0 }
  return config
}
