import common from './ja-JP/common.ts'
import auth from './ja-JP/auth.ts'
import chat from './ja-JP/chat.ts'
import agent from './ja-JP/agent.ts'
import knowledge from './ja-JP/knowledge.ts'
import datasource from './ja-JP/datasource.ts'
import model from './ja-JP/model.ts'
import settings from './ja-JP/settings.ts'
import system from './ja-JP/system.ts'
import local from './ja-JP/local.ts'
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
