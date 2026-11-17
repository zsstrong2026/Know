<template>
  <!-- 洞察：主题洞察 + 自我洞悉。视觉与 docs/design/insight-module-prototype.html 对齐 -->
  <div class="ins">
    <!-- ===== 顶部 ===== -->
    <header v-if="screen === 'home'" class="ins-topbar">
      <div class="ins-topbar-main">
        <div class="ins-title-row">
          <h1>洞察 <span class="ins-beta">Beta</span></h1>
          <div class="ins-mode">
            <button :class="{ on: mode === 'topics' }" @click="setMode('topics')">主题洞察</button>
            <button :class="{ on: mode === 'self' }" @click="setMode('self')">
              <t-icon name="chart-analytics" size="13px" />自我洞悉<span class="ins-dot" />
            </button>
          </div>
        </div>
        <p class="ins-sub">
          {{
            mode === 'topics'
              ? '从归档文档里找出你的思考方式、价值排序与反复出现的矛盾'
              : '把各主题报告与归档文档合成一份「自我说明书」：不只说你是什么样，还解释你为什么这样、在什么环境下会变形'
          }}
        </p>
      </div>
      <div class="ins-topbar-actions">
        <template v-if="mode === 'topics'">
          <button class="ins-btn ghost" @click="openMasking">
            <t-icon name="lock-on" size="13px" />脱敏设置
          </button>
          <button class="ins-btn ghost" @click="openRuns">
            <t-icon name="history" size="13px" />运行记录
          </button>
          <button class="ins-btn primary" @click="openNewTheme">
            <t-icon name="add" size="13px" />新建主题
          </button>
        </template>
        <template v-else>
          <span class="ins-gate" :class="{ warn: !selfGateOk }">
            <t-icon :name="selfGateOk ? 'check' : 'error-circle'" size="13px" />
            生成条件 {{ self?.gate.completed ?? 0 }} / {{ self?.gate.required ?? 6 }} 已完成
          </span>
          <button class="ins-btn" @click="exportSelfMarkdown">
            <t-icon name="download" size="13px" />导出
          </button>
          <button class="ins-btn" :disabled="savingDoc || !selfReady" @click="saveSelfAsDoc">
            <t-icon name="file-add" size="13px" />{{ savingDoc ? '保存中…' : '存为文档' }}
          </button>
          <button class="ins-btn primary" :disabled="!selfGateOk || self?.status === 'generating'" @click="regenerateSelf">
            <t-icon name="chart-analytics" size="13px" />{{ self?.status === 'generating' ? '生成中…' : '重新生成' }}
          </button>
        </template>
      </div>
    </header>

    <header v-else class="ins-topbar" :class="{ compact: screen === 'report' }">
      <div class="ins-topbar-main">
        <div class="ins-title-row">
          <button class="ins-icon-btn" @click="backHome"><t-icon name="chevron-left" size="16px" /></button>
          <h1 v-if="screen !== 'report'">{{ topbarTitle }}</h1>
          <div v-else class="crumb">洞察 / <b>{{ reportThemeName }}</b> / 报告</div>
        </div>
        <p v-if="screen !== 'report'" class="ins-sub">{{ topbarSub }}</p>
      </div>
      <div class="ins-topbar-actions">
        <template v-if="screen === 'run'">
          <button class="ins-btn ghost danger" @click="stopRun">终止运行</button>
        </template>
        <template v-else-if="screen === 'evidence'">
          <span class="ins-tag outline"><t-icon name="lock-on" size="11px" />仅你可见</span>
        </template>
        <template v-else-if="screen === 'report'">
          <button class="ins-btn small" :disabled="savingDoc" @click="saveReportAsDoc">
            <t-icon name="file-add" size="12px" />{{ savingDoc ? '保存中…' : '存为文档' }}
          </button>
          <button class="ins-btn small" @click="exportReportMarkdown">
            <t-icon name="download" size="12px" />导出 .md
          </button>
          <button class="ins-btn small" @click="printReportMarkdown">
            <t-icon name="print" size="12px" />打印 / PDF
          </button>
          <button class="ins-btn small" @click="copyReportMarkdown">
            <t-icon name="file" size="12px" />复制 Markdown
          </button>
        </template>
      </div>
    </header>

    <!-- ===== 主题洞察首页 ===== -->
    <div v-if="mode === 'topics' && screen === 'home'" class="home-grid">
      <aside class="theme-pane">
        <div class="pane-head"><b>主题</b><span>{{ store.themes.length }} 个</span></div>
        <div class="search">
          <t-icon name="search" size="13px" />
          <input v-model="keyword" placeholder="搜索主题" />
        </div>
        <div class="theme-scroll">
          <div class="list-label"><span>内置主题</span><span>{{ builtinThemes.length }}</span></div>
          <button
            v-for="t in builtinThemes"
            :key="t.id"
            class="theme-item"
            :class="{ 'is-active': t.id === store.selectedId }"
            :title="t.description"
            @click="store.selectTheme(t.id)"
          >
            <span class="theme-glyph"><t-icon :name="glyphIcon(t.glyph)" size="13px" /></span>
            <span class="theme-copy">
              <span class="theme-name">
                {{ t.name }}
                <span v-if="t.sensitive" class="ins-tag amber tiny">敏感</span>
              </span>
              <span class="theme-desc">{{ t.description }}</span>
              <span class="theme-meta">
                <span class="dot" :class="{ run: t.running }" />
                {{ themeMeta(t) }}
              </span>
            </span>
          </button>
          <div class="list-label"><span>我的主题</span><span>{{ customThemes.length }}</span></div>
          <button
            v-for="t in customThemes"
            :key="t.id"
            class="theme-item"
            :class="{ 'is-active': t.id === store.selectedId }"
            :title="t.description"
            @click="store.selectTheme(t.id)"
          >
            <span class="theme-glyph"><t-icon :name="glyphIcon(t.glyph)" size="13px" /></span>
            <span class="theme-copy">
              <span class="theme-name">{{ t.name }}</span>
              <span class="theme-desc">{{ t.description }}</span>
              <span class="theme-meta">
                <span class="dot" :class="{ run: t.running }" />
                {{ themeMeta(t) }}
              </span>
            </span>
          </button>
          <div v-if="!store.themes.length" class="empty-box">
            <b>还没有洞察主题</b>点击右上角「新建主题」，或从内置模板开始。
          </div>
        </div>
      </aside>

      <div class="detail-pane">
        <div v-if="selected" class="detail-inner">
          <div class="detail-head">
            <div>
              <div class="detail-title">
                <h2>{{ selected.name }}</h2>
                <span class="ins-tag" :class="selected.builtin ? 'green' : 'outline'">
                  {{ selected.builtin ? '内置模板' : '自定义方法包' }}
                </span>
                <span v-if="selected.sensitive" class="ins-tag amber">敏感</span>
              </div>
              <p class="detail-desc">{{ selected.description }}</p>
            </div>
            <div class="detail-actions">
              <button class="ins-btn ghost" @click="openEditTheme">
                <t-icon name="edit" size="12px" />编辑方法
              </button>
              <button class="ins-btn primary" @click="startFlow">
                {{ selected.report_count ? '再次分析' : '开始分析' }}
              </button>
            </div>
          </div>

          <div class="meta-strip">
            <div><span>分析维度</span><b>{{ selected.dimensions.length }} 个</b></div>
            <div><span>模型</span><b>默认模型</b></div>
            <div><span>历史报告</span><b>{{ selected.report_count }} 份</b></div>
            <div><span>上次运行</span><b>{{ relativeTime(selected.last_run_at) }}</b></div>
          </div>

          <div class="scope-card">
            <t-icon name="book" size="16px" />
            <div class="scope-main">
              <b>工作知识库 · {{ scopeLabel }}</b>
              <p>{{ store.docCount }} 篇 · 范围只影响本次分析，体检与运行使用同一口径</p>
            </div>
            <button class="ins-btn small" @click="openScope">调整范围</button>
          </div>

          <div v-if="selected.updated_docs" class="stale-bar">
            <t-icon name="history" size="14px" />
            <span>
              上次报告后有 <b>{{ selected.updated_docs }}</b> 篇文档更新，新内容可能没进入上一份报告。
            </span>
            <button class="ins-btn small" @click="startFlow">纳入新内容</button>
          </div>

          <div class="section-title">维度与判断口径<span class="right">主题只声明口径，报告结构统一</span></div>
          <div class="dim-table">
            <div class="dim-row head"><span>维度</span><span>判断标准</span><span>评分</span></div>
            <div v-for="d in selected.dimensions" :key="d.name" class="dim-row">
              <b>{{ d.name }}</b><span>{{ d.criteria }}</span>
              <span class="ins-tag outline fit">{{ d.scale }}</span>
            </div>
          </div>

          <div class="section-title">
            历史报告 <span class="count">{{ store.reports.length }}</span>
            <span class="right">报告为不可变快照</span>
          </div>
          <div v-if="store.reports.length">
            <div v-for="r in store.reports" :key="r.id" class="report-row" @click="openReport(r.id)">
              <span class="date">{{ shortDate(r.created_at) }}</span>
              <span class="name">{{ r.title || `${selected.name}洞察报告` }}</span>
              <span class="facts">覆盖 {{ r.docs_covered }} 篇 · {{ r.evidence_count }} 条证据</span>
              <t-icon name="more" size="12px" style="color: #c5c5c5" />
            </div>
          </div>
          <div v-else class="empty-box">
            <b>还没有运行过这个主题</b>建议先跑一次小样（最近 20 篇），确认分析口径后再全量。
          </div>

          <div class="privacy-note">
            <t-icon name="lock-on" size="13px" />
            <span>洞察仅你本人可见：报告、证据集与运行记录管理员不可读，也不进入团队统计。删除运行会同时删除它的证据集。</span>
          </div>
        </div>
        <div v-else class="detail-inner empty-box">
          <b>选择一个主题</b>左侧内置了思维模式、价值观、思维陷阱、价值澄清、MBTI 与主要矛盾。
        </div>
      </div>
    </div>

    <!-- ===== 运行中 ===== -->
    <div v-else-if="screen === 'run'" class="run-body">
      <div class="run-main">
        <div class="stepper">
          <div class="step" :class="{ done: stepIndex >= 2 }">
            <span class="num"><t-icon v-if="stepIndex > 1" name="check" size="11px" /><template v-else>1</template></span>素材体检
          </div>
          <div class="step-line" :class="{ done: stepIndex > 1 }" />
          <div class="step" :class="{ done: stepIndex >= 3, active: stepIndex === 2 }">
            <span class="num"><t-icon v-if="stepIndex > 2" name="check" size="11px" /><template v-else>2</template></span>证据抽取
          </div>
          <div class="step-line" :class="{ done: stepIndex > 2 }" />
          <div class="step" :class="{ done: stepIndex >= 4, active: stepIndex === 3 }">
            <span class="num"><t-icon v-if="stepIndex > 3" name="check" size="11px" /><template v-else>3</template></span>证据确认
          </div>
          <div class="step-line" :class="{ done: stepIndex >= 4 }" />
          <div class="step" :class="{ active: stepIndex === 4 }">
            <span class="num">4</span>生成报告
          </div>
        </div>

        <div class="run-card">
          <div class="run-card-head">
            <b>{{ runStageLabel }}</b>
            <span>{{ run?.progress ?? 0 }}%</span>
          </div>
          <div class="bar"><i :style="{ width: `${run?.progress ?? 0}%` }" /></div>
          <div class="run-stats">
            <span>已处理 <b>{{ run?.docs_processed ?? 0 }}</b>/{{ run?.docs_total ?? 0 }} 篇</span>
            <span>证据 <b>{{ run?.evidence_count ?? 0 }}</b> 条</span>
            <span>{{ run?.status === 'failed' ? '已失败' : run?.status === 'cancelled' ? '已终止' : '后台任务执行中' }}</span>
          </div>
          <div class="log">
            <div><span class="t">状态</span>{{ run?.message || '正在准备…' }}</div>
            <div v-if="run?.error" style="color: var(--td-error-color)"><span class="t">错误</span>{{ run.error }}</div>
          </div>
        </div>
      </div>
      <div class="run-aside">
        <div class="tip-card">
          <b>为什么先跑阶段一</b>
          逐篇抽取只做「找证据」，不做判断。跑完你会先看到证据集，可以剔除噪声文档或误判条目，再进入归并生成。
        </div>
        <div class="tip-card">
          <b>只读一次的素材</b>
          本次范围：工作知识库{{ scopeLabel }} · {{ checkup?.analyzable ?? run?.docs_total ?? 0 }} 篇可分析；已排除
          {{ excludedDocs }} 篇（空 {{ checkup?.empty ?? 0 }} / 过短 {{ checkup?.too_short ?? 0 }} / 纯附件
          {{ checkup?.attachment ?? 0 }} / 重复 {{ checkup?.duplicate ?? 0 }}）。
        </div>
      </div>
    </div>

    <!-- ===== 证据集 ===== -->
    <div v-else-if="screen === 'evidence'" class="evidence-page">
      <div class="evidence-toolbar">
        <button class="chip" :class="{ on: evidenceDim === '' }" @click="evidenceDim = ''">
          全部 <span class="n">{{ store.evidence.length }}</span>
        </button>
        <button
          v-for="d in evidenceDims"
          :key="d"
          class="chip"
          :class="{ on: evidenceDim === d }"
          @click="evidenceDim = d"
        >
          {{ d }}
          <span class="n">{{ evidenceDimCounts[d] ?? 0 }}</span>
        </button>
        <button class="chip" :class="{ on: onlyInference }" @click="onlyInference = !onlyInference">
          仅看推断 <span class="n">{{ inferenceCount }}</span>
        </button>
        <div style="flex: 1" />
        <div class="search table-search">
          <t-icon name="search" size="13px" />
          <input v-model="evidenceKeyword" placeholder="搜索证据或文档" />
        </div>
      </div>
      <div class="evidence-wrap">
        <table class="ev">
          <thead>
            <tr>
              <th style="width: 34px" />
              <th style="width: 42%">证据摘录</th>
              <th style="width: 96px">维度</th>
              <th style="width: 30%">来源</th>
              <th style="width: 84px">类型</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="item in filteredEvidence" :key="item.id" :class="{ 'is-excluded': item.excluded }">
              <td>
                <span class="chk" :class="{ on: item.excluded }" @click="toggleEvidence(item)">
                  <t-icon v-if="item.excluded" name="check" size="9px" />
                </span>
              </td>
              <td><div class="q">{{ item.quote }}</div></td>
              <td><span class="ins-tag outline">{{ item.dimension }}</span></td>
              <td>
                <div class="src">
                  <b>《{{ item.knowledge_title }}》 · 第 {{ item.chunk_index }} 段</b><br />
                  {{ shortDate(item.created_at) }}
                </div>
                <button class="link-btn" @click="excludeByDocument(item.knowledge_id)">剔除该文档全部证据</button>
              </td>
              <td>
                <span class="ins-tag" :class="item.evidence_type === 'inference' ? 'amber' : 'green'">
                  {{ item.evidence_type === 'inference' ? '推断' : '有证据' }}
                </span>
              </td>
            </tr>
          </tbody>
        </table>
        <div v-if="!filteredEvidence.length" class="empty-box" style="margin: 24px">
          <b>没有匹配的证据</b>调整筛选条件，或返回上一阶段。
        </div>
      </div>
      <div class="evidence-foot">
        <span class="stat">有效 <b>{{ validEvidence }}</b> 条</span>
        <span class="stat">已排除 <b>{{ store.evidenceExcluded }}</b> 条</span>
        <span class="foot-hint">证据只可剔除、不可编辑 —— 保证结论始终可回溯到原文</span>
        <div style="flex: 1" />
        <button class="ins-btn primary" :disabled="validEvidence === 0 || confirming" @click="confirmEvidence">
          {{ confirming ? '生成中…' : '确认证据集，生成报告' }}
        </button>
      </div>
    </div>

    <!-- ===== 报告 ===== -->
    <div v-else-if="screen === 'report'" class="report-layout">
      <div class="report-scroll">
        <article v-if="report" class="report">
          <div class="report-cover">
            <div class="snapshot">
              <t-icon name="lock-on" size="10px" />不可变快照<template v-if="reportOrdinal"> · 第 {{ reportOrdinal }} 次运行</template>
            </div>
            <h1>{{ report.title || `${report.theme_name}洞察报告` }}</h1>
            <div class="report-meta">
              <span>{{ formatTime(report.created_at) }}</span><span>·</span>
              <span>{{ report.model_id || '默认模型' }}</span><span>·</span>
              <span>覆盖 {{ report.docs_covered }} 篇</span><span>·</span>
              <span>{{ report.evidence_count }} 条证据</span><span>·</span>
              <span>仅你可见</span>
              <template v-if="maskConfigured"><span>·</span><span>导出脱敏已开启</span></template>
            </div>
            <div class="disclaimer">
              本报告是基于你归档文档的倾向性推断，不是心理测量结果，也不构成任何专业或医疗建议。你可以删除本次运行及其证据。
            </div>
          </div>

          <section id="ins-r1" class="r-sec">
            <div class="r-sec-head"><span class="idx">01</span><h2>素材覆盖度</h2></div>
            <p class="cov-note">{{ report.content.coverage_note || '本次分析覆盖了你工作知识库中的归档文档。' }}</p>
          </section>

          <section id="ins-r2" class="r-sec">
            <div class="r-sec-head"><span class="idx">02</span><h2>核心发现</h2><span class="aside">{{ report.content.findings.length }} 条</span></div>
            <div
              v-for="(f, i) in report.content.findings"
              :key="i"
              class="finding"
              :class="{ inferred: f.kind === 'inference' }"
            >
              <div class="finding-head">
                <span class="ins-tag" :class="f.kind === 'inference' ? 'amber' : 'green'">
                  {{ f.kind === 'inference' ? '推断' : '有证据' }}
                </span>
                <span class="ins-tag outline">置信度 {{ f.confidence || '中' }}</span>
                <h3>{{ f.title }}</h3>
              </div>
              <p>{{ f.detail }}</p>
              <div v-if="f.refs?.length" class="refs">
                <span v-for="(r, ri) in f.refs" :key="ri" class="ref">{{ r }}</span>
              </div>
            </div>
          </section>

          <section id="ins-r3" class="r-sec">
            <div class="r-sec-head"><span class="idx">03</span><h2>证据链</h2><span class="aside">{{ report.content.evidence_groups.length }} 条代表证据</span></div>
            <blockquote v-for="(q, i) in report.content.evidence_groups" :key="i">
              {{ q.quote }}
              <span class="quote-src">{{ q.source }}</span>
            </blockquote>
          </section>

          <section id="ins-r4" class="r-sec">
            <div class="r-sec-head"><span class="idx">04</span><h2>反证与不确定性</h2><span class="aside">已主动检索反例</span></div>
            <div v-for="(c, i) in report.content.counterpoints" :key="i" class="bullet"><i /><span>{{ c }}</span></div>
          </section>

          <section id="ins-r5" class="r-sec">
            <div class="r-sec-head"><span class="idx">05</span><h2>维度评分</h2><span class="aside">倾向参考，非量表</span></div>
            <div v-for="(s, i) in report.content.scores" :key="i" class="score-row">
              <span>{{ s.name }}</span>
              <div class="score-track"><i :style="{ width: `${clamp(s.value)}%` }" /></div>
              <span class="score-val">{{ s.label }} {{ clamp(s.value) }}</span>
            </div>
            <p class="scale-note">分数只表示「在该维度上证据更偏向哪一端」，不表示能力高低；同一个人在不同领域可以有不同倾向。</p>
          </section>

          <section id="ins-r6" class="r-sec">
            <div class="r-sec-head"><span class="idx">06</span><h2>结论</h2><span class="aside">文字总结</span></div>
            <div class="conclusion">
              <p v-for="(para, i) in conclusionParagraphs" :key="i" :class="{ lead: i === 0 }">{{ para }}</p>
              <div v-if="report.content.conclusion_basis" class="basis">{{ report.content.conclusion_basis }}</div>
            </div>
          </section>

          <section id="ins-r7" class="r-sec">
            <div class="r-sec-head"><span class="idx">07</span><h2>建议与下一步</h2></div>
            <div v-for="(a, i) in report.content.actions" :key="i" class="action">
              <span class="n">{{ i + 1 }}</span><span>{{ a }}</span>
            </div>
          </section>
        </article>
      </div>
      <aside class="ask-rail">
        <div class="ask-head">
          <b><t-icon name="chart-analytics" size="13px" />就这份报告追问</b>
          <p>只检索本次报告与它的 {{ report?.evidence_count ?? 0 }} 条证据，不会重新扫描全部文档。</p>
        </div>
        <div class="toc">
          <a href="#ins-r1">01 素材覆盖度</a>
          <a href="#ins-r2">02 核心发现</a>
          <a href="#ins-r3">03 证据链</a>
          <a href="#ins-r4">04 反证与不确定性</a>
          <a href="#ins-r5">05 维度评分</a>
          <a href="#ins-r6">06 结论</a>
          <a href="#ins-r7">07 建议与下一步</a>
        </div>
        <div class="chat">
          <div
            v-for="m in store.reportMessages"
            :key="m.id"
            class="msg"
            :class="m.role === 'user' ? 'user' : 'ai'"
          >
            <template v-if="m.role === 'assistant'">
              <div class="ai-name">洞察助手</div>
              <div class="ai-body">{{ m.content }}</div>
            </template>
            <template v-else>{{ m.content }}</template>
          </div>
          <div v-if="asking" class="msg ai">
            <div class="ai-name">洞察助手</div>
            <div class="ai-body">正在基于本报告与证据作答…</div>
          </div>
          <div v-else-if="!store.reportMessages.length" class="msg ai">
            <div class="ai-name">洞察助手</div>
            <div class="ai-body">
              可以问：某条发现的证据够不够、某个维度为什么这样评分、报告里的反例是否成立。回答只使用这份报告和它的证据集。
            </div>
          </div>
        </div>
        <div class="ask-input">
          <div class="ask-box">
            <textarea
              v-model="reportAsk"
              placeholder="比如：这条结论的证据够吗？"
              @keydown.enter.exact.prevent="sendReportAsk"
            />
            <button class="send" :disabled="asking" @click="sendReportAsk">
              <t-icon name="arrow-right" size="12px" />
            </button>
          </div>
        </div>
      </aside>
    </div>

    <!-- ===== 自我洞悉 ===== -->
    <div v-else class="report-layout">
      <div class="report-scroll">
        <div v-if="self?.content" class="self-wrap">
          <div class="self-hero">
            <div class="badges">
              <span class="ins-tag green">更新于 {{ formatTime(self.updated_at) }}</span>
              <span class="ins-tag outline">综合 {{ selfStats.themes }} 个内置主题 · {{ selfStats.reports }} 份报告 · {{ store.docCount }} 篇文档</span>
              <span class="ins-tag amber">{{ selfStats.evidenced }} / {{ selfStats.total }} 维度有证据</span>
            </div>
            <p class="portrait" v-html="safePortrait" />
            <div class="foot">
              生成前提：6 个内置主题都已至少完成一次分析。本页随主题报告与归档文档覆盖更新，不保留历史版本；想留档请另存为墨匣文档。第 5–7 层证据通常偏少，空白维度不会被编造。
            </div>
            <div class="layer-nav">
              <a
                v-for="layer in self.content.layers"
                :key="layer.no"
                class="layer-chip"
                :href="`#ins-layer-${layer.no}`"
              >
                <span class="n">L{{ layer.no }}</span>
                <b>{{ layer.name.replace('层', '') }}</b>
                <span class="cov"><i :style="{ width: layerCoverage(layer.coverage) }" /></span>
              </a>
            </div>
          </div>

          <section class="layer-sec">
            <div class="layer-head">
              <span class="layer-no">◎</span>
              <div><h2>关于我的速览</h2><div class="q">先看结论，再往下看每一层的证据与来源</div></div>
            </div>
            <div class="overview-grid">
              <div v-for="(o, i) in self.content.overview" :key="i" class="ov-card">
                <div class="h">
                  <t-icon :name="overviewIcon(o.key)" size="13px" />{{ o.title }}
                  <a v-if="o.link" :href="overviewHref(o.link)">{{ overviewLinkText(o) }}</a>
                </div>
                <p>{{ o.text }}</p>
              </div>
            </div>
          </section>

          <section
            v-for="layer in self.content.layers"
            :id="`ins-layer-${layer.no}`"
            :key="layer.no"
            class="layer-sec"
          >
            <div class="layer-head">
              <span class="layer-no">{{ layer.no }}</span>
              <div><h2>{{ layerTitle(layer) }}</h2><div class="q">核心提问：{{ layer.question }}</div></div>
              <span class="ins-tag cov-tag" :class="coverageTone(layer.coverage)">{{ layer.coverage }}</span>
            </div>
            <p class="layer-lead">{{ layer.lead }}</p>
            <div
              v-for="(d, di) in layer.dims"
              :key="di"
              class="ldim"
              :class="{ 'is-blank': d.status === 'blank' }"
            >
              <div class="ldim-head">
                <b>{{ d.name }}</b>
                <span class="ins-tag" :class="statusTone(d.status)">{{ statusLabel(d.status) }}</span>
                <span v-if="d.source" class="src">{{ d.source }}</span>
              </div>
              <p>{{ d.text }}</p>
            </div>
          </section>

          <section class="layer-sec">
            <div class="layer-head">
              <span class="layer-no">+</span>
              <div><h2>四类贯穿性视角</h2><div class="q">不属于任何单层，但决定这套说明书的可信度</div></div>
            </div>
            <div class="cross-grid">
              <div v-for="(c, i) in self.content.cross" :key="i" class="cross">
                <div class="t">{{ c.title }}</div>
                <p>{{ c.text }}</p>
              </div>
            </div>
          </section>

          <section id="ins-trio" class="layer-sec">
            <div class="layer-head">
              <span class="layer-no">×</span>
              <div><h2>用三元组替代优缺点</h2><div class="q">同一个特质在不同情境下既是资产也是负债</div></div>
            </div>
            <table class="trio">
              <thead><tr><th style="width: 22%">特质</th><th style="width: 40%">适用情境</th><th>代价 / 失效条件</th></tr></thead>
              <tbody>
                <tr v-for="(t, i) in self.content.trios" :key="i">
                  <td><b>{{ t.trait }}</b></td><td>{{ t.situation }}</td><td>{{ t.cost }}</td>
                </tr>
              </tbody>
            </table>
          </section>

          <section class="layer-sec">
            <div class="layer-head">
              <span class="layer-no">→</span>
              <div><h2>空白区与下一步</h2><div class="q">从 24 个维度里先补影响最大的几项</div></div>
            </div>
            <div v-for="(n, i) in self.content.next" :key="i" class="action">
              <span class="n">{{ i + 1 }}</span><span><b>{{ n.title }}</b>：{{ n.text }}</span>
            </div>
          </section>
        </div>

        <div v-else class="self-wrap">
          <div class="self-hero">
            <div class="badges">
              <span class="ins-tag amber">尚未生成</span>
            </div>
            <p class="portrait">
              自我洞悉会综合全部内置主题的最新报告与工作知识库文档，按七层结构输出一份自我说明书。
            </p>
            <div class="foot">生成前提：6 个内置主题都已至少完成一次分析。</div>
            <div class="gate-list">
              <div v-for="t in store.builtinThemes" :key="t.id" class="gate-row">
                <t-icon :name="t.report_count ? 'check' : 'error-circle'" size="13px" :style="{ color: t.report_count ? 'var(--td-brand-color)' : 'var(--td-warning-color)' }" />
                <span>{{ t.name }}</span>
                <em>{{ t.report_count ? `${t.report_count} 份报告` : '未运行' }}</em>
              </div>
            </div>
            <div style="margin-top: 16px">
              <button class="ins-btn primary" :disabled="!selfGateOk || self?.status === 'generating'" @click="regenerateSelf">
                {{ self?.status === 'generating' ? '生成中…' : '生成自我洞悉' }}
              </button>
            </div>
          </div>
        </div>
      </div>
      <aside class="ask-rail">
        <div class="ask-head">
          <b><t-icon name="chart-analytics" size="13px" />就这份说明书追问</b>
          <p>上下文限定为本次自我洞悉 + 它引用的主题报告与证据集。</p>
        </div>
        <div class="toc">
          <a v-for="layer in self?.content?.layers ?? []" :key="layer.no" :href="`#ins-layer-${layer.no}`">
            L{{ layer.no }} {{ layerTitle(layer) }}
          </a>
        </div>
        <div class="src-box-wrap">
          <div class="src-box">
            <b>这次综合了什么</b>
            <div v-for="t in store.builtinThemes" :key="t.id" class="src-row">
              <span>{{ t.name }}</span><em>{{ t.report_count }} 份报告</em>
            </div>
            <div class="src-row" style="border-top: 1px solid var(--td-component-stroke); margin-top: 5px; padding-top: 5px">
              <span>工作知识库</span><em>{{ store.docCount }} 篇文档</em>
            </div>
            <div class="src-row gate">
              <span>生成条件</span><em>{{ self?.gate.completed ?? 0 }} / {{ self?.gate.required ?? 6 }} 已满足</em>
            </div>
          </div>
        </div>
        <div class="chat">
          <div
            v-for="m in store.selfMessages"
            :key="m.id"
            class="msg"
            :class="m.role === 'user' ? 'user' : 'ai'"
          >
            <template v-if="m.role === 'assistant'">
              <div class="ai-name">洞察助手</div>
              <div class="ai-body">{{ m.content }}</div>
            </template>
            <template v-else>{{ m.content }}</template>
          </div>
          <div v-if="selfAsking" class="msg ai">
            <div class="ai-name">洞察助手</div>
            <div class="ai-body">正在基于这份说明书作答…</div>
          </div>
          <div v-else-if="!store.selfMessages.length" class="msg ai">
            <div class="ai-name">洞察助手</div>
            <div class="ai-body">
              可以问：某个维度为什么这样判断、两层的结论是否冲突、想补哪一块证据。回答只使用这份说明书与各主题报告的结论摘要。
            </div>
          </div>
        </div>
        <div class="ask-input">
          <div class="ask-box">
            <textarea
              v-model="selfAsk"
              :disabled="!selfReady"
              :placeholder="selfReady ? '比如：我为什么在压力下更容易回避冲突？' : '生成说明书后可追问'"
              @keydown.enter.exact.prevent="sendSelfAsk"
            />
            <button class="send" :disabled="selfAsking || !selfReady" @click="sendSelfAsk">
              <t-icon name="arrow-right" size="12px" />
            </button>
          </div>
        </div>
      </aside>
    </div>

    <!-- ===== 分析范围 ===== -->
    <div v-if="dialogs.scope" class="mask" @click.self="dialogs.scope = false">
      <div class="modal">
        <div class="modal-head">
          <div>
            <h3>调整分析范围</h3>
            <p>范围只影响本次分析；素材体检与运行使用同一口径。</p>
          </div>
          <button class="ins-icon-btn" @click="dialogs.scope = false"><t-icon name="close" size="15px" /></button>
        </div>
        <div class="modal-body">
          <div class="rule-list">
            <div class="rule clickable" @click="setScopeDays(0)">
              <div class="rule-main"><b>全部归档</b><span>分析工作知识库中的全部可分析文档</span></div>
              <span class="radio" :class="{ on: scope.recent_days === 0 }" />
            </div>
            <div class="rule clickable" @click="setScopeDays(90)">
              <div class="rule-main"><b>最近 90 天</b><span>只看近三个月有更新的文档</span></div>
              <span class="radio" :class="{ on: scope.recent_days === 90 }" />
            </div>
            <div class="rule clickable" @click="setScopeDays(180)">
              <div class="rule-main"><b>最近 180 天</b><span>只看近半年有更新的文档</span></div>
              <span class="radio" :class="{ on: scope.recent_days === 180 }" />
            </div>
          </div>
          <div class="field">
            <label>限定文件夹（可选）</label>
            <select v-model="scope.folder_path">
              <option value="">不限定文件夹</option>
              <option v-for="folder in store.checkup?.folders ?? []" :key="folder" :value="folder">
                {{ folder }}
              </option>
            </select>
          </div>
          <div class="hint-box">
            <t-icon name="info-circle" size="13px" />
            <span>当前范围：{{ scopeLabel }}。开始分析前还会展示素材体检，可再确认可分析文档数。</span>
          </div>
        </div>
        <div class="modal-foot">
          <button class="ins-btn ghost" @click="dialogs.scope = false">取消</button>
          <div style="flex: 1" />
          <button class="ins-btn primary" @click="dialogs.scope = false">完成</button>
        </div>
      </div>
    </div>

    <!-- ===== 运行记录 ===== -->
    <div v-if="dialogs.runs" class="mask" @click.self="dialogs.runs = false">
      <div class="modal wide">
        <div class="modal-head">
          <div>
            <h3>运行记录 · {{ selected?.name }}</h3>
            <p>删除运行会同时删除它的证据集；已经生成的报告仍可查看。</p>
          </div>
          <button class="ins-icon-btn" @click="dialogs.runs = false"><t-icon name="close" size="15px" /></button>
        </div>
        <div class="modal-body" style="padding-top: 14px">
          <div v-if="!store.runs.length" class="empty-box">
            <b>还没有运行记录</b>回到主题详情点击「开始分析」。
          </div>
          <div v-else class="run-list">
            <div v-for="item in store.runs" :key="item.id" class="run-row">
              <span class="ins-tag" :class="runStatusTone(item.status)">{{ runStatusLabel(item.status) }}</span>
              <div class="run-copy">
                <b>{{ formatTime(item.created_at) }}</b>
                <span>{{ item.docs_processed }}/{{ item.docs_total }} 篇 · {{ item.evidence_count }} 条证据 · {{ item.message || '—' }}</span>
              </div>
              <button v-if="reportForRun(item.id)" class="ins-btn small" @click="openReportFromRun(item.id)">
                查看报告
              </button>
              <button class="ins-btn small danger" :disabled="isRunningStatus(item.status)" @click="confirmDeleteRun(item.id)">
                删除
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- ===== 素材体检 ===== -->
    <div v-if="dialogs.checkup" class="mask" @click.self="dialogs.checkup = false">
      <div class="modal">
        <div class="modal-head">
          <div>
            <h3>分析前确认</h3>
            <p>{{ selected?.name }} · 工作知识库全部归档</p>
          </div>
          <button class="ins-icon-btn" @click="dialogs.checkup = false"><t-icon name="close" size="15px" /></button>
        </div>
        <div class="modal-body">
          <div class="stat-grid">
            <div class="stat-cell"><span>归档</span><b>{{ checkup?.archived ?? 0 }}</b></div>
            <div class="stat-cell"><span>可分析</span><b>{{ checkup?.analyzable ?? 0 }}</b></div>
            <div class="stat-cell muted"><span>过短</span><b>{{ checkup?.too_short ?? 0 }}</b></div>
            <div class="stat-cell muted"><span>空/附件</span><b>{{ (checkup?.empty ?? 0) + (checkup?.attachment ?? 0) }}</b></div>
            <div class="stat-cell muted"><span>重复</span><b>{{ checkup?.duplicate ?? 0 }}</b></div>
          </div>
          <div class="rule-list">
            <div class="rule">
              <div class="rule-main"><b>排除空文档与过短内容</b><span>少于 120 字的文档不参与分析</span></div>
              <div class="switch on fixed" title="固定策略，由服务端执行" />
            </div>
            <div class="rule">
              <div class="rule-main"><b>排除纯附件与纯图片文档</b><span>没有可提取正文的文件跳过</span></div>
              <div class="switch on fixed" title="固定策略，由服务端执行" />
            </div>
            <div class="rule">
              <div class="rule-main"><b>重复文档只保留最新</b><span>按标题 + 正文相似度判重</span></div>
              <div class="switch on fixed" title="固定策略，由服务端执行" />
            </div>
            <div class="rule">
              <div class="rule-main"><b>超长文档部分截取</b><span>超过 4 万字的文档取开头 / 结尾与主题相关段落，不整篇排除</span></div>
              <div class="switch on fixed" title="固定策略，由服务端执行" />
            </div>
            <div class="rule">
              <div class="rule-main"><b>先跑小样（最近 20 篇）</b><span>约 2 分钟，确认分析口径后再全量</span></div>
              <div class="switch" :class="{ on: sampleRun }" @click="sampleRun = !sampleRun" />
            </div>
          </div>
          <div class="hint-box">
            <t-icon name="lock-on" size="13px" />
            <span>本次分析仅你可见，管理员也无法查看报告与证据。服务端会跳过空文档、纯附件与重复文档；人名 / 组织名脱敏在首页「脱敏设置」里配置，作用于复制、导出与存文档。</span>
          </div>
        </div>
        <div class="modal-foot">
          <span class="hint">阶段一约 4–6 分钟，可随时终止</span>
          <div style="flex: 1" />
          <button class="ins-btn ghost" @click="dialogs.checkup = false">取消</button>
          <button class="ins-btn primary" :disabled="!checkup?.analyzable" @click="confirmCheckup">确认并开始</button>
        </div>
      </div>
    </div>

    <!-- ===== 导出脱敏设置 ===== -->
    <div v-if="dialogs.masking" class="mask" @click.self="dialogs.masking = false">
      <div class="modal wide">
        <div class="modal-head">
          <div>
            <h3>导出脱敏</h3>
            <p>只作用于复制、导出、打印与存为文档；页面正文和报告追问仍是原文。</p>
          </div>
          <button class="ins-icon-btn" @click="dialogs.masking = false">
            <t-icon name="close" size="15px" />
          </button>
        </div>
        <div class="modal-body">
          <div class="mask-grid">
            <label class="mask-field">
              <span>人名<em>导出时替换为「某人」</em></span>
              <textarea v-model="maskDraft.persons" rows="4" placeholder="每行一个，例如：张三" />
            </label>
            <label class="mask-field">
              <span>组织<em>导出时替换为「组织」</em></span>
              <textarea v-model="maskDraft.organizations" rows="4" placeholder="每行一个，例如：某某公司" />
            </label>
            <label class="mask-field">
              <span>项目 / 代号<em>导出时替换为「项目」</em></span>
              <textarea v-model="maskDraft.projects" rows="4" placeholder="每行一个，例如：星河计划" />
            </label>
          </div>
          <div class="confirm-line" @click="maskDraft.hide_evidence_quotes = !maskDraft.hide_evidence_quotes">
            <span class="box" :class="{ on: maskDraft.hide_evidence_quotes }">
              <t-icon v-if="maskDraft.hide_evidence_quotes" name="check" size="10px" />
            </span>
            <span>导出时隐藏证据原文，只保留来源文档名</span>
          </div>
          <p class="mask-hint">
            只做精确文本替换，不猜测人名；长词优先，避免「张三丰」被「张三」截断。同类多个词按输入顺序编号为「某人1、某人2」。
          </p>
        </div>
        <div class="modal-foot">
          <span class="hint">{{ maskSummary }}</span>
          <div style="flex: 1" />
          <button class="ins-btn ghost" @click="dialogs.masking = false">取消</button>
          <button class="ins-btn primary" :disabled="savingMask" @click="saveMasking">
            {{ savingMask ? '保存中…' : '保存' }}
          </button>
        </div>
      </div>
    </div>

    <!-- ===== 敏感主题隐私确认 ===== -->
    <div v-if="dialogs.privacy" class="mask" @click.self="dialogs.privacy = false">
      <div class="modal">
        <div class="modal-head">
          <div>
            <div class="shield"><t-icon name="lock-on" size="20px" /></div>
            <h3>这是一次敏感分析</h3>
            <p>「{{ selected?.name }}」会对你本人做出高敏感推断，首次运行前需要确认数据如何使用。</p>
          </div>
          <button class="ins-icon-btn" @click="dialogs.privacy = false"><t-icon name="close" size="15px" /></button>
        </div>
        <div class="modal-body">
          <div class="priv-list">
            <div class="priv-item"><t-icon name="lock-on" size="13px" /><span>报告、证据集与运行记录<strong>仅你本人可见</strong>，管理员不可读。</span></div>
            <div class="priv-item"><t-icon name="lock-on" size="13px" /><span>正文会经过你配置的模型服务商，请确认该服务商可接收这类内容。</span></div>
            <div class="priv-item"><t-icon name="delete" size="13px" /><span>你可以随时删除本次运行及其证据，删除后不可恢复。</span></div>
          </div>
          <div class="confirm-line" @click="agreeChecked = !agreeChecked">
            <span class="box" :class="{ on: agreeChecked }"><t-icon v-if="agreeChecked" name="check" size="10px" /></span>
            <span>我了解这类分析只是基于文档的倾向性推断，不是心理诊断。</span>
          </div>
        </div>
        <div class="modal-foot">
          <button class="ins-btn ghost" @click="dialogs.privacy = false">取消</button>
          <div style="flex: 1" />
          <button class="ins-btn primary" :disabled="!agreeChecked" @click="privacyConfirmed">继续</button>
        </div>
      </div>
    </div>

    <!-- ===== 新建 / 编辑主题 ===== -->
    <div v-if="dialogs.theme" class="mask" @click.self="closeThemeDialog">
      <div class="modal wide">
        <div class="modal-head">
          <div>
            <h3>{{ editingThemeId ? '编辑洞察主题' : '新建洞察主题' }}</h3>
            <p>主题只声明「看什么、怎么判断」，报告结构由系统统一，保证不同时间的报告可对比。</p>
          </div>
          <button class="ins-icon-btn" @click="closeThemeDialog"><t-icon name="close" size="15px" /></button>
        </div>
        <div v-if="!editingThemeId" class="tabs">
          <button class="tab" :class="{ on: themeTab === 'tpl' }" @click="themeTab = 'tpl'">从内置模板</button>
          <button class="tab" :class="{ on: themeTab === 'write' }" @click="themeTab = 'write'">手写提示词</button>
          <button class="tab" :class="{ on: themeTab === 'file' }" @click="themeTab = 'file'">上传方法包</button>
        </div>
        <div class="modal-body" style="padding-top: 14px">
          <div v-if="themeTab === 'tpl' && !editingThemeId" class="tpl-grid">
            <button
              v-for="p in presets"
              :key="p.name"
              class="tpl"
              :class="{ on: draft.name === p.name }"
              @click="applyPreset(p)"
            >
              <b><t-icon :name="presetIcon(p.name)" size="12px" />{{ p.name }}</b>
              <p>{{ p.description }}</p>
            </button>
          </div>
          <div v-if="themeTab === 'tpl' && !editingThemeId" class="hint-box" style="margin-top: 12px">
            <t-icon name="info-circle" size="13px" />
            <span>模板会复制成可编辑的主题，内置模板本身不会被改动。</span>
          </div>

          <template v-if="themeTab === 'write' || editingThemeId">
            <div class="field"><label>主题名称</label><input v-model="draft.name" placeholder="例如：我的决策复盘" /></div>
            <div class="field"><label>一句话说明</label><input v-model="draft.description" placeholder="这个主题想回答什么问题？" /></div>
            <div class="field">
              <label>分析维度与判断口径</label>
              <div v-for="(d, i) in draft.dimensions" :key="i" class="dim-edit">
                <input v-model="d.name" placeholder="维度名称" />
                <input v-model="d.criteria" placeholder="判断标准" />
                <select v-model="d.scale"><option value="0–100">0–100</option></select>
                <button class="ins-icon-btn" @click="draft.dimensions.splice(i, 1)"><t-icon name="close" size="12px" /></button>
              </div>
              <button class="ins-btn small" @click="draft.dimensions.push({ name: '', criteria: '', scale: '0–100' })">
                <t-icon name="add" size="12px" />添加维度
              </button>
            </div>
            <div class="rule" style="border: 1px solid var(--td-component-stroke); border-radius: 9px">
              <div class="rule-main"><b>标记为敏感主题</b><span>首次运行需要额外确认，报告中带敏感声明</span></div>
              <div class="switch" :class="{ on: draft.sensitive }" @click="draft.sensitive = !draft.sensitive" />
            </div>
          </template>

          <div v-if="themeTab === 'file' && !editingThemeId">
            <label class="drop">
              <t-icon name="upload" size="22px" style="margin-bottom: 6px" />
              <b>{{ uploading ? '正在解析…' : '选择 SKILL.md 或 .zip 方法包' }}</b>
              需包含 frontmatter（name / description / dimensions），正文作为分析口径；上传后可在表单里微调
              <input type="file" accept=".zip,.md,.markdown,.txt" hidden @change="onMethodFile" />
            </label>
            <div class="code">---
