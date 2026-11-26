import common from './en-US/common.ts'
import auth from './en-US/auth.ts'
import chat from './en-US/chat.ts'
import agent from './en-US/agent.ts'
import knowledge from './en-US/knowledge.ts'
import datasource from './en-US/datasource.ts'
import model from './en-US/model.ts'
import settings from './en-US/settings.ts'
import system from './en-US/system.ts'
import local from './en-US/local.ts'
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
