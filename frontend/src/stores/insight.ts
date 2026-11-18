/**
 * 洞察 store：主题、运行、证据、报告与自我洞悉。
 *
 * 约定与 workbench store 一致：只保存服务端状态，不拼 URL、不判断错误码；
 * 所有请求都经 @/api/insight。
 */
import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import {
  askInsightReport,
  askInsightSelf,
  cancelInsightRun,
  confirmInsightRun,
  createInsightTheme,
  deleteInsightReport,
  deleteInsightRun,
  deleteInsightTheme,
  excludeInsightEvidence,
  generateInsightSelf,
  getInsightCheckup,
  getInsightPrivacy,
  getInsightReport,
  getInsightRun,
  getInsightSelf,
  importInsightTheme,
  listInsightEvidence,
  listInsightReportMessages,
  listInsightReports,
  listInsightRuns,
  listInsightSelfMessages,
  listInsightThemes,
  startInsightRun,
  updateInsightPrivacy,
  updateInsightTheme,
  type CreateThemePayload,
  type InsightCheckup,
  type InsightEvidence,
  type InsightPrivacy,
  type InsightReportDetail,
  type InsightReportMessage,
  type InsightReportSummary,
  type InsightRun,
  type InsightScope,
  type InsightSelf,
  type InsightSelfMessage,
  type InsightTheme,
  type StartRunPayload,
} from '@/api/insight'

/** 服务端在「从未保存过」时字段可能是 null；统一成空数组，调用方无需判空。 */
function normalizePrivacy(input: InsightPrivacy | null | undefined): InsightPrivacy {
  return {
    persons: input?.persons ?? [],
    organizations: input?.organizations ?? [],
    projects: input?.projects ?? [],
    hide_evidence_quotes: !!input?.hide_evidence_quotes,
  }
}

