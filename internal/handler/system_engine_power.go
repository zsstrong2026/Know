package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// 解析引擎宿主机控制器（tools/engine_supervisor.py）的代理接口。
// 后端容器不直接管理宿主机进程，统一转发到本机控制器，由其完成启停与空闲自动停止。

const defaultEngineSupervisorURL = "http://host.docker.internal:8098"

var enginePowerWhitelist = map[string]struct{}{
	"mineru":       {},
	"paddleocr_vl": {},
}

func engineSupervisorURL() string {
	if v := strings.TrimSpace(os.Getenv("ENGINE_SUPERVISOR_URL")); v != "" {
		return strings.TrimRight(v, "/")
	}
	return defaultEngineSupervisorURL
}

// callEngineSupervisor 调用控制器接口，返回解析后的 JSON 与是否可达
func callEngineSupervisor(method, path string, body any) (map[string]any, bool) {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, false
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, engineSupervisorURL()+path, reader)
	if err != nil {
		return nil, false
	}
	req.Header.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(os.Getenv("ENGINE_SUPERVISOR_TOKEN")); token != "" {
		req.Header.Set("X-Engine-Supervisor-Token", token)
	}
	client := &http.Client{Timeout: 4 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	var payload map[string]any
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = json.Unmarshal(raw, &payload)
	if payload == nil {
		payload = map[string]any{}
	}
	return payload, true
}

// GetParserEnginePower 返回自建引擎的运行状态与空闲配置。
// @Summary      查看解析引擎运行状态
// @Tags         系统
// @Produce      json
// @Success      200
// @Router       /system/parser-engines/power [get]
func (h *SystemHandler) GetParserEnginePower(c *gin.Context) {
	payload, ok := callEngineSupervisor(http.MethodGet, "/status", nil)
	if !ok {
		c.JSON(200, gin.H{
			"code": 0,
			"msg":  "success",
			"data": gin.H{"supervisor_available": false, "engines": gin.H{}},
		})
		return
	}
	payload["supervisor_available"] = true
	c.JSON(200, gin.H{"code": 0, "msg": "success", "data": payload})
}

// SetParserEnginePower 启停指定自建引擎。
// @Summary      启停解析引擎
// @Tags         系统
// @Accept       json
// @Produce      json
// @Param        body  body  object{engine string,action string} true "引擎与动作"
// @Success      200
// @Router       /system/parser-engines/power [post]
func (h *SystemHandler) SetParserEnginePower(c *gin.Context) {
	var req struct {
		Engine string `json:"engine" binding:"required"`
		Action string `json:"action" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"code": 1, "msg": "请求体格式错误"})
		return
	}
	engine := strings.TrimSpace(req.Engine)
	action := strings.ToLower(strings.TrimSpace(req.Action))
	if _, ok := enginePowerWhitelist[engine]; !ok {
		c.JSON(400, gin.H{"code": 1, "msg": "不支持的引擎: " + engine})
		return
	}
	if action != "start" && action != "stop" {
		c.JSON(400, gin.H{"code": 1, "msg": "不支持的动作: " + action})
		return
	}
	payload, ok := callEngineSupervisor(http.MethodPost, "/engines/"+engine+"/"+action, nil)
	if !ok {
		c.JSON(200, gin.H{"code": 1, "msg": "引擎管理服务未启动，请先在宿主机运行 tools/engine_supervisor.py"})
		return
	}
	c.JSON(200, gin.H{"code": 0, "msg": "success", "data": payload})
}

// UpdateParserEnginePowerConfig 更新空闲自动停止时长（秒，0 表示关闭）。
// @Summary      更新解析引擎空闲停止配置
// @Tags         系统
// @Accept       json
// @Produce      json
// @Param        body  body  object{idle_timeout_sec int} true "空闲时长（秒）"
// @Success      200
// @Router       /system/parser-engines/power/config [put]
func (h *SystemHandler) UpdateParserEnginePowerConfig(c *gin.Context) {
	var req struct {
		IdleTimeoutSec int `json:"idle_timeout_sec"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"code": 1, "msg": "请求体格式错误"})
		return
	}
	if req.IdleTimeoutSec < 0 || req.IdleTimeoutSec > 7*24*3600 {
		c.JSON(400, gin.H{"code": 1, "msg": "空闲时长超出范围"})
		return
	}
	payload, ok := callEngineSupervisor(http.MethodPut, "/config", gin.H{"idle_timeout_sec": req.IdleTimeoutSec})
	if !ok {
		c.JSON(200, gin.H{"code": 1, "msg": "引擎管理服务未启动"})
		return
	}
	c.JSON(200, gin.H{"code": 0, "msg": "success", "data": payload})
}
