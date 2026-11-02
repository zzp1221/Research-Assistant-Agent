package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"research-assistant-backend/internal/models"
)

// Enabled reports whether memory writes/injection are enabled. MEMORY_ENABLED=false
// is the documented rollback switch.
func Enabled() bool {
	raw := strings.TrimSpace(os.Getenv("MEMORY_ENABLED"))
	if raw == "" {
		return true
	}
	return !(strings.EqualFold(raw, "false") || raw == "0" || strings.EqualFold(raw, "off"))
}

// Mode returns on | shadow | off.
func Mode() string {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv("MEMORY_MODE")))
	if raw == "" {
		return "on"
	}
	return raw
}

// InjectionEnabled is true only when memory is fully on (shadow retrieves but
// does not inject).
func InjectionEnabled() bool {
	return Enabled() && Mode() != "shadow" && Mode() != "off"
}

// RecordPlan deterministically turns a PlanGraph into episode/fact/constraint/
// negative memories. It is idempotent: the record ids are stable per plan.
func RecordPlan(ctx context.Context, store Store, plan *models.PlanGraph) error {
	if store == nil || plan == nil || strings.TrimSpace(plan.OwnerID) == "" {
		return nil
	}
	now := time.Now().UTC()
	scope := Scope{OwnerID: plan.OwnerID, ProjectID: plan.ProjectID, SessionID: plan.SessionID, PlanID: plan.ID}
	outcome := string(plan.Outcome)
	if outcome == "" {
		outcome = "pending"
	}
	importance := 6
	switch plan.Outcome {
	case models.OutcomeFailed, models.OutcomeBlocked:
		importance = 9
	case models.OutcomeNeedsReview:
		importance = 8
	}
	title := strings.TrimSpace(plan.Title)
	if title == "" {
		title = truncateRunes(strings.TrimSpace(plan.UserIntent), 60)
	}
	title = truncateRunes(title, 120)
	var builder strings.Builder
	fmt.Fprintf(&builder, "目标：%s\n", truncateRunes(strings.TrimSpace(plan.UserIntent), 300))
	fmt.Fprintf(&builder, "意图类型：%s；计划形状：%s；节点数：%d\n", plan.IntentType, plan.Meta.GeneralPlanShape, len(plan.Nodes))
	fmt.Fprintf(&builder, "结果：%s", outcome)
	if strings.TrimSpace(plan.OutcomeReason) != "" {
		fmt.Fprintf(&builder, "；原因：%s", truncateRunes(plan.OutcomeReason, 200))
	}
	builder.WriteString("\n")
	failed := make([]string, 0)
	for _, node := range plan.Nodes {
		if node == nil || node.Status != models.StatusFailed {
			continue
		}
		failed = append(failed, fmt.Sprintf("%s: %s", node.Name, truncateRunes(node.Error, 200)))
	}
	if len(failed) > 0 {
		builder.WriteString("关键失败任务：\n")
		for _, line := range failed {
			if len([]rune(builder.String())) > 650 {
				break
			}
			builder.WriteString("- ")
			builder.WriteString(line)
			builder.WriteString("\n")
		}
	}
	if len(plan.MissingRequirements) > 0 {
		builder.WriteString("缺失要求：")
		for i, missing := range plan.MissingRequirements {
			if i >= 5 {
				break
			}
			if i > 0 {
				builder.WriteString("；")
			}
			builder.WriteString(fmt.Sprintf("%s/%s %s", missing.Kind, missing.Name, missing.Remediation))
		}
		builder.WriteString("\n")
	}
	if plan.EnvironmentManifest != nil {
		env := plan.EnvironmentManifest
		builder.WriteString(fmt.Sprintf("环境：provider=%s image=%s python=%s gpu=%s network=%s\n", env.SandboxProvider, env.Image, env.PythonVersion, env.GPU, env.NetworkMode))
	}
	builder.WriteString(fmt.Sprintf("成本：tokens=%d/%d calls=%d sandbox_seconds=%.1f", plan.Usage.LLMPromptTokens, plan.Usage.LLMCompletionTokens, plan.Usage.LLMCalls, plan.Usage.SandboxSeconds))

	episode := Record{
		ID: "mem-plan-" + plan.ID, Kind: KindEpisode, Scope: scope, Title: title,
		Content: truncateRunes(builder.String(), 800), Tags: []string{"plan", plan.IntentType},
		Entities:   []Entity{{Type: "plan", Value: plan.ID}},
		Source:     Source{Type: "plan", Ref: plan.ID, CreatedAt: now},
		Evidence:   Evidence{Class: "execution_derived", Confidence: 0.95},
		Validity:   Validity{ValidFrom: planTime(plan), RecordedAt: now},
		Importance: importance, Confidence: 0.95, Trust: TrustVerified, Sensitivity: "private", Status: StatusActive,
	}
	if _, err := store.Put(ctx, episode); err != nil {
		return err
	}
	if plan.Context != nil && plan.Context.Paper != nil {
		paper := plan.Context.Paper
		fact := Record{
			ID: "mem-paper-" + plan.ID, Kind: KindFact, Scope: scope,
			Title:   truncateRunes("论文："+paper.Title, 120),
			Content: fmt.Sprintf("标题：%s；作者：%s；年份：%d；会议：%s；DOI：%s；arXiv：%s；仓库：%s", paper.Title, strings.Join(paper.Authors, ", "), paper.Year, paper.Venue, paper.DOI, paper.ArxivID, paper.RepoURL),
			Tags:    []string{"paper", "metadata"}, Entities: []Entity{{Type: "paper", Value: paper.Title}, {Type: "paper", Value: "doi:" + paper.DOI}, {Type: "paper", Value: paper.ArxivID}},
			Source:   Source{Type: "plan", Ref: plan.ID, CreatedAt: now},
			Evidence: Evidence{Class: "paper_derived", Confidence: paper.Confidence}, Validity: Validity{ValidFrom: planTime(plan), RecordedAt: now},
			Importance: 8, Confidence: 0.95, Trust: TrustVerified, Sensitivity: "private", Status: StatusActive,
		}
		if _, err := store.Put(ctx, fact); err != nil {
			return err
		}
	}
	if plan.EnvironmentManifest != nil {
		env := plan.EnvironmentManifest
		constraint := Record{
			ID: "mem-env-" + plan.ID, Kind: KindConstraint, Scope: scope,
			Title:   "环境约束：" + plan.ID,
			Content: fmt.Sprintf("provider=%s image=%s digest=%s python=%s gpu=%s network=%s status=%s", env.SandboxProvider, env.Image, env.ImageDigest, env.PythonVersion, env.GPU, env.NetworkMode, env.Status),
			Tags:    []string{"environment"}, Entities: []Entity{{Type: "env", Value: env.Image}},
			Source:   Source{Type: "plan", Ref: plan.ID, CreatedAt: now},
			Evidence: Evidence{Class: "execution_derived", Confidence: 0.9}, Validity: Validity{ValidFrom: planTime(plan), RecordedAt: now},
			Importance: 7, Confidence: 0.9, Trust: TrustVerified, Sensitivity: "private", Status: StatusActive,
		}
		if _, err := store.Put(ctx, constraint); err != nil {
			return err
		}
	}
	if len(plan.MissingRequirements) > 0 {
		missing := Record{
			ID: "mem-missing-" + plan.ID, Kind: KindConstraint, Scope: scope,
			Title:   "缺失要求：" + plan.ID,
			Content: missingText(plan.MissingRequirements), Tags: []string{"missing-requirements"},
			Source:   Source{Type: "plan", Ref: plan.ID, CreatedAt: now},
			Evidence: Evidence{Class: "execution_derived", Confidence: 0.9}, Validity: Validity{ValidFrom: planTime(plan), RecordedAt: now},
			Importance: 7, Confidence: 0.9, Trust: TrustVerified, Sensitivity: "private", Status: StatusActive,
		}
		if _, err := store.Put(ctx, missing); err != nil {
			return err
		}
	}
	if plan.Outcome != "" && plan.Outcome != models.OutcomeCompleted {
		negative := Record{
			ID: "mem-negative-" + plan.ID, Kind: KindNegative, Scope: scope,
			Title:   "负结果：" + title,
			Content: fmt.Sprintf("计划 %s 结果 %s：%s", plan.ID, plan.Outcome, truncateRunes(plan.OutcomeReason, 300)),
			Tags:    []string{"negative", plan.IntentType}, Entities: []Entity{{Type: "plan", Value: plan.ID}},
			Source:   Source{Type: "plan", Ref: plan.ID, CreatedAt: now},
			Evidence: Evidence{Class: "execution_derived", Confidence: 0.9}, Validity: Validity{ValidFrom: planTime(plan), RecordedAt: now},
			Importance: 8, Confidence: 0.9, Trust: TrustVerified, Sensitivity: "private", Status: StatusActive,
		}
		if _, err := store.Put(ctx, negative); err != nil {
			return err
		}
	}
	return nil
}

