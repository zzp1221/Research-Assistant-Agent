package api

import (
	"strings"

	"research-assistant-backend/internal/models"

	"github.com/gin-gonic/gin"
)

// apiError is the structured error contract consumed by the frontend. A code is
// stable for clients, message is human-readable Chinese, remediation tells the
// user what to do next, and trace_id helps correlate with server logs.
type apiError struct {
	Code                string                      `json:"code"`
	Error               string                      `json:"error"`
	Remediation         string                      `json:"remediation,omitempty"`
	MissingRequirements []models.MissingRequirement `json:"missing_requirements,omitempty"`
	TraceID             string                      `json:"trace_id,omitempty"`
	Detail              string                      `json:"detail,omitempty"`
}

// respondError writes a structured API error with remediation guidance.
func respondError(c *gin.Context, status int, code, message, remediation string) {
	payload := apiError{Code: code, Error: message, Remediation: remediation}
	if planID := c.Param("id"); planID != "" {
		payload.TraceID = planID
	}
	if detail := strings.TrimSpace(c.GetString("error_detail")); detail != "" {
		payload.Detail = detail
	}
	c.AbortWithStatusJSON(status, payload)
}

// errorRemediation maps common error codes to actionable guidance.
func errorRemediation(code string) string {
	switch code {
	case "intent_too_long":
		return "把需求拆成更短的分段描述（每段不超过 20000 字符），或先上传文档再引用。"
	case "rate_limited":
		return "等待 Retry-After 指定的秒数后重试，或减少并发计划。"
	case "active_plan_quota_exceeded":
		return "先完成或取消正在运行的计划，再创建新计划。"
	case "plan_paused":
		return "先调用 /api/plans/{id}/resume 恢复计划，再执行。"
	case "plan_already_terminal":
		return "该计划已经结束；请基于该计划创建新计划，或使用从节点运行。"
	case "plan_already_running":
		return "等待当前执行结束，或取消后重新启动。"
	case "approval_expired":
		return "审批已过期；请重新创建计划或从节点运行以重新申请权限。"
	case "quota_exceeded":
		return "删除不再需要的上传文件，或提高 UPLOAD_USER_QUOTA_MB 配额。"
	case "malware_detected":
		return "文件未通过恶意软件扫描；请确认来源后重新上传干净的原始文件。"
	case "scan_failed":
		return "病毒扫描服务不可用；稍后重试或联系管理员检查 CLAMAV_ADDR。"
	case "session_has_active_plan":
		return "先取消该会话中正在运行的计划，再删除会话。"
	case "network_control_failed":
		return "检查 docker-sandbox 的 SANDBOX_ALLOW_NETWORK 与网络连通性后重试。"
	case "resource_limit":
		return "减小镜像/数据规模，或调整 RuntimePolicy 的硬限制后重试。"
	default:
		return "请稍后重试；若持续失败，请导出计划（/api/plans/{id}/export）并联系管理员。"
	}
}
