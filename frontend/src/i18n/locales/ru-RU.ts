import common from './ru-RU/common.ts'
import auth from './ru-RU/auth.ts'
import chat from './ru-RU/chat.ts'
import agent from './ru-RU/agent.ts'
import knowledge from './ru-RU/knowledge.ts'
import datasource from './ru-RU/datasource.ts'
import model from './ru-RU/model.ts'
import settings from './ru-RU/settings.ts'
import system from './ru-RU/system.ts'
import local from './ru-RU/local.ts'
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