name: 决策复盘
description: 从复盘文档中检查决策过程
dimensions:
  - name: 决策质量
    criteria: 是否记录了备选方案与放弃理由
    scale: 0-100
    requires_evidence: true
  - name: 事后偏差
    criteria: 是否出现"早就知道"类表述
    scale: 0-100
---

# 分析方法
1. 先定位每个决策节点……
2. 区分结果好与决策好……</div>
            <div class="hint-box" style="margin-top: 12px">
              <t-icon name="lock-on" size="13px" />
              <span>方法包只作为分析与提示词规范加载，不执行其中的任何代码，也不会进入沙箱镜像。</span>
            </div>
          </div>
        </div>
        <div class="modal-foot">
          <span class="hint">创建后不会自动运行</span>
          <div style="flex: 1" />
          <button class="ins-btn ghost" @click="closeThemeDialog">取消</button>
          <button class="ins-btn primary" :disabled="!draft.name.trim()" @click="saveTheme">保存</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
/**
 * 洞察页。
 *
 * 一个组件承载四个屏（主题首页 / 运行 / 证据 / 报告）与自我洞悉，
 * 与原型保持同一套视觉与交互顺序；轮询只在本组件内，离开即清理。
 */
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import { useInsightStore } from '@/stores/insight'
import { sanitizeHTML } from '@/utils/security'
import { maskSensitiveTerms } from '@/workbench/insight-mask'
import { printMarkdown } from '@/workbench/insight-print'
import type {
  CreateThemePayload,
  InsightEvidence,
  InsightScope,
  InsightTheme,
} from '@/api/insight'

