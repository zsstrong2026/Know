import common from './zh-TW/common.ts'
import auth from './zh-TW/auth.ts'
import chat from './zh-TW/chat.ts'
import agent from './zh-TW/agent.ts'
import knowledge from './zh-TW/knowledge.ts'
import datasource from './zh-TW/datasource.ts'
import model from './zh-TW/model.ts'
import settings from './zh-TW/settings.ts'
import system from './zh-TW/system.ts'
import local from './zh-TW/local.ts'
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
