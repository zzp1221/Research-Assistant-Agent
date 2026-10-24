package planner

import (
	"testing"

	"research-assistant-backend/internal/models"
)

func TestTaskRegistryCoversPlannerTemplates(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")
	uploaded := []map[string]any{{
		"id": "upload-1", "name": "reviews.csv", "storage_path": "/tmp/reviews.csv", "text_excerpt": "review,label",
	}}
	cases := []struct {
		name   string
		intent models.IntentContext
	}{
		{
			name: "code execution plot",
			intent: models.IntentContext{
				RawIntent:  "帮我画一个正弦函数和余弦函数的对比图",
				IntentType: "Code_Execution",
				Entities:   map[string]any{"needs_plot": true},
			},
		},
		{
			name: "framework plot",
			intent: models.IntentContext{
				RawIntent:  "对比 LangChain 和 LlamaIndex 的适配性并画图",
				IntentType: "Framework_Evaluation",
				Entities: map[string]any{
					"frameworks": []string{"langchain", "llamaindex"},
					"needs_plot": true,
				},
			},
		},
		{
			name: "framework benchmark plot",
			intent: models.IntentContext{
				RawIntent:  "对比 LangChain 和 LlamaIndex 的响应时间并画柱状图",
				IntentType: "Framework_Evaluation",
				Entities: map[string]any{
					"frameworks":      []string{"langchain", "llamaindex"},
					"needs_plot":      true,
					"needs_benchmark": true,
				},
			},
		},
		{
			name: "paper reproduction plot",
			intent: models.IntentContext{
				RawIntent:  "复现 Transformer 并画出结果",
				IntentType: "Paper_Reproduction",
				Entities: map[string]any{
					"paper_title": "Transformer",
					"needs_plot":  true,
				},
				Constraints: map[string]any{"reproduction_mode": "smoke"},
			},
		},
		{
			name: "auto research",
			intent: models.IntentContext{
				RawIntent:  "用 https://github.com/example/research-repo 做 AutoResearch，最多 2 次实验",
				IntentType: "AutoResearch",
				Entities: map[string]any{
					"needs_autoresearch": true,
					"preferred_repo_url": "https://github.com/example/research-repo",
				},
			},
		},
		{
			name: "custom benchmark",
			intent: models.IntentContext{
				RawIntent:  "用 https://github.com/example/research-repo 跑 benchmark，最多 2 条样本",
				IntentType: "Custom_Benchmark",
				Entities: map[string]any{
					"needs_custom_benchmark": true,
					"preferred_repo_url":     "https://github.com/example/research-repo",
					"uploaded_files":         uploaded,
				},
			},
		},
		{
			name: "dataset experiment",
			intent: models.IntentContext{
				RawIntent:  "上传数据自动比较方法和参数",
				IntentType: "AutoResearch",
				Entities: map[string]any{
					"needs_autoresearch":     true,
					"needs_dataset_research": true,
					"uploaded_files":         uploaded,
				},
			},
		},
		{
			name: "general summary",
			intent: models.IntentContext{
				RawIntent:  "分析一下这篇论文的主要创新点和局限性",
				IntentType: "General",
				Entities:   map[string]any{},
			},
		},
		{
			name: "general process",
			intent: models.IntentContext{
				RawIntent:  "帮我处理一下这个请求",
				IntentType: "General",
				Entities:   map[string]any{},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := NewPlanner().BuildPlan(t.Context(), tc.intent)
			if err != nil {
				t.Fatalf("BuildPlan() error = %v", err)
			}
			for _, node := range plan.Nodes {
				if err := validateTaskNode(node); err != nil {
					t.Fatalf("node %q (%s) failed registry validation: %v", node.Name, node.Type, err)
				}
			}
		})
	}
}

func TestTaskRegistryRejectsUnregisteredType(t *testing.T) {
	err := validateTaskNode(&models.TaskNode{Type: "not_a_real_task", AssignedTo: "data_agent"})
	if err == nil {
		t.Fatal("unregistered task type was accepted")
	}
}

func TestTaskRegistryRejectsDeadTypes(t *testing.T) {
	for _, taskType := range []string{"paper_context", "framework_prepare_left", "framework_run_right"} {
		err := validateTaskNode(&models.TaskNode{Type: taskType, AssignedTo: "librarian_agent"})
		if err == nil {
			t.Fatalf("dead task type %q was accepted", taskType)
		}
	}
}

func TestTaskRegistryPlotTypesRequireCoderAndImage(t *testing.T) {
	for _, taskType := range []string{"render_plot", "result_visualization"} {
		spec, ok := lookupTaskTypeSpec(taskType)
		if !ok {
			t.Fatalf("plot type %q is not registered", taskType)
		}
		if spec.ExecutionKind != ExecutionReal {
			t.Fatalf("plot type %q kind=%s, want real_execution", taskType, spec.ExecutionKind)
		}
		if !spec.allowsAgent("coder_agent") {
			t.Fatalf("plot type %q does not allow coder_agent", taskType)
		}
		if err := validateTaskNode(&models.TaskNode{
			Type:            taskType,
			AssignedTo:      "data_agent",
			OutputArtifacts: []string{"plot_image"},
		}); err == nil {
			t.Fatalf("plot type %q assigned to data_agent was accepted", taskType)
		}
		if err := validateTaskNode(&models.TaskNode{
			Type:       taskType,
			AssignedTo: "coder_agent",
		}); err == nil {
			t.Fatalf("plot type %q without its image artifact was accepted", taskType)
		}
	}
}

func TestTaskRegistryMarksLLMOnlyNodes(t *testing.T) {
	for _, taskType := range []string{"verify_result", "paper_compare", "framework_report", "framework_research", "paper_parse", "general_research", "general_synthesis"} {
		spec, ok := lookupTaskTypeSpec(taskType)
		if !ok {
			t.Fatalf("task type %q is not registered", taskType)
		}
		if spec.ExecutionKind != ExecutionLLM {
			t.Fatalf("task type %q kind=%s, want llm_generation", taskType, spec.ExecutionKind)
		}
	}
}

func TestTaskRegistryGeneralProcessUsesGeneralAgent(t *testing.T) {
	spec, ok := lookupTaskTypeSpec("general_process")
	if !ok {
		t.Fatal("general_process is not registered")
	}
	if !spec.allowsAgent("general_agent") || spec.allowsAgent("data_agent") {
		t.Fatalf("general_process allowed agents=%v, want general_agent only", spec.AllowedAgents)
	}
}

func TestTaskRegistryDynamicFrameworkFit(t *testing.T) {
	spec, ok := lookupTaskTypeSpec("framework_fit_7")
	if !ok {
		t.Fatal("dynamic framework_fit_7 is not registered")
	}
	if !spec.allowsAgent("librarian_agent") {
		t.Fatalf("framework_fit_7 allowed agents=%v", spec.AllowedAgents)
	}
}
