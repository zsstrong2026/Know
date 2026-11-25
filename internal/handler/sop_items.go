// sop_items.go 是 SOP 事项（工作项）的 HTTP 端点：CRUD、状态迁移、批量迁移、
// 附件、澄清与活动流。
//
// 与项目端点同一套包络与错误助手（sopFail/sopAbort/sopBindJSON/sopTenantID/sopUserID）。
package handler

import (
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/application/service/sop"
	"github.com/Tencent/WeKnora/internal/types"
)

// sopItemPayload 新建/修改事项的请求体。指针字段区分「未传」与「置空」。
type sopItemPayload struct {
	Title       *string         `json:"title"`
	Description *string         `json:"description"`
	Kind        *string         `json:"kind"`
	Priority    *string         `json:"priority"`
	Source      *string         `json:"source"`
	Proposer    *string         `json:"proposer"`
	Owner       *string         `json:"owner"`
	Tags        *[]string       `json:"tags"`
	TemplateID  *string         `json:"template_id"`
	KBID        *string         `json:"kb_id"`
	Outcome     *map[string]any `json:"outcome"`
	DomainData  *map[string]any `json:"domain_data"`
	// IsBig/ParentID 只在创建时生效（创建态字段，后续修改走拆分/标记专有接口）。
	IsBig    bool   `json:"is_big"`
	ParentID string `json:"parent_id"`
}

// CreateItem 新建事项（项目下）：编号由服务端分配。
func (h *SOPHandler) CreateItem(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 {
		return
	}
	if h.items == nil {
		sopAbort(c, sopClientf("事项服务未就绪"))
		return
	}
	var req sopItemPayload
	if !sopBindJSON(c, &req) {
		return
	}
	if req.Title == nil {
		sopAbort(c, sop.ErrSopItemTitleRequired)
		return
	}
	in := sop.CreateItemInput{Title: *req.Title, IsBig: req.IsBig, ParentID: req.ParentID}
	applyItemPayload(&in, req)
	it, err := h.items.Create(c.Request.Context(), tenantID, sopUserID(c), c.Param("id"), in)
	if err != nil {
		sopFail(c, err, "项目不存在")
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": it})
}

// applyItemPayload 把请求体里的可选字段摊进创建入参。
func applyItemPayload(in *sop.CreateItemInput, req sopItemPayload) {
	str := func(dst *string, src *string) {
		if src != nil {
			*dst = *src
		}
	}
	str(&in.Description, req.Description)
	str(&in.Kind, req.Kind)
	str(&in.Priority, req.Priority)
	str(&in.Source, req.Source)
	str(&in.Proposer, req.Proposer)
	str(&in.Owner, req.Owner)
	str(&in.TemplateID, req.TemplateID)
	str(&in.KBID, req.KBID)
	if req.Tags != nil {
		in.Tags = *req.Tags
	}
}

// ListItems 列表（带派生执行情况/进度/当前环节）。
func (h *SOPHandler) ListItems(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 {
		return
	}
	if h.items == nil {
		sopAbort(c, sopClientf("事项服务未就绪"))
		return
	}
	f := types.SopItemFilter{
		Q:        strings.TrimSpace(c.Query("keyword")),
		Status:   c.Query("status"),
		Owner:    c.Query("owner"),
		Source:   c.Query("source"),
		Kind:     c.Query("kind"),
		Exec:     c.Query("exec_state"),
		Page:     atoiOr(c.Query("page"), 0),
		PageSize: atoiOr(c.Query("page_size"), 0),
	}
	if parent, ok := c.GetQuery("parent_id"); ok {
		f.ParentID = &parent
	}
	list, err := h.items.ListWithDerived(c.Request.Context(), tenantID, c.Param("id"), f)
	if err != nil {
		sopFail(c, err, "项目不存在")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list})
}