const store = useInsightStore()

const mode = ref<'topics' | 'self'>('topics')
const screen = ref<'home' | 'run' | 'evidence' | 'report'>('home')
const keyword = ref('')
const sampleRun = ref(true)
const runningThemeName = ref('')
const runningThemeId = ref('')
const acknowledged = ref<Set<string>>(new Set())
const agreeChecked = ref(false)
const confirming = ref(false)
const evidenceDim = ref('')
const onlyInference = ref(false)
const evidenceKeyword = ref('')
const dialogs = reactive({
  checkup: false,
  privacy: false,
  theme: false,
  scope: false,
  runs: false,
  masking: false,
})
/** 脱敏弹窗的本地草稿：textarea 按行编辑，保存时再拆成数组。 */
const maskDraft = reactive({
  persons: '',
  organizations: '',
  projects: '',
  hide_evidence_quotes: false,
})
const savingMask = ref(false)

const maskSummary = computed(() => {
  const count = (text: string) => text.split('\n').filter((line) => line.trim()).length
  const total = count(maskDraft.persons) + count(maskDraft.organizations) + count(maskDraft.projects)
  if (!total) return '尚未配置敏感词'
  return `将替换 ${total} 个词${maskDraft.hide_evidence_quotes ? '，并隐藏证据原文' : ''}`
})
const scope = reactive<InsightScope>({ folder_path: '', recent_days: 0 })
const themeTab = ref<'tpl' | 'write' | 'file'>('tpl')
const editingThemeId = ref('')
const uploading = ref(false)
let pollTimer: number | null = null
let selfTimer: number | null = null