export const useInsightStore = defineStore('insight', () => {
  const themes = ref<InsightTheme[]>([])
  const docCount = ref(0)
  const selectedId = ref('')
  const reports = ref<InsightReportSummary[]>([])
  const checkup = ref<InsightCheckup | null>(null)
  const runs = ref<InsightRun[]>([])
  const run = ref<InsightRun | null>(null)
  const evidence = ref<InsightEvidence[]>([])
  const evidenceTotal = ref(0)
  const evidenceExcluded = ref(0)
  const report = ref<InsightReportDetail | null>(null)
  const reportMessages = ref<InsightReportMessage[]>([])
  const self = ref<InsightSelf | null>(null)
  const selfMessages = ref<InsightSelfMessage[]>([])
  /** 导出层脱敏设置；页面展示与追问不读它。 */
  const privacy = ref<InsightPrivacy>({
    persons: [],
    organizations: [],
    projects: [],
    hide_evidence_quotes: false,
  })
  const loading = ref(false)

  const selected = computed(() => themes.value.find((t) => t.id === selectedId.value) ?? null)
  const builtinThemes = computed(() => themes.value.filter((t) => t.builtin))
  const customThemes = computed(() => themes.value.filter((t) => !t.builtin))
  const reportCount = computed(() => themes.value.reduce((sum, t) => sum + t.report_count, 0))
  const validEvidence = computed(() => evidenceTotal.value - evidenceExcluded.value)

  async function loadThemes() {
    loading.value = true
    try {
      const data = await listInsightThemes()
      themes.value = data.themes ?? []
      docCount.value = data.stats?.doc_count ?? 0
      if (!selectedId.value && themes.value.length) selectedId.value = themes.value[0].id
    } finally {
      loading.value = false
    }
  }

  async function selectTheme(id: string) {
    selectedId.value = id
    report.value = null
    await loadReports(id)
  }

  async function loadReports(themeId = selectedId.value) {
    if (!themeId) {
      reports.value = []
      return
    }
    reports.value = await listInsightReports(themeId)
  }

  async function loadCheckup(themeId = selectedId.value, scope?: Partial<InsightScope>) {
    checkup.value = await getInsightCheckup(themeId, scope)
    return checkup.value
  }

  async function loadRuns(themeId = selectedId.value) {
    if (!themeId) {
      runs.value = []
      return []
    }
    runs.value = await listInsightRuns(themeId)
    return runs.value
  }

  /** 删除单次运行及其证据；报告保留可读。 */
  async function removeRun(runId: string) {
    await deleteInsightRun(runId)
    await loadRuns()
    await loadThemes()
  }

  async function importTheme(file: File) {
    return importInsightTheme(file)
  }

  async function createTheme(payload: CreateThemePayload) {
    const theme = await createInsightTheme(payload)
    await loadThemes()
    selectedId.value = theme.id
    await loadReports(theme.id)
    return theme
  }

  async function updateTheme(id: string, payload: CreateThemePayload) {
    const theme = await updateInsightTheme(id, payload)
    await loadThemes()
    return theme
  }

  async function removeTheme(id: string) {
    await deleteInsightTheme(id)
    if (selectedId.value === id) selectedId.value = ''
    await loadThemes()
  }

  async function beginRun(payload: StartRunPayload) {
    const created = await startInsightRun(selectedId.value, payload)
    run.value = created
    evidence.value = []
    evidenceTotal.value = 0
    evidenceExcluded.value = 0
    return created
  }

  async function refreshRun(runId?: string) {
    const id = runId ?? run.value?.id
    if (!id) return null
    run.value = await getInsightRun(id)
    return run.value
  }

  /** 接管一次既有运行：关页或换页后重新回到过程页时使用。 */
  async function resumeRun(runId: string) {
    run.value = await getInsightRun(runId)
    return run.value
  }

  async function cancelRun() {
    if (!run.value) return
    await cancelInsightRun(run.value.id)
    await refreshRun()
  }

  async function loadEvidence() {
    if (!run.value) return
    const data = await listInsightEvidence(run.value.id)
    evidence.value = data.items ?? []
    evidenceTotal.value = data.total ?? 0
    evidenceExcluded.value = data.excluded ?? 0
  }

  async function setEvidenceExcluded(ids: string[], excluded: boolean) {
    if (!run.value) return
    await excludeInsightEvidence(run.value.id, ids, excluded)
    const target = new Set(ids)
    evidence.value = evidence.value.map((item) =>
      target.has(item.id) ? { ...item, excluded } : item,
    )
    evidenceExcluded.value = evidence.value.filter((item) => item.excluded).length
  }

  /** 确认证据并进入阶段二；必须已有运行上下文，缺上下文时报错而不是静默无反应。 */
  async function confirmEvidence(runId?: string) {
    const id = runId ?? run.value?.id
    if (!id) throw new Error('没有进行中的运行，请从运行记录重新进入')
    await confirmInsightRun(id)
    await refreshRun(id)
  }

  async function openReport(reportId: string, themeName: string) {
    report.value = await getInsightReport(reportId)
    if (report.value && !report.value.theme_name) report.value.theme_name = themeName
    await loadReportMessages(reportId)
    return report.value
  }

  async function loadReportMessages(reportId: string) {
    try {
      reportMessages.value = await listInsightReportMessages(reportId)
    } catch {
      // 会话记录失败不影响报告正文
      reportMessages.value = []
    }
  }

  /** 追问：服务端上下文限定为这份报告 + 它的证据集。 */
  async function askReport(reportId: string, question: string) {
    const answer = await askInsightReport(reportId, question)
    await loadReportMessages(reportId)
    return answer
  }

  async function removeReport(reportId: string) {
    await deleteInsightReport(reportId)
    await loadReports()
    await loadThemes()
  }

  async function loadSelf() {
    self.value = await getInsightSelf()
    return self.value
  }

  /** 拉取脱敏设置；失败时保持空词表，导出仍可用（只是不替换）。 */
  async function loadPrivacy() {
    try {
      privacy.value = normalizePrivacy(await getInsightPrivacy())
    } catch {
      privacy.value = normalizePrivacy(undefined)
    }
  }

  async function savePrivacy(next: InsightPrivacy) {
    privacy.value = normalizePrivacy(await updateInsightPrivacy(next))
    return privacy.value
  }

  async function loadSelfMessages() {
    try {
      selfMessages.value = await listInsightSelfMessages()
    } catch {
      // 会话记录失败不影响说明书正文
      selfMessages.value = []
    }
  }

  /** 自我洞悉追问：服务端上下文限定为当前说明书 + 主题报告摘要。 */
  async function askSelf(question: string) {
    const answer = await askInsightSelf(question)
    await loadSelfMessages()
    return answer
  }

  /** 重新生成会清空旧会话，本地同步清空，避免看到基于旧结论的问答。 */
  async function regenerateSelf() {
    await generateInsightSelf()
    selfMessages.value = []
    await loadSelf()
  }

  return {
    themes,
    docCount,
    selectedId,
    selected,
    builtinThemes,
    customThemes,
    reportCount,
    reports,
    checkup,
    runs,
    run,
    evidence,
    evidenceTotal,
    evidenceExcluded,
    validEvidence,
    report,
    reportMessages,
    self,
    selfMessages,
    privacy,
    loading,
    loadThemes,
    selectTheme,
    loadReports,
    loadCheckup,
    loadRuns,
    removeRun,
    importTheme,
    createTheme,
    updateTheme,
    removeTheme,
    beginRun,
    refreshRun,
    resumeRun,
    cancelRun,
    loadEvidence,
    setEvidenceExcluded,
    confirmEvidence,
    openReport,
    loadReportMessages,
    askReport,
    removeReport,
    loadSelf,
    loadPrivacy,
    savePrivacy,
    loadSelfMessages,
    askSelf,
    regenerateSelf,
  }
})