// atoiOr 解析整数，失败用默认值（分页参数容错）。
func atoiOr(s string, def int) int {
	n := 0
	if s == "" {
		return def
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return def
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// GetItem 事项详情。
func (h *SOPHandler) GetItem(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.items == nil {
		return
	}
	it, err := h.items.Get(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		sopFail(c, err, "事项不存在")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": it})
}

// UpdateItem 修改可编辑字段（状态不在这里）。
func (h *SOPHandler) UpdateItem(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 {
		return
	}
	if h.items == nil {
		sopAbort(c, sopClientf("事项服务未就绪"))
		return
	}
	var req sopItemPayload
	if !sopBindJSON(c, &req) {
		return
	}
	it, err := h.items.Update(c.Request.Context(), tenantID, sopUserID(c), c.Param("id"), sop.UpdateItemInput{
		Title: req.Title, Description: req.Description, Kind: req.Kind, Priority: req.Priority,
		Source: req.Source, Proposer: req.Proposer, Owner: req.Owner, Tags: req.Tags,
		TemplateID: req.TemplateID, KBID: req.KBID, Outcome: req.Outcome, DomainData: req.DomainData,
	})
	if err != nil {
		sopFail(c, err, "事项不存在")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": it})
}

// UpdateItemStatus 单条状态迁移。
func (h *SOPHandler) UpdateItemStatus(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 {
		return
	}
	if h.items == nil {
		sopAbort(c, sopClientf("事项服务未就绪"))
		return
	}
	var req struct {
		Status string `json:"status"`
	}
	if !sopBindJSON(c, &req) {
		return
	}
	if strings.TrimSpace(req.Status) == "" {
		sopAbort(c, sopClientf("请选择目标状态"))
		return
	}
	it, err := h.items.Get(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		sopFail(c, err, "事项不存在")
		return
	}
	if err := h.items.UpdateStatus(c.Request.Context(), tenantID, it.ID, req.Status); err != nil {
		sopAbort(c, err)
		return
	}
	it.Status = req.Status
	c.JSON(http.StatusOK, gin.H{"success": true, "data": it})
}

// BulkUpdateItemStatus 批量迁移：执行中的事项跳过并在 skipped 里回传。
func (h *SOPHandler) BulkUpdateItemStatus(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 {
		return
	}
	if h.items == nil {
		sopAbort(c, sopClientf("事项服务未就绪"))
		return
	}
	var req struct {
		IDs    []string `json:"ids"`
		Status string   `json:"status"`
	}
	if !sopBindJSON(c, &req) {
		return
	}
	if len(req.IDs) == 0 {
		sopAbort(c, sopClientf("请选择要处理的事项"))
		return
	}
	res, err := h.items.BulkUpdateStatus(c.Request.Context(), tenantID, sopUserID(c), req.IDs, req.Status)
	if err != nil {
		sopAbort(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": res})
}

// DeleteItem 软删事项。
func (h *SOPHandler) DeleteItem(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.items == nil {
		return
	}
	if err := h.items.Delete(c.Request.Context(), tenantID, sopUserID(c), c.Param("id")); err != nil {
		sopFail(c, err, "事项不存在")
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"deleted": true}})
}

// ---- 附件 ----

// UploadItemAttachment 上传附件（multipart，字段名 file）：列表不回传正文。
func (h *SOPHandler) UploadItemAttachment(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.items == nil {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<20)
	fileHeader, err := c.FormFile("file")
	if err != nil {
		sopAbort(c, sopClientf("请选择要上传的文件"))
		return
	}
	f, err := fileHeader.Open()
	if err != nil {
		sopAbort(c, sopClientf("读取上传文件失败"))
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		sopAbort(c, sopClientf("读取上传文件失败"))
		return
	}
	a, err := h.items.AddAttachment(c.Request.Context(), tenantID, sopUserID(c),
		c.Param("id"), fileHeader.Filename, fileHeader.Header.Get("Content-Type"), data)
	if err != nil {
		sopAbort(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": a})
}

// ListItemAttachments 列附件（不含正文）。
func (h *SOPHandler) ListItemAttachments(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.items == nil {
		return
	}
	list, err := h.items.ListAttachments(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		sopFail(c, err, "事项不存在")
		return
	}
	for i := range list {
		list[i].Data = nil
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list})
}

// ---- 澄清 ----

// ListItemClarifications 列澄清。
func (h *SOPHandler) ListItemClarifications(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.items == nil {
		return
	}
	list, err := h.items.ListClarifications(c.Request.Context(), tenantID, c.Param("id"))
	if err != nil {
		sopFail(c, err, "事项不存在")
		return
	}
	if list == nil {
		list = []types.SopItemClarification{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list})
}

// CreateItemClarification 新增待澄清问题（source 缺省 ai）。
func (h *SOPHandler) CreateItemClarification(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.items == nil {
		return
	}
	var req struct {
		Question string `json:"question"`
		Source   string `json:"source"`
	}
	if !sopBindJSON(c, &req) {
		return
	}
	cl, err := h.items.AddClarification(c.Request.Context(), tenantID, c.Param("id"), req.Question, req.Source)
	if err != nil {
		sopAbort(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"success": true, "data": cl})
}

// AnswerItemClarification 答复澄清（open → answered 单向）。
func (h *SOPHandler) AnswerItemClarification(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.items == nil {
		return
	}
	var req struct {
		Answer string `json:"answer"`
	}
	if !sopBindJSON(c, &req) {
		return
	}
	err := h.items.AnswerClarification(c.Request.Context(), tenantID, c.Param("id"), c.Param("cid"), req.Answer)
	if err != nil {
		sopAbort(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"answered": true}})
}

// ListItemActivities 活动流（?kind=business|audit）。
func (h *SOPHandler) ListItemActivities(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 || h.items == nil {
		return
	}
	list, err := h.items.ListActivities(c.Request.Context(), tenantID, c.Param("id"), c.Query("kind"))
	if err != nil {
		sopFail(c, err, "事项不存在")
		return
	}
	if list == nil {
		list = []types.SopItemActivity{}
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list})
}

// BulkLaunchItems 批量触发：一次请求处理多条事项，父项展开为子项、运行中跳过。
//
// 逐条建运行在本机能跑，但几十条会串成几十次 HTTP 往返；这里收成一次请求，
// 由服务层展开与跳过判定，返回 started/skipped/failed 三组 id。
func (h *SOPHandler) BulkLaunchItems(c *gin.Context) {
	tenantID := sopTenantID(c)
	if tenantID == 0 {
		return
	}
	if h.items == nil || h.runs2 == nil {
		sopAbort(c, sopClientf("事项或运行服务未就绪"))
		return
	}
	var req struct {
		IDs []string `json:"ids"`
	}
	if !sopBindJSON(c, &req) {
		return
	}
	if len(req.IDs) == 0 {
		sopAbort(c, sopClientf("请选择要触发的事项"))
		return
	}
	targets, skipped, err := h.items.ExpandLaunchTargets(c.Request.Context(), tenantID, req.IDs)
	if err != nil {
		sopAbort(c, err)
		return
	}
	result := &sop.LaunchResult{Started: []string{}, Skipped: skipped, Failed: []string{}}
	for _, t := range targets {
		_, err := h.runs2.Create(c.Request.Context(), tenantID, sopUserID(c), sop.CreateRunInput{
			TemplateID: t.TemplateID, ItemID: t.ItemID,
		})
		if err != nil {
			result.Failed = append(result.Failed, t.ItemID)
			continue
		}
		result.Started = append(result.Started, t.ItemID)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}