const draft = reactive<CreateThemePayload>({
  name: '',
  description: '',
  sensitive: false,
  instructions: '',
  dimensions: [],
})

/** 内置模板卡片图标：只影响展示，不写入主题数据。 */
const PRESET_ICONS: Record<string, string> = {
  思维模式: 'chart-bubble',
  价值观: 'star',
  思维陷阱: 'info-circle',
  价值澄清: 'layers',
  'MBTI 倾向分析': 'table',
  主要矛盾: 'time',
}
function presetIcon(name: string) {
  return PRESET_ICONS[name] || 'file'
}

const presets: CreateThemePayload[] = [
  {
    name: '思维模式',
    description: '识别稳定的思考路径与决策惯性',
    sensitive: false,
    instructions: '关注反复出现的判断方式，而不是单次情绪表达。',
    dimensions: [
      { name: '决策偏好', criteria: '先行动再校准 / 先穷举再决定', scale: '0–100' },
      { name: '归因方式', criteria: '对己与人分别倾向内部还是外部归因', scale: '0–100' },
      { name: '时间视角', criteria: '短期交付与长期方向的注意力分配', scale: '0–100' },
      { name: '抽象层级', criteria: '偏好具体案例还是抽象原则', scale: '0–100' },
    ],
  },
  {
    name: '价值观',
    description: '找到真正看重的东西与言行落差',
    sensitive: true,
    instructions: '只看取舍，不看自述；优先采信付出过真实代价的选择。',
    dimensions: [
      { name: '价值排序', criteria: '在冲突取舍中优先保留什么', scale: '0–100' },
      { name: '言行落差', criteria: '宣称价值与实际选择之间的差距', scale: '0–100' },
      { name: '长期承诺', criteria: '反复投入时间的方向', scale: '0–100' },
    ],
  },
  {
    name: '思维陷阱',
    description: '过度概括、确认偏误、沉没成本',
    sensitive: false,
    instructions: '逐条检查认知偏差，允许结论为「未发现」。',
    dimensions: [
      { name: '过度概括', criteria: '从个别事件推出普遍结论', scale: '0–100' },
      { name: '确认偏误', criteria: '只记录支持既有判断的证据', scale: '0–100' },
      { name: '沉没成本', criteria: '因已投入而继续错误的投入', scale: '0–100' },
      { name: '二元对立', criteria: '把连续问题表述成非此即彼', scale: '0–100' },
    ],
  },
  {
    name: '价值澄清',
    description: '核心价值观、反价值与领域权重',
    sensitive: true,
    instructions: '区分核心价值观与反价值，避免抽象口号。',
    dimensions: [
      { name: '核心价值观', criteria: '反复出现且愿意付出代价的原则', scale: '0–100' },
      { name: '反价值', criteria: '明确不愿成为的样子', scale: '0–100' },
      { name: '领域权重', criteria: '工作 / 关系 / 成长 / 健康的投入比', scale: '0–100' },
    ],
  },
  {
    name: 'MBTI 倾向分析',
    description: '四个维度的倾向与证据分布',
    sensitive: true,
    instructions: '只给倾向参考，不做人格诊断；每个维度都要有行为证据。',
    dimensions: [
      { name: 'E / I', criteria: '能量来自外部交互还是独处', scale: '0–100' },
      { name: 'S / N', criteria: '关注具体事实还是可能性', scale: '0–100' },
      { name: 'T / F', criteria: '决策依据逻辑还是关系', scale: '0–100' },
      { name: 'J / P', criteria: '偏好计划还是开放选项', scale: '0–100' },
    ],
  },
  {
    name: '主要矛盾',
    description: '目标冲突、资源约束、关系张力',
    sensitive: false,
    instructions: '找出当前最消耗人的一组冲突，而不是罗列所有问题。',
    dimensions: [
      { name: '目标冲突', criteria: '两个都想达成但相互挤压的目标', scale: '0–100' },
      { name: '资源约束', criteria: '时间 / 精力 / 权限的硬约束', scale: '0–100' },
      { name: '关系张力', criteria: '反复出现的人际摩擦点', scale: '0–100' },
    ],
  },
]

const selected = computed(() => store.selected)
const builtinThemes = computed(() => store.builtinThemes.filter((t) => t.name.includes(keyword.value)))
const customThemes = computed(() => store.customThemes.filter((t) => t.name.includes(keyword.value)))
const checkup = computed(() => store.checkup)
/** 体检里四类被排除的文档总数，用于运行页副卡片。 */
const excludedDocs = computed(() => {
  const c = checkup.value
  if (!c) return 0
  return (c.empty ?? 0) + (c.too_short ?? 0) + (c.attachment ?? 0) + (c.duplicate ?? 0)
})
const run = computed(() => store.run)
const report = computed(() => store.report)
const self = computed(() => store.self)
/** 报告页顶栏的面包屑主题名。 */
const reportThemeName = computed(() => {
  const current = report.value
  if (!current) return ''
  return current.theme_name || store.themes.find((item) => item.id === current.theme_id)?.name || ''
})
/** 报告在主题历史里的序号（从 1 开始），对应原型「不可变快照 · 第 N 次运行」。
 * 历史列表按时间倒序，找不到（如从别处直接打开）时不展示序号。
 */
const reportOrdinal = computed(() => {
  const id = report.value?.id
  if (!id) return 0
  const index = store.reports.findIndex(
    (item) => item.id === id && item.theme_id === report.value?.theme_id,
  )
  return index >= 0 ? store.reports.length - index : 0
})
/** 七层副标题：与提示词里的七层口径保持一致。 */
const LAYER_SUBTITLES: Record<number, string> = {
  1: '我是谁',
  2: '我为什么这样',
  3: '我实际怎么做',
  4: '我怎么和人相处',
  5: '哪里让我最好',
  6: '我怎么走到这',
  7: '我为了什么',
}
/** 速览卡片图标按 key 固定映射，未知 key 退回通用图标。 */
const OVERVIEW_ICONS: Record<string, string> = {
  personality: 'chart-bubble',
  values: 'star',
  strengths: 'layers',
  weaknesses: 'info-circle',
  relationship: 'book-open',
  environment: 'time',
}
function layerTitle(layer: { no: number | string; name: string }) {
  const subtitle = LAYER_SUBTITLES[Number(layer.no)]
  return subtitle ? `${layer.name} · ${subtitle}` : layer.name
}
function overviewIcon(key?: string) {
  return (key && OVERVIEW_ICONS[key]) || 'chart-analytics'
}
function overviewHref(link: string) {
  return link === 'trio' ? '#ins-trio' : `#ins-${link}`
}
function overviewLinkText(o: { key?: string; link?: string }) {
  if (o.link === 'trio') return o.key === 'strengths' ? '看适用条件' : '看代价'
  return `详见 L${o.link?.replace('layer-', '') ?? ''}`
}
const validEvidence = computed(() => store.validEvidence)
/** 报告封面上的「导出脱敏已开启」只在确实配置了词表或隐藏原文时出现。 */
const maskConfigured = computed(() => {
  const privacy = store.privacy
  return !!(
    privacy.persons.length ||
    privacy.organizations.length ||
    privacy.projects.length ||
    privacy.hide_evidence_quotes
  )
})
const selfGateOk = computed(() => {
  const gate = store.self?.gate
  return !!gate && gate.completed >= gate.required
})

const topbarTitle = computed(() => {
  if (screen.value === 'report') return report.value?.title || `${report.value?.theme_name ?? runningThemeName.value}洞察报告`
  if (screen.value === 'evidence') return `证据集 · ${runningThemeName.value}`
  return `${runningThemeName.value} · 分析进行中`
})
const topbarSub = computed(() => {
  if (screen.value === 'report') return '报告为不可变快照；可另存为墨匣文档继续编辑'
  if (screen.value === 'evidence') {
    const docs = run.value?.docs_processed ?? 0
    return `${docs} 篇文档 → ${store.evidenceTotal} 条证据。确认后进入阶段二，只基于确认后的证据集生成报告；证据不可编辑，只能剔除。`
  }
  if (run.value?.stage === 'synthesis' || run.value?.status === 'stage2') {
    return '证据集已确认，正在归并生成报告'
  }
  return '素材体检已完成，正在逐篇抽取证据'
})
const stepIndex = computed(() => {
  if (screen.value === 'evidence') return 3
  if (screen.value === 'report') return 5
  if (run.value?.stage === 'synthesis' || run.value?.status === 'stage2') return 4
  if (run.value?.status === 'waiting_confirm') return 3
  return 2
})
const runStageLabel = computed(() => {
  if (run.value?.status === 'stage2' || run.value?.stage === 'synthesis') return '阶段二 · 归并生成报告'
  return '阶段一 · 证据抽取'
})
const evidenceDims = computed(() => Array.from(new Set(store.evidence.map((e) => e.dimension))))
/** 证据维度筛选 chip 上显示条数。 */
const evidenceDimCounts = computed(() => {
  const counts: Record<string, number> = {}
  for (const item of store.evidence) counts[item.dimension] = (counts[item.dimension] ?? 0) + 1
  return counts
})
const inferenceCount = computed(
  () => store.evidence.filter((item) => item.evidence_type === 'inference').length,
)
const reportAsk = ref('')
const asking = ref(false)
const selfAsk = ref('')
const selfAsking = ref(false)
const savingDoc = ref(false)
const scopeLabel = computed(() => {
  const parts: string[] = []
  if (scope.recent_days > 0) parts.push(`最近 ${scope.recent_days} 天`)
  if (scope.folder_path) parts.push(`文件夹「${scope.folder_path}」`)
  return parts.length ? parts.join(' · ') : '全部归档'
})

/** 报告内追问：服务端上下文限定为这份报告 + 它的证据集。 */
async function sendReportAsk() {
  const text = reportAsk.value.trim()
  if (!text || asking.value || !report.value) return
  reportAsk.value = ''
  asking.value = true
  try {
    await store.askReport(report.value.id, text)
  } catch (e: any) {
    MessagePlugin.error(e?.message || '追问失败，请稍后重试')
    reportAsk.value = text
  } finally {
    asking.value = false
  }
}
function themeMeta(t: InsightTheme) {
  if (!t.report_count) return '未运行'
  const base = `${t.report_count} 份报告 · ${relativeTime(t.last_run_at)}`
  return t.updated_docs ? `${base} · ${t.updated_docs} 篇更新` : base
}

/** 自我洞悉追问：服务端上下文限定为当前说明书 + 主题报告摘要。 */
const selfReady = computed(() => store.self?.status === 'ready' && !!store.self?.content)

async function sendSelfAsk() {
  const text = selfAsk.value.trim()
  if (!text || selfAsking.value || !selfReady.value) return
  selfAsk.value = ''
  selfAsking.value = true
  try {
    await store.askSelf(text)
  } catch (e: any) {
    MessagePlugin.error(e?.message || '追问失败，请稍后重试')
    selfAsk.value = text
  } finally {
    selfAsking.value = false
  }
}