func planTime(plan *models.PlanGraph) *time.Time {
	if plan == nil {
		return nil
	}
	if plan.Usage.FinishedAt != nil {
		return plan.Usage.FinishedAt
	}
	if !plan.CreatedAt.IsZero() {
		return &plan.CreatedAt
	}
	return nil
}

func missingText(items []models.MissingRequirement) string {
	var builder strings.Builder
	for i, item := range items {
		if i >= 5 {
			break
		}
		if i > 0 {
			builder.WriteString("；")
		}
		builder.WriteString(fmt.Sprintf("%s/%s %s", item.Kind, item.Name, item.Remediation))
	}
	return truncateRunes(builder.String(), 500)
}

// RecordTask writes a task episode and, when a retry succeeded, a procedure.
func RecordTask(ctx context.Context, store Store, plan *models.PlanGraph, task *models.TaskNode, result *models.TaskExecutionResult) error {
	if store == nil || plan == nil || task == nil || strings.TrimSpace(plan.OwnerID) == "" {
		return nil
	}
	if result != nil {
		for _, artifact := range result.Artifacts {
			if artifact.Key != "research_trial_ledger" || strings.TrimSpace(artifact.Value) == "" {
				continue
			}
			var ledger models.ResearchTrialLedger
			if err := json.Unmarshal([]byte(artifact.Value), &ledger); err == nil {
				_ = RecordTrial(ctx, store, plan, ledger)
			}
		}
	}
	now := time.Now().UTC()
	scope := Scope{OwnerID: plan.OwnerID, ProjectID: plan.ProjectID, SessionID: plan.SessionID, PlanID: plan.ID, AgentID: task.AssignedTo}
	status := task.Status
	if result != nil && result.Status != "" {
		status = result.Status
	}
	if status == models.StatusFailed {
		content := fmt.Sprintf("任务：%s；类型：%s；结果：失败；错误：%s", task.Name, task.Type, truncateRunes(task.Error, 300))
		rec := Record{
			ID: "mem-task-" + plan.ID + "-" + task.ID, Kind: KindEpisode, Scope: scope,
			Title: truncateRunes("任务失败："+task.Name, 120), Content: truncateRunes(content, 700), Tags: []string{"task", "failure"},
			Source:   Source{Type: "task", Ref: task.ID, CreatedAt: now},
			Evidence: Evidence{Class: "execution_derived", Confidence: 0.9}, Validity: Validity{ValidFrom: taskTime(task), RecordedAt: now},
			Importance: 7, Confidence: 0.9, Trust: TrustVerified, Sensitivity: "private", Status: StatusActive,
		}
		return putIgnoreDuplicate(ctx, store, rec)
	}
	if status == models.StatusCompleted && task.RunCount > 1 {
		trigger := "task retry"
		if task.Metadata != nil {
			if value, ok := task.Metadata["previous_error"].(string); ok && strings.TrimSpace(value) != "" {
				trigger = truncateRunes(value, 120)
			}
		}
		proc := Record{
			ID: "mem-proc-" + plan.ID + "-" + task.ID, Kind: KindProcedure, Scope: scope,
			Title:   truncateRunes("修复经验："+task.Name, 120),
			Content: fmt.Sprintf("触发：%s；动作：重试/修复后成功；任务：%s；证据：task=%s", trigger, task.Name, task.ID),
			Tags:    []string{"procedure", "repair"}, Entities: []Entity{{Type: "task", Value: task.ID}},
			Source:   Source{Type: "task", Ref: task.ID, CreatedAt: now},
			Evidence: Evidence{Class: "execution_derived", Confidence: 0.8}, Validity: Validity{ValidFrom: taskTime(task), RecordedAt: now},
			Importance: 7, Confidence: 0.8, Trust: TrustVerified, Sensitivity: "private", Status: StatusActive,
		}
		return putIgnoreDuplicate(ctx, store, proc)
	}
	return nil
}

func taskTime(task *models.TaskNode) *time.Time {
	if task == nil {
		return nil
	}
	if task.FinishedAt != nil {
		return task.FinishedAt
	}
	if !task.CreatedAt.IsZero() {
		return &task.CreatedAt
	}
	return nil
}

func putIgnoreDuplicate(ctx context.Context, store Store, rec Record) error {
	if _, err := store.Put(ctx, rec); err != nil {
		if strings.Contains(err.Error(), "duplicate") {
			return nil
		}
		return err
	}
	return nil
}
