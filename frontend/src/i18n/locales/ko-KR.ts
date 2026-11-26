import common from './ko-KR/common.ts'
import auth from './ko-KR/auth.ts'
import chat from './ko-KR/chat.ts'
import agent from './ko-KR/agent.ts'
import knowledge from './ko-KR/knowledge.ts'
import datasource from './ko-KR/datasource.ts'
import model from './ko-KR/model.ts'
import settings from './ko-KR/settings.ts'
import system from './ko-KR/system.ts'
import local from './ko-KR/local.ts'
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
