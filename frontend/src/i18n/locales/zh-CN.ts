import common from './zh-CN/common.ts'
import auth from './zh-CN/auth.ts'
import chat from './zh-CN/chat.ts'
import agent from './zh-CN/agent.ts'
import knowledge from './zh-CN/knowledge.ts'
import datasource from './zh-CN/datasource.ts'
import model from './zh-CN/model.ts'
import settings from './zh-CN/settings.ts'
import system from './zh-CN/system.ts'
import local from './zh-CN/local.ts'
import { mergeLocale } from './mergeLocale.ts'

// 本地增量 local 与上游域模块深合并；对外类型保持上游各域模块的交叉类型，
// 避免本地增量把 messages 类型拓宽为 Record<string, unknown>。
export default mergeLocale(
  common,
  auth,
  chat,
  agent,
  knowledge,
  datasource,
  model,
  settings,
  system,
  local,
) as typeof common &
  typeof auth &
  typeof chat &
  typeof agent &
  typeof knowledge &
  typeof datasource &
  typeof model &
  typeof settings &
  typeof system