const filteredEvidence = computed(() => {
  const kw = evidenceKeyword.value.trim()
  return store.evidence.filter((e) => {
    if (evidenceDim.value && e.dimension !== evidenceDim.value) return false
    if (onlyInference.value && e.evidence_type !== 'inference') return false
    if (kw && !`${e.quote}${e.knowledge_title}${e.dimension}`.includes(kw)) return false
    return true
  })
})
const conclusionParagraphs = computed(() =>
  (report.value?.content.conclusion || '').split(/\n+/).map((s) => s.trim()).filter(Boolean),
)
/** 画像由模型返回，含少量强调标签；渲染前统一消毒。 */
const safePortrait = computed(() => sanitizeHTML(store.self?.content?.portrait ?? ''))
const selfStats = computed(() => {
  const layers = store.self?.content?.layers ?? []
  const dims = layers.flatMap((l) => l.dims)
  return {
    themes: store.builtinThemes.filter((t) => t.report_count > 0).length,
    reports: store.builtinThemes.reduce((sum, t) => sum + t.report_count, 0),
    evidenced: dims.filter((d) => d.status === 'evidence').length,
    total: dims.length || 24,
  }
})

function glyphIcon(glyph: string): string {
  const map: Record<string, string> = {
    'i-spark': 'chart-bubble',
    'i-star': 'star',
    'i-info': 'info-circle',
    'i-layers': 'layers',
    'i-table': 'table',
    'i-clock': 'time',
    'i-edit': 'edit',
  }
  return map[glyph] ?? 'chart-analytics'
}
function relativeTime(value: string | null): string {
  if (!value) return '未运行'
  const diff = Date.now() - new Date(value).getTime()
  if (diff < 60000) return '刚刚'
  const mins = Math.floor(diff / 60000)
  if (mins < 60) return `${mins} 分钟前`
  const hours = Math.floor(mins / 60)
  if (hours < 24) return `${hours} 小时前`
  return `${Math.floor(hours / 24)} 天前`
}
function shortDate(value: string): string {
  if (!value) return ''
  return new Date(value).toISOString().slice(0, 10)
}
function formatTime(value: string | null): string {
  if (!value) return ''
  const d = new Date(value)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}
function clamp(v: number): number {
  return Math.max(0, Math.min(100, Number(v) || 0))
}
function layerCoverage(coverage: string): string {
  const m = coverage.match(/(\d+)\s*\/\s*(\d+)/)
  if (!m) return '0%'
  return `${Math.round((Number(m[1]) / Math.max(1, Number(m[2]))) * 100)}%`
}
function coverageTone(coverage: string): string {
  const pct = parseInt(layerCoverage(coverage), 10)
  if (pct >= 90) return 'green'
  if (pct >= 40) return 'amber'
  return 'outline'
}
function statusTone(status: string): string {
  if (status === 'evidence') return 'green'
  if (status === 'partial' || status === 'inference') return 'amber'
  return 'outline'
}
function statusLabel(status: string): string {
  const labels: Record<string, string> = {
    evidence: '有证据',
    partial: '部分证据',
    inference: '推断',
    blank: '待补证据',
  }
  return labels[status] ?? '待补证据'
}
function toast(msg: string) {
  MessagePlugin.info(msg)
}

function setMode(next: 'topics' | 'self') {
  mode.value = next
  if (next === 'self') {
    screen.value = 'home'
    void store.loadSelf()
    void store.loadSelfMessages()
  }
}

function openReport(id: string) {
  screen.value = 'report'
  reportAsk.value = ''
  void store.openReport(id, runningThemeName.value || selected.value?.name || '')
}

/** 打开范围设置：先拉一次体检以拿到可选文件夹。 */
function openScope() {
  void store.loadCheckup(store.selectedId, scope)
  dialogs.scope = true
}

function setScopeDays(days: number) {
  scope.recent_days = days
}

function openRuns() {
  void store.loadRuns()
  void store.loadReports()
  dialogs.runs = true
}

function runStatusLabel(status: string): string {
  const labels: Record<string, string> = {
    pending: '排队中',
    stage1: '抽取证据',
    waiting_confirm: '待确认证据',
    stage2: '生成报告',
    done: '已完成',
    failed: '失败',
    cancelled: '已终止',
  }
  return labels[status] ?? status
}

function runStatusTone(status: string): string {
  if (status === 'done') return 'green'
  if (status === 'failed') return 'red'
  if (status === 'cancelled') return 'outline'
  return 'amber'
}

function isRunningStatus(status: string): boolean {
  return ['pending', 'stage1', 'waiting_confirm', 'stage2'].includes(status)
}

function reportForRun(runId: string) {
  return store.reports.find((item) => item.run_id === runId)
}

function openReportFromRun(runId: string) {
  const matched = reportForRun(runId)
  if (!matched) return
  dialogs.runs = false
  openReport(matched.id)
}

function confirmDeleteRun(runId: string) {
  const dialog = DialogPlugin.confirm({
    header: '删除这次运行？',
    body: '它的证据集会一并删除；已经生成的报告仍可查看。',
    theme: 'warning',
    confirmBtn: '删除',
    onConfirm: async () => {
      try {
        await store.removeRun(runId)
        MessagePlugin.success('已删除')
      } catch {
        MessagePlugin.error('删除失败')
      }
      dialog.hide()
    },
  })
}

/** 按文档批量剔除证据：同一篇文档的所有未排除条目一次处理。 */
async function excludeByDocument(knowledgeId: string) {
  const ids = store.evidence
    .filter((item) => item.knowledge_id === knowledgeId && !item.excluded)
    .map((item) => item.id)
  if (!ids.length) return
  try {
    await store.setEvidenceExcluded(ids, true)
    MessagePlugin.success(`已剔除 ${ids.length} 条证据`)
  } catch {
    MessagePlugin.error('剔除失败')
  }
}

/** 把不可变报告转成 Markdown，用于复制或下载留档。 */
function buildReportMarkdown(): string {
  const current = report.value
  if (!current) return ''
  const content = current.content
  // 可选段落（发现 / 证据 / 反证 / 建议）可能为空，按空数组处理保证导出可用。
  const findings = content.findings ?? []
  const evidenceGroups = content.evidence_groups ?? []
  const counterpoints = content.counterpoints ?? []
  const scores = content.scores ?? []
  const actions = content.actions ?? []
  const lines: string[] = [
    `# ${current.title || `${current.theme_name}洞察报告`}`,
    '',
    `> ${formatTime(current.created_at)} · 覆盖 ${current.docs_covered} 篇 · ${current.evidence_count} 条证据`,
    '',
    '## 01 素材覆盖度',
    '',
    content.coverage_note || '',
    '',
    '## 02 核心发现',
    '',
  ]
  for (const finding of findings) {
    lines.push(
      `### ${finding.title}`,
      `类型：${finding.kind === 'inference' ? '推断' : '有证据'} · 置信度：${finding.confidence || '中'}`,
      '',
      finding.detail,
      '',
    )
    if (finding.refs?.length) lines.push(`引用：${finding.refs.join('；')}`, '')
  }
  lines.push('## 03 证据链', '')
  for (const quote of evidenceGroups) {
    // 隐藏原文时只保留来源：句子本身最容易带出可识别信息。
    const body = store.privacy.hide_evidence_quotes ? '（证据原文已隐藏）' : quote.quote
    lines.push(`> ${body}`, `> — ${quote.source}`, '')
  }
  lines.push('## 04 反证与不确定性', '')
  for (const point of counterpoints) lines.push(`- ${point}`)
  lines.push('', '## 05 维度评分', '')
  for (const score of scores) lines.push(`- ${score.name}：${score.label} ${score.value}`)
  lines.push('', '## 06 结论', '', content.conclusion || '')
  if (content.conclusion_basis) lines.push('', `依据：${content.conclusion_basis}`)
  lines.push('', '## 07 建议与下一步', '')
  actions.forEach((action, index) => lines.push(`${index + 1}. ${action}`))
  return lines.join('\n')
}

/** 打开脱敏弹窗：把已保存的数组回填成按行编辑。 */
function openMasking() {
  maskDraft.persons = store.privacy.persons.join('\n')
  maskDraft.organizations = store.privacy.organizations.join('\n')
  maskDraft.projects = store.privacy.projects.join('\n')
  maskDraft.hide_evidence_quotes = store.privacy.hide_evidence_quotes
  dialogs.masking = true
}

async function saveMasking() {
  if (savingMask.value) return
  savingMask.value = true
  try {
    const toList = (text: string) =>
      text.split('\n').map((line) => line.trim()).filter(Boolean)
    await store.savePrivacy({
      persons: toList(maskDraft.persons),
      organizations: toList(maskDraft.organizations),
      projects: toList(maskDraft.projects),
      hide_evidence_quotes: maskDraft.hide_evidence_quotes,
    })
    dialogs.masking = false
    MessagePlugin.success('脱敏设置已保存')
  } catch (e: any) {
    MessagePlugin.error(e?.message || '保存失败，请稍后重试')
  } finally {
    savingMask.value = false
  }
}

/** 导出统一入口：先按设置隐藏证据原文，再按词表脱敏。 */
function maskedReportMarkdown(): string {
  const markdown = buildReportMarkdown()
  if (!markdown) return ''
  return maskSensitiveTerms(markdown, store.privacy)
}

async function copyReportMarkdown() {
  const markdown = maskedReportMarkdown()
  if (!markdown) return
  try {
    await navigator.clipboard.writeText(markdown)
    MessagePlugin.success('Markdown 已复制')
  } catch {
    MessagePlugin.error('复制失败，请改用导出')
  }
}

/** 触发浏览器下载：报告与自我说明书共用，避免两份重复逻辑。 */
function downloadMarkdown(markdown: string, filename: string) {
  const blob = new Blob([markdown], { type: 'text/markdown;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = `${filename}.md`
  document.body.appendChild(link)
  link.click()
  document.body.removeChild(link)
  URL.revokeObjectURL(url)
}

function exportReportMarkdown() {
  const markdown = maskedReportMarkdown()
  if (!markdown) return
  downloadMarkdown(markdown, report.value?.title || '洞察报告')
}

/** 打印版 PDF：走系统打印对话框，用户可另存为 PDF。 */
function printReportMarkdown() {
  const markdown = maskedReportMarkdown()
  if (!markdown) return
  printMarkdown(report.value?.title || '洞察报告', markdown)
}

/** 存为墨匣文档：Markdown → Y.Doc 在浏览器内完成，成功后可在墨匣直接打开。 */
async function saveReportAsDoc() {
  const markdown = maskedReportMarkdown()
  if (!markdown || savingDoc.value) return
  savingDoc.value = true
  try {
    const { saveMarkdownAsWorkbenchDoc } = await import('@/workbench/insight-export')
    const saved = await saveMarkdownAsWorkbenchDoc({
      title: report.value?.title || `${report.value?.theme_name ?? '洞察'}洞察报告`,
      markdown,
    })
    MessagePlugin.success(`已存为墨匣文档「${saved.title}」`)
  } catch (e: any) {
    MessagePlugin.error(e?.message || '保存失败，请稍后重试')
  } finally {
    savingDoc.value = false
  }
}

/** 自我说明书的 Markdown 版本，与页面上七层结构一一对应。 */
function buildSelfMarkdown(): string {
  const content = self.value?.content
  if (!content) return ''
  // 模型可能省略可选段落，数组字段按空数组处理，避免导出直接报错。
  const overview = content.overview ?? []
  const layers = content.layers ?? []
  const cross = content.cross ?? []
  const trios = content.trios ?? []
  const next = content.next ?? []
  const lines: string[] = ['# 自我说明书', '', content.portrait || '', '']
  if (overview.length) {
    lines.push('## 自我速览', '')
    for (const item of overview) lines.push(`- **${item.title}**：${item.text}`)
    lines.push('')
  }
  lines.push('## 七层结构', '')
  for (const layer of layers) {
    lines.push(`### 第${layer.no}层 · ${layer.name}`, '')
    if (layer.question) lines.push(`> ${layer.question}`, '')
    if (layer.coverage) lines.push(`覆盖：${layer.coverage}`, '')
    if (layer.lead) lines.push('', layer.lead)
    lines.push('')
    for (const dim of layer.dims ?? []) {
      const source = dim.source ? `（来源：${dim.source}）` : ''
      lines.push(`- **${dim.name}**：${dim.text}${source}`)
    }
    lines.push('')
  }
  if (cross.length) {
    lines.push('## 贯穿视角', '')
    for (const item of cross) lines.push(`### ${item.title}`, '', item.text, '')
  }
  if (trios.length) {
    lines.push('## 特质三元组', '')
    for (const trio of trios) {
      lines.push(`- **${trio.trait}**：在${trio.situation}；代价是${trio.cost}`)
    }
    lines.push('')
  }
  if (next.length) {
    lines.push('## 下一步', '')
    next.forEach((item, index) => lines.push(`${index + 1}. ${item.title}：${item.text}`))
  }
  return lines.join('\n')
}

/** 自我说明书的导出入口：与报告共用同一份脱敏词表。 */
function maskedSelfMarkdown(): string {
  const markdown = buildSelfMarkdown()
  if (!markdown) return ''
  return maskSensitiveTerms(markdown, store.privacy)
}

function exportSelfMarkdown() {
  const markdown = maskedSelfMarkdown()
  if (!markdown) {
    MessagePlugin.warning('还没有可导出的说明书')
    return
  }
  downloadMarkdown(markdown, '自我说明书')
}

async function saveSelfAsDoc() {
  const markdown = maskedSelfMarkdown()
  if (!markdown || savingDoc.value) return
  savingDoc.value = true
  try {
    const { saveMarkdownAsWorkbenchDoc } = await import('@/workbench/insight-export')
    const saved = await saveMarkdownAsWorkbenchDoc({ title: '自我说明书', markdown })
    MessagePlugin.success(`已存为墨匣文档「${saved.title}」`)
  } catch (e: any) {
    MessagePlugin.error(e?.message || '保存失败，请稍后重试')
  } finally {
    savingDoc.value = false
  }
}

function backHome() {
  stopPolling()
  screen.value = 'home'
  mode.value = 'topics'
  void store.loadThemes()
  if (store.selectedId) void store.loadReports(store.selectedId)
}

function openNewTheme() {
  editingThemeId.value = ''
  themeTab.value = 'tpl'
  Object.assign(draft, {
    name: '',
    description: '',
    sensitive: false,
    instructions: '',
    dimensions: [{ name: '', criteria: '', scale: '0–100' }],
  })
  applyPreset(presets[0])
  dialogs.theme = true
}

function openEditTheme() {
  if (!selected.value) return
  editingThemeId.value = selected.value.id
  themeTab.value = 'write'
  Object.assign(draft, {
    name: selected.value.name,
    description: selected.value.description,
    sensitive: selected.value.sensitive,
    instructions: selected.value.instructions,
    dimensions: selected.value.dimensions.map((d) => ({ ...d })),
  })
  dialogs.theme = true
}

function applyPreset(preset: CreateThemePayload) {
  Object.assign(draft, {
    name: preset.name,
    description: preset.description,
    sensitive: preset.sensitive,
    instructions: preset.instructions,
    dimensions: preset.dimensions.map((d) => ({ ...d })),
  })
  themeTab.value = 'write'
}

function closeThemeDialog() {
  dialogs.theme = false
  editingThemeId.value = ''
}

async function saveTheme() {
  if (!draft.name.trim()) return
  try {
    const payload: CreateThemePayload = {
      name: draft.name.trim(),
      description: draft.description.trim(),
      sensitive: draft.sensitive,
      instructions: draft.instructions || '',
      dimensions: draft.dimensions.filter((d) => d.name.trim()),
    }
    if (editingThemeId.value) {
      await store.updateTheme(editingThemeId.value, payload)
    } else {
      await store.createTheme(payload)
    }
    closeThemeDialog()
    MessagePlugin.success('已保存')
  } catch {
    MessagePlugin.error('保存失败')
  }
}

async function onMethodFile(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  uploading.value = true
  try {
    const parsed = await store.importTheme(file)
    draft.name = parsed.name
    draft.description = parsed.description
    draft.sensitive = parsed.sensitive
    draft.instructions = parsed.instructions
    draft.dimensions = parsed.dimensions.length
      ? parsed.dimensions
      : draft.dimensions
    themeTab.value = 'write'
    MessagePlugin.success('方法包已解析')
  } catch (e: any) {
    MessagePlugin.error(e?.message || '方法包解析失败')
  } finally {
    uploading.value = false
    input.value = ''
  }
}

async function startFlow() {
  if (!selected.value) return
  runningThemeId.value = selected.value.id
  runningThemeName.value = selected.value.name
  if (selected.value.sensitive && !acknowledged.value.has(selected.value.id)) {
    agreeChecked.value = false
    dialogs.privacy = true
    return
  }
  await openCheckup()
}

async function openCheckup() {
  try {
    await store.loadCheckup(runningThemeId.value, scope)
    sampleRun.value = true
    dialogs.checkup = true
  } catch {
    MessagePlugin.error('素材体检失败')
  }
}

function privacyConfirmed() {
  acknowledged.value.add(runningThemeId.value)
  dialogs.privacy = false
  void openCheckup()
}

async function confirmCheckup() {
  dialogs.checkup = false
  screen.value = 'run'
  try {
    await store.beginRun({
      sample: sampleRun.value,
      max_docs: 300,
      folder_path: scope.folder_path || undefined,
      recent_days: scope.recent_days || undefined,
    })
    startPolling()
  } catch {
    screen.value = 'home'
    MessagePlugin.error('启动分析失败')
  }
}

function startPolling() {
  stopPolling()
  pollTimer = window.setInterval(async () => {
    let current = null
    try {
      current = await store.refreshRun()
    } catch {
      return
    }
    if (!current) return
    if (current.status === 'waiting_confirm') {
      stopPolling()
      await store.loadEvidence()
      screen.value = 'evidence'
      return
    }
    if (current.status === 'done') {
      stopPolling()
      await store.loadReports()
      await store.loadThemes()
      if (store.reports.length) {
        screen.value = 'report'
        await store.openReport(store.reports[0].id, runningThemeName.value)
      } else {
        screen.value = 'home'
      }
      return
    }
    if (current.status === 'failed' || current.status === 'cancelled') {
      stopPolling()
    }
  }, 1500)
}

function stopPolling() {
  if (pollTimer !== null) {
    window.clearInterval(pollTimer)
    pollTimer = null
  }
}

async function stopRun() {
  try {
    await store.cancelRun()
  } finally {
    stopPolling()
    MessagePlugin.info('已终止运行，已抽取的证据保留')
    backHome()
  }
}

async function toggleEvidence(item: InsightEvidence) {
  try {
    await store.setEvidenceExcluded([item.id], !item.excluded)
  } catch {
    MessagePlugin.error('更新证据失败')
  }
}

async function confirmEvidence() {
  confirming.value = true
  try {
    await store.confirmEvidence()
    screen.value = 'run'
    startPolling()
  } catch {
    MessagePlugin.error('生成报告失败')
  } finally {
    confirming.value = false
  }
}

async function regenerateSelf() {
  try {
    await store.regenerateSelf()
    selfAsk.value = ''
    MessagePlugin.info('已开始生成，完成后会自动刷新')
    startSelfPolling()
  } catch (e: any) {
    MessagePlugin.error(e?.message || '生成失败，请先完成全部内置主题')
  }
}

/** 自我洞悉是后台任务：每 2 秒刷新一次，直到成功或失败。 */
function startSelfPolling() {
  stopSelfPolling()
  selfTimer = window.setInterval(async () => {
    let current = null
    try {
      current = await store.loadSelf()
    } catch {
      return
    }
    if (!current || current.status !== 'generating') {
      stopSelfPolling()
      if (current?.status === 'ready') MessagePlugin.success('自我洞悉已更新')
      if (current?.status === 'failed') MessagePlugin.error(current.error || '生成失败')
    }
  }, 2000)
}

function stopSelfPolling() {
  if (selfTimer !== null) {
    window.clearInterval(selfTimer)
    selfTimer = null
  }
}

onMounted(async () => {
  try {
    await store.loadThemes()
    if (store.selectedId) await store.selectTheme(store.selectedId)
  } catch {
    MessagePlugin.error('洞察主题加载失败')
  }
  try {
    await store.loadSelf()
  } catch {
    // 自我洞悉依赖主题报告；单独失败不影响主题列表
  }
  // 脱敏词表失败时按空表处理，不阻断页面
  void store.loadPrivacy()
})

onBeforeUnmount(() => {
  stopPolling()
  stopSelfPolling()
})
</script>

<style lang="less" scoped>
/* 视觉与 docs/design/insight-module-prototype.html 保持一致，变量复用全局主题 */
.ins {
  position: relative;
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
  margin: var(--app-space-4) var(--app-space-5) var(--app-space-5);
  background: var(--td-bg-color-container);
  border: 1px solid var(--td-component-stroke);
  border-radius: var(--app-radius-xl);
  box-shadow: 0 8px 24px rgba(22, 32, 26, 0.06);
  overflow: hidden;
}
.ins-topbar {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  padding: 18px 22px 14px;
  border-bottom: 1px solid var(--td-component-stroke);
}
.ins-topbar-main { min-width: 0; }
.ins-topbar.compact { align-items: center; padding: 11px 18px; }
.crumb { font-size: 11.5px; color: var(--td-text-color-placeholder); }
.crumb b { font-weight: 500; color: var(--td-text-color-secondary); }
.ins-title-row { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; }
.ins-title-row h1 { font-size: 17px; font-weight: 600; margin: 0; display: flex; align-items: center; gap: 8px; }
.ins-beta { font-size: 10px; padding: 2px 6px; border-radius: 999px; background: var(--td-brand-color-1); color: var(--td-brand-color-7); font-weight: 600; }
.ins-sub { margin: 5px 0 0; font-size: 12px; color: var(--td-text-color-placeholder); }
.ins-topbar-actions { display: flex; align-items: center; gap: 8px; flex: none; }
.ins-mode { display: inline-flex; padding: 2px; background: var(--td-bg-color-secondarycontainer); border-radius: 999px; gap: 2px; }
.ins-mode button { height: 26px; padding: 0 11px; border-radius: 999px; border: 0; background: transparent; font-size: 12px; color: var(--td-text-color-secondary); display: inline-flex; align-items: center; gap: 5px; cursor: pointer; }
.ins-mode button.on { background: var(--td-bg-color-container); color: var(--td-text-color-primary); font-weight: 500; box-shadow: 0 1px 3px rgba(0, 0, 0, 0.1); }
.ins-dot { width: 5px; height: 5px; border-radius: 50%; background: var(--td-brand-color); }
.ins-gate { display: inline-flex; align-items: center; gap: 5px; height: 28px; padding: 0 10px; border-radius: 999px; background: var(--td-brand-color-1); color: var(--td-brand-color-7); font-size: 11px; font-weight: 500; }
.ins-gate.warn { background: var(--td-warning-color-1); color: var(--td-warning-color-7); }
.ins-btn { display: inline-flex; align-items: center; justify-content: center; gap: 6px; height: 32px; padding: 0 13px; border: 1px solid var(--td-component-stroke); border-radius: 8px; background: var(--td-bg-color-container); font-size: 12.5px; font-weight: 500; color: var(--td-text-color-primary); cursor: pointer; white-space: nowrap; }
.ins-btn:hover { border-color: var(--td-brand-color); color: var(--td-brand-color-7); }
.ins-btn.primary { background: linear-gradient(135deg, var(--td-brand-color-3), var(--td-brand-color-6)); border-color: transparent; color: #fff; box-shadow: 0 6px 14px color-mix(in srgb, var(--td-brand-color) 26%, transparent); }
.ins-btn.primary:hover { color: #fff; filter: brightness(1.03); }
.ins-btn.ghost { background: transparent; border-color: transparent; color: var(--td-text-color-secondary); }
.ins-btn.ghost:hover { background: var(--td-bg-color-container-hover); color: var(--td-text-color-primary); }
.ins-btn.ghost.danger { color: var(--td-error-color); }
.ins-btn.small { height: 27px; padding: 0 9px; font-size: 11.5px; border-radius: 6px; }
.ins-btn[disabled] { opacity: 0.45; pointer-events: none; }
.ins-icon-btn { width: 28px; height: 28px; border: 0; border-radius: 8px; background: transparent; color: var(--td-text-color-secondary); display: grid; place-items: center; cursor: pointer; }
.ins-icon-btn:hover { background: var(--td-bg-color-container-hover); color: var(--td-text-color-primary); }
.ins-tag { display: inline-flex; align-items: center; gap: 4px; padding: 2px 7px; border-radius: 999px; font-size: 10.5px; line-height: 1.6; background: var(--td-bg-color-secondarycontainer); color: var(--td-text-color-secondary); font-weight: 500; white-space: nowrap; }
.ins-tag.green { background: var(--td-brand-color-1); color: var(--td-brand-color-7); }
.ins-tag.amber { background: var(--td-warning-color-1); color: var(--td-warning-color-7); }
.ins-tag.red { background: var(--td-error-color-1); color: var(--td-error-color-7); }
.ins-tag.outline { background: transparent; border: 1px solid var(--td-component-stroke); color: var(--td-text-color-placeholder); }
.ins-tag.tiny { padding: 0 5px; font-size: 9px; }
.ins-tag.fit { justify-self: start; }

.home-grid { display: flex; flex: 1; min-height: 0; }
.theme-pane { width: 292px; flex: none; border-right: 1px solid var(--td-component-stroke); display: flex; flex-direction: column; min-height: 0; }
.pane-head { padding: 12px 14px 8px; display: flex; align-items: center; justify-content: space-between; font-size: 12.5px; }
.pane-head span { font-size: 11px; color: var(--td-text-color-placeholder); }
.search { display: flex; align-items: center; gap: 6px; margin: 0 14px 10px; padding: 0 9px; height: 30px; border: 1px solid var(--td-component-stroke); border-radius: 8px; color: var(--td-text-color-placeholder); background: var(--td-bg-color-secondarycontainer); }
.search input { flex: 1; min-width: 0; border: 0; background: transparent; font-size: 12px; color: var(--td-text-color-primary); outline: none; }
.table-search { margin: 0; width: 200px; }
.theme-scroll { flex: 1; overflow-y: auto; padding: 0 8px 12px; }
.list-label { display: flex; align-items: center; justify-content: space-between; padding: 10px 8px 5px; font-size: 10px; letter-spacing: 0.06em; color: var(--td-text-color-placeholder); }
.theme-item { display: flex; align-items: flex-start; gap: 9px; width: 100%; padding: 9px; border: 0; border-radius: 10px; background: transparent; text-align: left; cursor: pointer; position: relative; overflow: hidden; }
.theme-item:hover { background: var(--td-bg-color-container-hover); }
.theme-item.is-active { background: var(--td-brand-color-1); }
.theme-item.is-active::before { content: ''; position: absolute; left: 0; top: 11px; bottom: 11px; width: 2.5px; border-radius: 2px; background: var(--td-brand-color-6); }
.theme-glyph { width: 26px; height: 26px; flex: none; border-radius: 8px; display: grid; place-items: center; background: var(--td-bg-color-secondarycontainer); color: var(--td-text-color-secondary); }
.theme-item.is-active .theme-glyph { background: var(--td-bg-color-container); color: var(--td-brand-color-7); }
.theme-copy { min-width: 0; flex: 1; display: block; overflow: hidden; }
.theme-name { display: flex; align-items: center; gap: 5px; min-width: 0; font-size: 12.5px; font-weight: 500; color: var(--td-text-color-primary); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.theme-name .ins-tag { flex: none; }
.theme-desc { display: block; width: 100%; margin-top: 2px; font-size: 11px; color: var(--td-text-color-placeholder); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.theme-meta { margin-top: 4px; display: flex; align-items: center; gap: 6px; font-size: 10px; color: var(--td-text-color-disabled); }
.dot { width: 5px; height: 5px; border-radius: 50%; background: var(--td-gray-color-5); }
.dot.run { background: var(--td-brand-color); box-shadow: 0 0 0 3px var(--td-brand-color-1); }

.detail-pane { flex: 1; min-width: 0; overflow-y: auto; }
.detail-inner { max-width: 880px; padding: 22px 26px 40px; }
.detail-head { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
.detail-title { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.detail-title h2 { font-size: 20px; font-weight: 600; margin: 0; letter-spacing: -0.01em; }
.detail-desc { margin-top: 8px; font-size: 12.5px; line-height: 1.7; color: var(--td-text-color-secondary); max-width: 640px; }
.detail-actions { display: flex; gap: 8px; flex: none; }
.meta-strip { display: flex; gap: 26px; margin-top: 16px; padding: 12px 14px; border: 1px solid var(--td-component-stroke); border-radius: 10px; background: var(--td-bg-color-secondarycontainer); }
.meta-strip div { display: flex; flex-direction: column; gap: 3px; }
.meta-strip span { font-size: 10.5px; color: var(--td-text-color-placeholder); }
.meta-strip b { font-size: 12.5px; font-weight: 600; }
.scope-card { margin-top: 14px; padding: 13px 14px; border: 1px solid var(--td-component-stroke); border-radius: 10px; display: flex; align-items: center; gap: 12px; }
.scope-main { flex: 1; min-width: 0; }
.scope-main b { font-size: 12.5px; }
.scope-main p { margin: 3px 0 0; font-size: 11.5px; color: var(--td-text-color-placeholder); }
/* 上次报告后文档有更新的提醒条：只提示，不阻断任何操作 */
.stale-bar { margin-top: 10px; padding: 10px 13px; border-radius: 10px; display: flex; align-items: center; gap: 9px; font-size: 11.5px; line-height: 1.6; color: var(--td-text-color-secondary); background: var(--td-warning-color-1, #fff7e6); border: 1px solid var(--td-warning-color-3, #f5c26b); }
.stale-bar span { flex: 1; min-width: 0; }
.stale-bar b { font-weight: 600; }
.section-title { display: flex; align-items: center; gap: 8px; margin: 22px 0 10px; font-size: 13px; font-weight: 600; }
.section-title .count { font-size: 11px; color: var(--td-text-color-placeholder); font-weight: 400; }
.section-title .right { margin-left: auto; font-size: 11.5px; font-weight: 400; color: var(--td-text-color-placeholder); }
.dim-table { border: 1px solid var(--td-component-stroke); border-radius: 10px; overflow: hidden; }
.dim-row { display: grid; grid-template-columns: 150px 1fr 130px; gap: 12px; padding: 10px 13px; border-bottom: 1px solid var(--td-component-stroke); font-size: 12px; align-items: center; }
.dim-row:last-child { border-bottom: 0; }
.dim-row.head { background: var(--td-bg-color-secondarycontainer); font-size: 10.5px; color: var(--td-text-color-placeholder); }
.dim-row b { font-weight: 500; }
.dim-row span { color: var(--td-text-color-secondary); line-height: 1.6; }
.report-row { display: flex; align-items: center; gap: 10px; padding: 11px 12px; border-bottom: 1px solid var(--td-component-stroke); font-size: 12px; cursor: pointer; border-radius: 8px; }
.report-row:hover { background: var(--td-bg-color-container-hover); }
.report-row .date { width: 74px; color: var(--td-text-color-secondary); font-variant-numeric: tabular-nums; }
.report-row .name { flex: 1; font-weight: 500; }
.report-row .facts { font-size: 10.5px; color: var(--td-text-color-placeholder); }
.empty-box { margin-top: 10px; padding: 26px; border: 1px dashed var(--td-component-stroke); border-radius: 10px; text-align: center; color: var(--td-text-color-placeholder); font-size: 12px; }
.empty-box b { display: block; font-size: 12.5px; color: var(--td-text-color-secondary); margin-bottom: 4px; }
.privacy-note { margin-top: 22px; display: flex; gap: 8px; padding: 10px 12px; border-radius: 9px; background: var(--td-bg-color-secondarycontainer); color: var(--td-text-color-placeholder); font-size: 11px; line-height: 1.7; }

.run-body { padding: 26px 30px; display: flex; gap: 24px; overflow-y: auto; }
.run-main { flex: 1; max-width: 760px; }
.stepper { display: flex; align-items: center; margin-bottom: 26px; }
.step { display: flex; align-items: center; gap: 8px; font-size: 11.5px; color: var(--td-text-color-placeholder); white-space: nowrap; }
.step .num { width: 22px; height: 22px; border-radius: 50%; display: grid; place-items: center; border: 1px solid var(--td-component-stroke); font-size: 10.5px; background: var(--td-bg-color-container); }
.step.done { color: var(--td-brand-color-7); }
.step.done .num { background: var(--td-brand-color-1); border-color: var(--td-brand-color-2); color: var(--td-brand-color-7); }
.step.active { color: var(--td-text-color-primary); font-weight: 500; }
.step.active .num { background: var(--td-brand-color); border-color: var(--td-brand-color); color: #fff; }
.step-line { flex: 1; height: 1px; background: var(--td-component-stroke); margin: 0 10px; }
.step-line.done { background: var(--td-brand-color-3); }
.run-card { border: 1px solid var(--td-component-stroke); border-radius: 12px; padding: 18px; }
.run-card-head { display: flex; align-items: center; justify-content: space-between; }
.run-card-head b { font-size: 13px; }
.run-card-head span { font-size: 11.5px; color: var(--td-text-color-placeholder); font-variant-numeric: tabular-nums; }
.bar { height: 6px; border-radius: 99px; background: var(--td-bg-color-secondarycontainer); margin-top: 12px; overflow: hidden; }
.bar i { display: block; height: 100%; border-radius: 99px; background: linear-gradient(90deg, var(--td-brand-color-3), var(--td-brand-color-6)); transition: width 0.4s ease; }
.run-stats { display: flex; gap: 20px; margin-top: 12px; font-size: 11px; color: var(--td-text-color-placeholder); }
.run-stats b { color: var(--td-text-color-secondary); font-weight: 500; }
.log { margin-top: 16px; padding: 12px; border-radius: 9px; background: var(--td-bg-color-secondarycontainer); font-size: 11px; line-height: 1.9; color: var(--td-text-color-secondary); font-family: var(--app-font-family-mono); max-height: 180px; overflow-y: auto; }
.log .t { color: var(--td-text-color-disabled); margin-right: 8px; }
.run-aside { width: 260px; flex: none; }
.tip-card { border: 1px solid var(--td-component-stroke); border-radius: 12px; padding: 14px; font-size: 11.5px; line-height: 1.8; color: var(--td-text-color-secondary); }
.tip-card + .tip-card { margin-top: 12px; }
.tip-card b { display: block; font-size: 12px; color: var(--td-text-color-primary); margin-bottom: 6px; }

.evidence-page { display: flex; flex-direction: column; flex: 1; min-height: 0; }
.evidence-toolbar { display: flex; align-items: center; gap: 8px; padding: 12px 22px; border-bottom: 1px solid var(--td-component-stroke); flex-wrap: wrap; }
.chip { height: 27px; padding: 0 10px; border-radius: 999px; border: 1px solid var(--td-component-stroke); background: var(--td-bg-color-container); font-size: 11.5px; color: var(--td-text-color-secondary); display: inline-flex; align-items: center; gap: 5px; cursor: pointer; }
.chip.on { background: var(--td-brand-color-1); border-color: var(--td-brand-color-2); color: var(--td-brand-color-7); font-weight: 500; }
.chip .n { font-size: 10px; color: var(--td-text-color-placeholder); }
.evidence-wrap { flex: 1; overflow: auto; padding: 0 22px; }
table.ev { width: 100%; border-collapse: collapse; font-size: 12px; }
table.ev th { position: sticky; top: 0; background: var(--td-bg-color-container); text-align: left; font-size: 10.5px; font-weight: 500; color: var(--td-text-color-placeholder); padding: 9px 8px; border-bottom: 1px solid var(--td-component-stroke); z-index: 1; }
table.ev td { padding: 11px 8px; border-bottom: 1px solid var(--td-component-stroke); vertical-align: top; }
table.ev tr.is-excluded td { color: var(--td-text-color-disabled); }
table.ev tr.is-excluded .q { text-decoration: line-through; text-decoration-color: var(--td-gray-color-5); }
.q { line-height: 1.6; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
.src { font-size: 10.5px; color: var(--td-text-color-placeholder); line-height: 1.6; }
.src b { font-weight: 500; color: var(--td-text-color-secondary); }
.link-btn { margin-top: 4px; padding: 0; border: 0; background: none; font-size: 10px; color: var(--td-text-color-placeholder); text-decoration: underline; cursor: pointer; }
.link-btn:hover { color: var(--td-brand-color-7); }
.chk { width: 15px; height: 15px; border-radius: 4px; border: 1.5px solid var(--td-gray-color-5); display: inline-grid; place-items: center; background: var(--td-bg-color-container); cursor: pointer; color: #fff; }
.chk.on { background: var(--td-brand-color); border-color: var(--td-brand-color); }
.evidence-foot { display: flex; align-items: center; gap: 14px; padding: 12px 22px; border-top: 1px solid var(--td-component-stroke); }
.evidence-foot .stat { font-size: 11.5px; color: var(--td-text-color-secondary); }
.evidence-foot .stat b { font-weight: 600; color: var(--td-text-color-primary); }
.foot-hint { font-size: 10.5px; color: var(--td-text-color-placeholder); }

.report-layout { flex: 1; min-height: 0; display: flex; }
.report-scroll { flex: 1; overflow-y: auto; min-width: 0; }
.report { max-width: 780px; margin: 0 auto; padding: 30px 28px 70px; }
.report-cover { padding-bottom: 18px; border-bottom: 1px solid var(--td-component-stroke); }
.snapshot { display: inline-flex; align-items: center; gap: 5px; padding: 2px 8px; border-radius: 999px; background: var(--td-bg-color-secondarycontainer); color: var(--td-text-color-secondary); font-size: 10px; margin-bottom: 12px; }
.report-cover h1 { font-size: 24px; font-weight: 600; margin: 0; letter-spacing: -0.02em; }
.report-meta { margin-top: 8px; font-size: 11px; color: var(--td-text-color-placeholder); display: flex; gap: 6px; flex-wrap: wrap; }
.disclaimer { margin-top: 14px; padding: 9px 11px; border-radius: 8px; background: var(--td-bg-color-secondarycontainer); font-size: 11px; line-height: 1.7; color: var(--td-text-color-placeholder); }
.r-sec { margin-top: 30px; }
.r-sec-head { display: flex; align-items: baseline; gap: 8px; margin-bottom: 12px; }
.r-sec-head .idx { font-size: 10.5px; color: var(--td-brand-color-7); font-weight: 600; letter-spacing: 0.06em; }
.r-sec-head h2 { font-size: 15px; font-weight: 600; margin: 0; }
.r-sec-head .aside { margin-left: auto; font-size: 10.5px; color: var(--td-text-color-placeholder); }
.cov-note { font-size: 12px; line-height: 1.8; color: var(--td-text-color-secondary); }
.finding { border: 1px solid var(--td-component-stroke); border-left: 3px solid var(--td-brand-color); border-radius: 0 9px 9px 0; padding: 13px 14px; margin-bottom: 10px; }
.finding.inferred { border-left-color: var(--td-warning-color); }
.finding-head { display: flex; align-items: center; gap: 7px; flex-wrap: wrap; }
.finding-head h3 { font-size: 13px; font-weight: 600; line-height: 1.5; margin: 0; }
.finding p { margin: 7px 0 0; font-size: 12.5px; line-height: 1.8; color: var(--td-text-color-secondary); }
.refs { display: flex; gap: 5px; margin-top: 9px; flex-wrap: wrap; }
.ref { font-size: 10px; padding: 1px 6px; border-radius: 5px; background: var(--td-bg-color-secondarycontainer); color: var(--td-text-color-secondary); }
blockquote { margin: 0 0 10px; padding: 11px 13px; border-left: 2px solid var(--td-component-stroke); background: var(--td-bg-color-secondarycontainer); border-radius: 0 8px 8px 0; font-size: 12.5px; line-height: 1.75; color: var(--td-text-color-secondary); }
.quote-src { display: block; margin-top: 6px; font-size: 10.5px; color: var(--td-text-color-placeholder); }
.bullet { display: flex; gap: 8px; font-size: 12.5px; line-height: 1.8; color: var(--td-text-color-secondary); margin-bottom: 8px; }
.bullet i { width: 4px; height: 4px; border-radius: 50%; background: var(--td-gray-color-5); margin-top: 9px; flex: none; }
.score-row { display: grid; grid-template-columns: 96px 1fr 92px; gap: 12px; align-items: center; margin-bottom: 11px; font-size: 12px; }
.score-track { height: 6px; border-radius: 99px; background: var(--td-bg-color-secondarycontainer); overflow: hidden; }
.score-track i { display: block; height: 100%; border-radius: 99px; background: linear-gradient(90deg, var(--td-brand-color-3), var(--td-brand-color-6)); }
.score-val { text-align: right; font-size: 11px; color: var(--td-text-color-secondary); font-variant-numeric: tabular-nums; }
.scale-note { margin-top: 4px; font-size: 10.5px; color: var(--td-text-color-placeholder); }
.conclusion { padding: 16px 18px; border-radius: 10px; background: var(--td-brand-color-1); border: 1px solid var(--td-brand-color-2); }
.conclusion p { font-size: 12.5px; line-height: 1.95; color: var(--td-text-color-secondary); margin: 0 0 10px; }
.conclusion p:last-of-type { margin-bottom: 0; }
.conclusion p.lead { font-size: 14px; line-height: 1.9; font-weight: 500; color: var(--td-text-color-primary); }
.conclusion .basis { margin-top: 12px; padding-top: 9px; border-top: 1px dashed var(--td-brand-color-2); font-size: 10.5px; line-height: 1.7; color: var(--td-text-color-placeholder); }
.action { display: flex; gap: 10px; padding: 11px 12px; border: 1px solid var(--td-component-stroke); border-radius: 9px; margin-bottom: 8px; font-size: 12.5px; line-height: 1.7; }
.action .n { width: 19px; height: 19px; flex: none; border-radius: 50%; background: var(--td-brand-color-1); color: var(--td-brand-color-7); display: grid; place-items: center; font-size: 10px; font-weight: 600; margin-top: 1px; }

.ask-rail { width: 320px; flex: none; border-left: 1px solid var(--td-component-stroke); display: flex; flex-direction: column; min-height: 0; background: var(--td-bg-color-secondarycontainer); }
.ask-head { padding: 14px 16px 10px; border-bottom: 1px solid var(--td-component-stroke); }
.ask-head b { font-size: 12.5px; display: flex; align-items: center; gap: 6px; }
.ask-head p { margin: 4px 0 0; font-size: 10.5px; color: var(--td-text-color-placeholder); line-height: 1.6; }
.toc { padding: 10px 16px; border-bottom: 1px solid var(--td-component-stroke); }
.toc a { display: block; font-size: 11.5px; color: var(--td-text-color-secondary); text-decoration: none; padding: 4px 0; }
.toc a:hover { color: var(--td-brand-color-7); }
.chat { flex: 1; overflow-y: auto; padding: 14px 16px; display: flex; flex-direction: column; gap: 12px; }
.msg { font-size: 12px; line-height: 1.75; }
.msg.ai .ai-name { font-size: 10px; color: var(--td-text-color-placeholder); margin-bottom: 4px; }
.msg.ai .ai-body { background: var(--td-bg-color-container); border: 1px solid var(--td-component-stroke); border-radius: 2px 10px 10px 10px; padding: 9px 11px; color: var(--td-text-color-secondary); white-space: pre-wrap; }
.msg.user { align-self: flex-end; max-width: 85%; background: var(--td-brand-color-1); color: var(--td-text-color-primary); padding: 8px 10px; border-radius: 10px 10px 2px 10px; white-space: pre-wrap; }
.ask-input { padding: 10px 12px; border-top: 1px solid var(--td-component-stroke); background: var(--td-bg-color-container); }
.ask-box { display: flex; align-items: flex-end; gap: 6px; border: 1px solid var(--td-component-stroke); border-radius: 10px; padding: 6px 6px 6px 10px; }
.ask-box:focus-within { border-color: var(--td-brand-color); box-shadow: 0 0 0 3px var(--td-brand-color-1); }
.ask-box textarea { flex: 1; min-width: 0; border: 0; resize: none; height: 32px; line-height: 1.6; font-size: 12px; background: transparent; outline: none; color: var(--td-text-color-primary); font-family: inherit; }
.send { width: 26px; height: 26px; flex: none; border: 0; border-radius: 7px; background: var(--td-brand-color); color: #fff; display: grid; place-items: center; cursor: pointer; }
.send[disabled] { opacity: 0.45; pointer-events: none; }
.src-box-wrap { padding: 0 16px; }
.src-box { border: 1px solid var(--td-component-stroke); border-radius: 10px; padding: 11px 12px; margin-top: 10px; background: var(--td-bg-color-container); }
.src-box b { display: block; font-size: 11.5px; margin-bottom: 7px; }
.src-row { display: flex; align-items: center; font-size: 11px; color: var(--td-text-color-secondary); padding: 2px 0; }
.src-row em { margin-left: auto; font-style: normal; color: var(--td-text-color-placeholder); font-size: 10.5px; }

.self-wrap { max-width: 860px; margin: 0 auto; padding: 24px 28px 60px; }
.self-hero { padding: 20px 22px; border-radius: 14px; background: var(--td-brand-color-1); border: 1px solid var(--td-brand-color-2); }
.self-hero .badges { display: flex; align-items: center; gap: 6px; flex-wrap: wrap; margin-bottom: 12px; }
.portrait { font-size: 14.5px; line-height: 1.95; color: var(--td-text-color-primary); margin: 0; }
.portrait :deep(strong) { font-weight: 600; color: var(--td-brand-color-7); }
.self-hero .foot { margin-top: 12px; padding-top: 10px; border-top: 1px dashed var(--td-brand-color-2); font-size: 10.5px; line-height: 1.7; color: var(--td-text-color-placeholder); }
.layer-nav { display: grid; grid-template-columns: repeat(7, 1fr); gap: 6px; margin-top: 14px; }
.layer-chip { border: 1px solid var(--td-component-stroke); border-radius: 9px; padding: 8px 6px; background: var(--td-bg-color-container); text-align: center; text-decoration: none; color: inherit; display: block; }
.layer-chip:hover { border-color: var(--td-brand-color); }
.layer-chip .n { font-size: 9px; color: var(--td-text-color-disabled); }
.layer-chip b { display: block; font-size: 11px; margin-top: 2px; font-weight: 500; color: var(--td-text-color-secondary); white-space: nowrap; }
.layer-chip .cov { display: block; margin-top: 6px; height: 3px; border-radius: 99px; background: var(--td-bg-color-secondarycontainer); overflow: hidden; }
.layer-chip .cov i { display: block; height: 100%; border-radius: 99px; background: var(--td-brand-color); }
.layer-sec { margin-top: 24px; padding-top: 18px; border-top: 1px solid var(--td-component-stroke); scroll-margin-top: 12px; }
.layer-head { display: flex; align-items: flex-start; gap: 10px; }
.layer-no { width: 28px; height: 28px; flex: none; border-radius: 8px; display: grid; place-items: center; background: var(--td-brand-color-1); color: var(--td-brand-color-7); font-size: 11.5px; font-weight: 600; }
.layer-head h2 { font-size: 15px; font-weight: 600; margin: 0; }
.layer-head .q { margin-top: 3px; font-size: 10.5px; color: var(--td-text-color-placeholder); }
.layer-head .cov-tag { margin-left: auto; flex: none; }
.layer-lead { margin: 10px 0 0; font-size: 12.5px; line-height: 1.9; color: var(--td-text-color-secondary); }
.ldim { border: 1px solid var(--td-component-stroke); border-radius: 9px; padding: 10px 12px; margin-top: 8px; }
.ldim-head { display: flex; align-items: center; gap: 7px; flex-wrap: wrap; }
.ldim-head b { font-size: 12.5px; font-weight: 600; }
.ldim-head .src { margin-left: auto; font-size: 10px; color: var(--td-text-color-disabled); }
.ldim p { margin: 5px 0 0; font-size: 12px; line-height: 1.8; color: var(--td-text-color-secondary); }
.ldim.is-blank { background: var(--td-bg-color-secondarycontainer); border-style: dashed; }
.overview-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 9px; margin-top: 10px; }
.ov-card { border: 1px solid var(--td-component-stroke); border-radius: 10px; padding: 12px; }
.ov-card .h { display: flex; align-items: center; gap: 6px; font-size: 12px; font-weight: 600; }
.ov-card p { margin: 6px 0 0; font-size: 12px; line-height: 1.8; color: var(--td-text-color-secondary); }
.ov-card .h a { margin-left: auto; font-size: 10px; font-weight: 400; color: var(--td-text-color-placeholder); text-decoration: none; }
.ov-card .h a:hover { color: var(--td-brand-color); }
.src-row.gate { color: var(--td-brand-color-7); }
.src-row.gate em { color: var(--td-brand-color-7); }
.cross-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 9px; margin-top: 10px; }
.cross { border: 1px solid var(--td-component-stroke); border-radius: 10px; padding: 12px; }
.cross .t { font-size: 12px; font-weight: 600; }
.cross p { margin: 6px 0 0; font-size: 12px; line-height: 1.8; color: var(--td-text-color-secondary); }
.trio { width: 100%; border-collapse: collapse; font-size: 12px; margin-top: 10px; }
.trio th { background: var(--td-bg-color-secondarycontainer); text-align: left; font-size: 10.5px; color: var(--td-text-color-placeholder); font-weight: 500; padding: 8px 10px; border-bottom: 1px solid var(--td-component-stroke); }
.trio td { padding: 9px 10px; border-bottom: 1px solid var(--td-component-stroke); vertical-align: top; line-height: 1.75; color: var(--td-text-color-secondary); }
.trio td b { color: var(--td-text-color-primary); font-weight: 600; }
.gate-list { margin-top: 14px; border: 1px solid var(--td-component-stroke); border-radius: 10px; overflow: hidden; background: var(--td-bg-color-container); }
.gate-row { display: flex; align-items: center; gap: 8px; padding: 9px 12px; border-bottom: 1px solid var(--td-component-stroke); font-size: 12px; }
.gate-row:last-child { border-bottom: 0; }
.gate-row em { margin-left: auto; font-style: normal; font-size: 10.5px; color: var(--td-text-color-placeholder); }

.mask { position: fixed; inset: 0; background: rgba(20, 24, 22, 0.38); display: flex; align-items: center; justify-content: center; z-index: 1200; padding: 24px; }
.modal { width: 560px; max-width: 100%; max-height: 88vh; background: var(--td-bg-color-container); border-radius: 16px; box-shadow: 0 24px 64px rgba(0, 0, 0, 0.24); display: flex; flex-direction: column; overflow: hidden; }
.modal.wide { width: 720px; }
.modal-head { display: flex; align-items: flex-start; gap: 10px; padding: 18px 20px 12px; }
.modal-head h3 { font-size: 15px; font-weight: 600; margin: 0; }
.modal-head p { margin: 4px 0 0; font-size: 11.5px; color: var(--td-text-color-placeholder); line-height: 1.6; }
.modal-head .ins-icon-btn { margin-left: auto; }
.modal-body { padding: 0 20px 18px; overflow-y: auto; }
.modal-foot { display: flex; align-items: center; gap: 8px; padding: 12px 20px; border-top: 1px solid var(--td-component-stroke); background: var(--td-bg-color-secondarycontainer); }
.modal-foot .hint { font-size: 10.5px; color: var(--td-text-color-placeholder); }
.stat-grid { display: grid; grid-template-columns: repeat(5, 1fr); gap: 7px; margin-bottom: 16px; }
.stat-cell { border: 1px solid var(--td-component-stroke); border-radius: 9px; padding: 9px 10px; }
.stat-cell span { font-size: 10px; color: var(--td-text-color-placeholder); }
.stat-cell b { display: block; margin-top: 3px; font-size: 17px; font-weight: 600; font-variant-numeric: tabular-nums; }
.stat-cell.muted b { color: var(--td-text-color-placeholder); }
.rule-list { border: 1px solid var(--td-component-stroke); border-radius: 10px; overflow: hidden; margin-bottom: 14px; }
.rule { display: flex; align-items: center; gap: 10px; padding: 10px 12px; border-bottom: 1px solid var(--td-component-stroke); }
.rule:last-child { border-bottom: 0; }
.rule.clickable { cursor: pointer; }
.rule.clickable:hover { background: var(--td-bg-color-container-hover); }
.radio { width: 15px; height: 15px; border-radius: 50%; border: 1.5px solid var(--td-gray-color-5); display: grid; place-items: center; flex: none; }
.radio.on { border-color: var(--td-brand-color); }
.radio.on::after { content: ''; width: 7px; height: 7px; border-radius: 50%; background: var(--td-brand-color); }
.run-list { border: 1px solid var(--td-component-stroke); border-radius: 10px; overflow: hidden; }
.run-row { display: flex; align-items: center; gap: 10px; padding: 10px 12px; border-bottom: 1px solid var(--td-component-stroke); }
.run-row:last-child { border-bottom: 0; }
.run-copy { flex: 1; min-width: 0; }
.run-copy b { display: block; font-size: 12px; font-weight: 500; }
.run-copy span { display: block; margin-top: 2px; font-size: 10.5px; color: var(--td-text-color-placeholder); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rule-main { flex: 1; }
.rule-main b { display: block; font-size: 12px; font-weight: 500; }
.rule-main span { font-size: 10.5px; color: var(--td-text-color-placeholder); line-height: 1.6; }
.switch { width: 32px; height: 18px; border-radius: 99px; background: var(--td-gray-color-4); position: relative; transition: 0.15s; flex: none; cursor: pointer; }
.switch::after { content: ''; position: absolute; top: 2px; left: 2px; width: 14px; height: 14px; border-radius: 50%; background: #fff; transition: 0.15s; box-shadow: 0 1px 2px rgba(0, 0, 0, 0.2); }
.switch.on { background: var(--td-brand-color); }
.switch.on::after { transform: translateX(14px); }
/* 固定策略：保持「已开启」外观，但不接受点击 */
.switch.fixed { cursor: default; opacity: 0.9; }
.hint-box { display: flex; gap: 8px; padding: 9px 11px; border-radius: 9px; background: var(--td-brand-color-1); color: var(--td-text-color-secondary); font-size: 11px; line-height: 1.7; }
.tabs { display: flex; gap: 2px; padding: 0 20px; border-bottom: 1px solid var(--td-component-stroke); }
.tab { padding: 9px 10px; font-size: 12px; color: var(--td-text-color-secondary); border: 0; border-bottom: 2px solid transparent; margin-bottom: -1px; background: transparent; cursor: pointer; }
.tab.on { color: var(--td-brand-color-7); border-bottom-color: var(--td-brand-color); font-weight: 500; }
.field { margin-bottom: 12px; }
.field label { display: block; font-size: 11px; color: var(--td-text-color-placeholder); margin-bottom: 5px; }
.field input, .field select { width: 100%; border: 1px solid var(--td-component-stroke); border-radius: 8px; padding: 7px 9px; font-size: 12px; background: var(--td-bg-color-container); color: var(--td-text-color-primary); outline: none; box-sizing: border-box; }
.field input:focus { border-color: var(--td-brand-color); }
.tpl-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 8px; }
.tpl { border: 1px solid var(--td-component-stroke); border-radius: 10px; padding: 11px 12px; text-align: left; background: transparent; cursor: pointer; }
.tpl:hover { border-color: var(--td-brand-color); }
.tpl.on { border-color: var(--td-brand-color); background: var(--td-brand-color-1); }
.tpl b { font-size: 12px; font-weight: 600; display: flex; align-items: center; gap: 6px; }
.tpl p { margin: 4px 0 0; font-size: 10.5px; color: var(--td-text-color-placeholder); line-height: 1.6; }
.drop { display: block; margin-top: 4px; border: 1px dashed var(--td-component-stroke); border-radius: 12px; padding: 26px; text-align: center; color: var(--td-text-color-placeholder); font-size: 11.5px; line-height: 1.8; cursor: pointer; }
.drop b { display: block; font-size: 12px; color: var(--td-text-color-secondary); margin-bottom: 2px; }
.code { margin-top: 12px; padding: 10px 12px; border-radius: 9px; background: #fafafa; font-family: ui-monospace, Consolas, monospace; font-size: 10.5px; line-height: 1.8; color: var(--td-text-color-secondary); white-space: pre-wrap; }
.dim-edit { display: grid; grid-template-columns: 1fr 1.6fr 88px 24px; gap: 6px; margin-bottom: 6px; align-items: center; }
.dim-edit input, .dim-edit select { width: 100%; border: 1px solid var(--td-component-stroke); border-radius: 7px; padding: 6px 8px; font-size: 11.5px; background: var(--td-bg-color-container); color: var(--td-text-color-primary); box-sizing: border-box; }
.shield { width: 40px; height: 40px; border-radius: 12px; background: var(--td-brand-color-1); color: var(--td-brand-color-7); display: grid; place-items: center; margin-bottom: 12px; }
.priv-list { display: flex; flex-direction: column; gap: 9px; margin: 12px 0 14px; }
.priv-item { display: flex; gap: 8px; font-size: 11.5px; line-height: 1.7; color: var(--td-text-color-secondary); }
.confirm-line { display: flex; align-items: flex-start; gap: 7px; font-size: 11.5px; color: var(--td-text-color-secondary); padding: 9px 11px; border-radius: 8px; background: var(--td-bg-color-secondarycontainer); cursor: pointer; }
.confirm-line .box { width: 14px; height: 14px; border: 1.5px solid var(--td-gray-color-5); border-radius: 4px; margin-top: 2px; display: grid; place-items: center; background: var(--td-bg-color-container); flex: none; color: #fff; }
.confirm-line .box.on { background: var(--td-brand-color); border-color: var(--td-brand-color); }
/* 导出脱敏：三列词表，窄屏折叠为一列 */
.mask-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 10px; margin: 4px 0 12px; }
.mask-field { display: flex; flex-direction: column; gap: 6px; }
.mask-field > span { display: flex; flex-direction: column; gap: 2px; font-size: 11.5px; font-weight: 600; color: var(--td-text-color-primary); }
.mask-field em { font-style: normal; font-weight: 400; font-size: 10.5px; color: var(--td-text-color-placeholder); }
.mask-field textarea { min-height: 76px; resize: vertical; font-size: 12.5px; line-height: 1.6; border: 1px solid var(--td-component-stroke); border-radius: 8px; padding: 8px 10px; background: var(--td-bg-color-container); color: var(--td-text-color-primary); outline: none; box-sizing: border-box; }
.mask-field textarea:focus { border-color: var(--td-brand-color); }
.mask-hint { margin: 10px 0 0; font-size: 10.5px; line-height: 1.7; color: var(--td-text-color-placeholder); }

@media (max-width: 1100px) {
  .theme-pane { width: 240px; }
  .ask-rail { width: 280px; }
  .overview-grid, .cross-grid { grid-template-columns: 1fr; }
  .layer-nav { grid-template-columns: repeat(4, 1fr); }
  .mask-grid { grid-template-columns: 1fr; }
}
</style>
