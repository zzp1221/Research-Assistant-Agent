package planner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"research-assistant-backend/internal/agent/conversation"
	"research-assistant-backend/internal/llmclient"
	"research-assistant-backend/internal/models"
	"research-assistant-backend/internal/prompts"
	"strings"

	"github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
)

type plannerAgent struct {
	enabled   bool
	chatModel model.BaseChatModel
}

type plannerAgentResponse struct {
	Strategy string                 `json:"strategy"`
	Nodes    []plannerNodeBlueprint `json:"nodes"`
}

type plannerNodeBlueprint struct {
	Ref               string   `json:"ref"`
	Name              string   `json:"name"`
	Type              string   `json:"type"`
	AssignedTo        string   `json:"assigned_to"`
	Description       string   `json:"description"`
	Dependencies      []string `json:"dependencies"`
	RequiredArtifacts []string `json:"required_artifacts"`
	OutputArtifacts   []string `json:"output_artifacts"`
	Parallelizable    bool     `json:"parallelizable"`
	Priority          int      `json:"priority"`
}

func newPlannerAgent() *plannerAgent {
	// Network-free/deterministic deployments (and the container E2E suite) can
	// force the template planner while keeping every runtime model feature on.
	if strings.EqualFold(strings.TrimSpace(os.Getenv("PLANNER_AGENT_ENABLED")), "false") {
		return &plannerAgent{enabled: false}
	}
	// The runtime resolver is installed by the API at startup; the env-built
	// model remains as a fallback for env-only deployments and tests.
	runtime := newRuntimePlannerModel(nil)
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey != "" {
		baseURL := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
		if baseURL == "" {
			baseURL = "https://api.deepseek.com/v1"
		}
		modelName := strings.TrimSpace(os.Getenv("OPENAI_MODEL_NAME"))
		if modelName == "" {
			modelName = "deepseek-chat"
		}
		fixed, err := llmclient.NewChatModel(context.Background(), &openai.ChatModelConfig{
			BaseURL: baseURL,
			APIKey:  apiKey,
			Model:   modelName,
		})
		if err != nil {
			log.Printf("[PlannerAgent] init failed, runtime profiles will be used: %v", err)
		} else {
			runtime.fallback = fixed
		}
	}
	return &plannerAgent{
		enabled:   runtime.available(),
		chatModel: runtime,
	}
}

// BuildNodesWithSession runs the planner agent and returns the conversation so
// the bounded repair round can continue it instead of rebuilding the prompt.
func (p *plannerAgent) BuildNodesWithSession(ctx context.Context, intent models.IntentContext, skeleton []*models.TaskNode) ([]*models.TaskNode, *conversation.Session, error) {
	if p == nil || !p.enabled || p.chatModel == nil {
		return nil, nil, fmt.Errorf("planner agent is disabled")
	}

	intentPayload, _ := json.MarshalIndent(intent, "", "  ")
	intentText, _ := prompts.TruncateByTokens(string(intentPayload), 24000, prompts.EstimatedCounter{})
	userPrompt := prompts.PlannerAgentUserPrompt(intentText)
	if len(skeleton) > 0 {
		userPrompt += "\n\n" + prompts.PlannerSkeletonSection(skeleton)
	}
	session := conversation.NewSession(plannerSessionID(intent), prompts.PlannerAgentSystemPrompt, userPrompt)
	msg, err := session.Generate(ctx, p.chatModel)
	if err != nil {
		return nil, session, err
	}

	parsed, err := parsePlannerAgentResponse(msg.Content)
	if err != nil {
		return nil, session, err
	}
	nodes, err := materializePlannerNodes(parsed.Nodes, intent)
	if err != nil {
		return nil, session, err
	}
	if len(skeleton) > 0 {
		nodes = mergeAgentPlanWithSkeleton(skeleton, nodes)
	}
	return nodes, session, nil
}

func plannerSessionID(intent models.IntentContext) string {
	payload, _ := json.Marshal(intent)
	sum := sha256.Sum256(payload)
	return "planner-" + hex.EncodeToString(sum[:8])
}

// RepairNodesWithSession appends the validation feedback to an existing
// conversation. When session is nil it rebuilds the initial conversation with
// the previous node list as the assistant message.
func (p *plannerAgent) RepairNodesWithSession(ctx context.Context, intent models.IntentContext, nodes []*models.TaskNode, skeleton []*models.TaskNode, validationErr error, session *conversation.Session) ([]*models.TaskNode, error) {
	if p == nil || !p.enabled || p.chatModel == nil {
		return nil, fmt.Errorf("planner agent is disabled")
	}
	feedback := "校验错误:\n" + validationErr.Error() +
		"\n\n请修复节点列表并只返回严格的 JSON：{\"nodes\":[{\"ref\",\"name\",\"type\",\"assigned_to\",\"dependencies\",\"required_artifacts\",\"output_artifacts\",\"parallelizable\"}]}。要求：每个节点必须有非空 output_artifacts；不同分支不得重复使用同一个 artifact key（用分支前缀）；最多 14 个节点。"
	if len(skeleton) > 0 {
		feedback += "\n\n" + prompts.PlannerSkeletonSection(skeleton)
	}
	if session == nil {
		intentPayload, _ := json.MarshalIndent(intent, "", "  ")
		session = conversation.NewSession(plannerSessionID(intent), prompts.PlannerAgentSystemPrompt, prompts.PlannerAgentUserPrompt(string(intentPayload)))
		if nodesPayload, err := json.MarshalIndent(nodes, "", "  "); err == nil {
			nodesText, _ := prompts.TruncateByTokens(string(nodesPayload), 24000, prompts.EstimatedCounter{})
			session.AddAssistant(nodesText)
		}
	}
	session.AddUser(feedback)
	msg, err := session.Generate(ctx, p.chatModel)
	if err != nil {
		return nil, err
	}
	parsed, err := parsePlannerAgentResponse(msg.Content)
	if err != nil {
		return nil, err
	}
	repaired, err := materializePlannerNodes(parsed.Nodes, intent)
	if err != nil {
		return nil, err
	}
	if len(skeleton) > 0 {
		repaired = mergeAgentPlanWithSkeleton(skeleton, repaired)
	}
	return repaired, nil
}

// mergeAgentPlanWithSkeleton merges agent output onto the deterministic
// skeleton: matched nodes keep the skeleton identity and artifact contract,
// unmatched skeleton nodes are preserved, and new agent nodes are appended
// with dependencies remapped onto skeleton IDs.
func mergeAgentPlanWithSkeleton(skeleton, agentNodes []*models.TaskNode) []*models.TaskNode {
	if len(skeleton) == 0 {
		return agentNodes
	}
	byName := make(map[string]*models.TaskNode, len(agentNodes))
	for _, node := range agentNodes {
		if node == nil {
			continue
		}
		byName[normalizePlanNodeName(node.Name)] = node
	}
	used := map[string]bool{}
	idMap := map[string]string{}
	out := make([]*models.TaskNode, 0, len(skeleton)+len(agentNodes))
	for _, skeletonNode := range skeleton {
		if skeletonNode == nil {
			continue
		}
		match := byName[normalizePlanNodeName(skeletonNode.Name)]
		if match == nil {
			out = append(out, skeletonNode)
			continue
		}
		used[match.ID] = true
		idMap[match.ID] = skeletonNode.ID
		merged := *skeletonNode
		if strings.TrimSpace(match.Description) != "" {
			merged.Description = match.Description
		}
		out = append(out, &merged)
	}
	for _, node := range agentNodes {
		if node == nil || used[node.ID] {
			continue
		}
		for index, dep := range node.Dependencies {
			if mapped, ok := idMap[dep]; ok {
				node.Dependencies[index] = mapped
			}
		}
		out = append(out, node)
	}
	return out
}

// normalizePlanNodeName normalises a node name for skeleton matching.
func normalizePlanNodeName(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	lower = strings.ReplaceAll(lower, "&", " and ")
	fields := strings.Fields(lower)
	return strings.Join(fields, " ")
}

func parsePlannerAgentResponse(raw string) (*plannerAgentResponse, error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "```json")
	cleaned = strings.TrimPrefix(cleaned, "```")
	cleaned = strings.TrimSuffix(cleaned, "```")
	cleaned = strings.TrimSpace(cleaned)

	var response plannerAgentResponse
	if err := json.Unmarshal([]byte(cleaned), &response); err != nil {
		return nil, fmt.Errorf("parse planner agent response failed: %w", err)
	}
	if len(response.Nodes) == 0 {
		return nil, fmt.Errorf("planner agent returned empty node list")
	}
	return &response, nil
}

// maxAgentPlanNodes bounds an agent-generated plan. Deterministic templates may
// legitimately be larger (paper reproduction is 13 nodes).
const maxAgentPlanNodes = 14

// postProcessPlannerNodes normalises an agent-generated node list before
// validation: consecutive duplicate general_process nodes are merged, nodes
// that cannot produce any artifact are pruned, and artifact keys that would
// have multiple producers are namespaced per branch.
func postProcessPlannerNodes(nodes []*models.TaskNode) []*models.TaskNode {
	nodes = mergeConsecutiveGeneralProcess(nodes)
	nodes = pruneOutputlessNodes(nodes)
	namespaceDuplicateArtifacts(nodes)
	return nodes
}

// mergeableNodeTypes are pure LLM/report stages where two consecutive
// instances add latency but no information.
var mergeableNodeTypes = map[string]struct{}{
	"general_process":   {},
	"general_synthesis": {},
	"general_research":  {},
	"verify_result":     {},
}

// mergeConsecutiveGeneralProcess collapses a chain of identical mergeable nodes
// into one, which is the most common source of "3-4 repeated nodes".
func mergeConsecutiveGeneralProcess(nodes []*models.TaskNode) []*models.TaskNode {
	if len(nodes) < 2 {
		return nodes
	}
	out := make([]*models.TaskNode, 0, len(nodes))
	removed := map[string]string{} // removed id -> replacement id
	for _, node := range nodes {
		if node == nil {
			continue
		}
		_, mergeable := mergeableNodeTypes[node.Type]
		if len(out) > 0 && mergeable && node.Type == out[len(out)-1].Type {
			previous := out[len(out)-1]
			previous.OutputArtifacts = ensureArtifacts(previous.OutputArtifacts, node.OutputArtifacts...)
			previous.RequiredArtifacts = ensureArtifacts(previous.RequiredArtifacts, node.RequiredArtifacts...)
			for _, produced := range previous.OutputArtifacts {
				previous.RequiredArtifacts = removeArtifact(previous.RequiredArtifacts, produced)
			}
			previous.Dependencies = append(previous.Dependencies, node.Dependencies...)
			removed[node.ID] = previous.ID
			continue
		}
		out = append(out, node)
	}
	for _, node := range out {
		node.Dependencies = dedupeDependencies(node.Dependencies, removed, node.ID)
	}
	return out
}

func dedupeDependencies(deps []string, removed map[string]string, selfID string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(deps))
	for _, dep := range deps {
		if replacement, ok := removed[dep]; ok {
			dep = replacement
		}
		if dep == selfID {
			continue
		}
		if _, ok := seen[dep]; ok {
			continue
		}
		seen[dep] = struct{}{}
		out = append(out, dep)
	}
	return out
}

// pruneOutputlessNodes removes nodes that declare no output artifact; a node
// that cannot produce anything only adds latency and failure surface.
func pruneOutputlessNodes(nodes []*models.TaskNode) []*models.TaskNode {
	if len(nodes) == 0 {
		return nodes
	}
	removed := map[string][]string{}
	keep := make([]*models.TaskNode, 0, len(nodes))
	for _, node := range nodes {
		if node == nil {
			continue
		}
		if len(node.OutputArtifacts) == 0 {
			// Splice the node out: downstream nodes inherit its dependencies.
			removed[node.ID] = append([]string(nil), node.Dependencies...)
			continue
		}
		keep = append(keep, node)
	}
	if len(removed) == 0 {
		return keep
	}
	var expand func(id string, depth int) []string
	expand = func(id string, depth int) []string {
		deps, ok := removed[id]
		if !ok || depth > 8 {
			return []string{id}
		}
		out := make([]string, 0, len(deps))
		for _, dep := range deps {
			out = append(out, expand(dep, depth+1)...)
		}
		return out
	}
	for _, node := range keep {
		next := make([]string, 0, len(node.Dependencies))
		for _, dep := range node.Dependencies {
			next = append(next, expand(dep, 0)...)
		}
		node.Dependencies = dedupeDependencies(next, nil, node.ID)
	}
	return keep
}

// namespaceDuplicateArtifacts rewrites artifact keys that would otherwise have
// multiple producers (for example two framework branches both emitting
// "generated_code") into per-branch keys, and updates downstream requirements
// to reference the producer they actually depend on.
func namespaceDuplicateArtifacts(nodes []*models.TaskNode) {
	producers := map[string][]*models.TaskNode{}
	for _, node := range nodes {
		if node == nil {
			continue
		}
		for _, key := range node.OutputArtifacts {
			producers[key] = append(producers[key], node)
		}
	}
	rename := map[string]map[string]string{}
	for key, list := range producers {
		if len(list) < 2 {
			continue
		}
		rename[key] = map[string]string{}
		for index, node := range list {
			newKey := artifactPrefixForNode(node, index) + "_" + key
			rename[key][node.ID] = newKey
			node.OutputArtifacts = replaceArtifactName(node.OutputArtifacts, key, newKey)
		}
	}
	if len(rename) == 0 {
		return
	}
	for _, node := range nodes {
		if node == nil {
			continue
		}
		for index, required := range node.RequiredArtifacts {
			mapping, duplicated := rename[required]
			if !duplicated {
				continue
			}
			if producerID := findAncestorProducer(node, nodes, mapping); producerID != "" {
				node.RequiredArtifacts[index] = mapping[producerID]
			}
		}
	}
}

func artifactPrefixForNode(node *models.TaskNode, index int) string {
	base := normalizeToken(node.Name)
	if base == "" {
		base = "branch"
	}
	fields := strings.Fields(base)
	if len(fields) > 2 {
		fields = fields[:2]
	}
	slug := strings.Join(fields, "_")
	if slug == "" {
		slug = fmt.Sprintf("branch_%d", index+1)
	}
	return slug
}

func replaceArtifactName(values []string, oldName, newName string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == oldName {
			out = append(out, newName)
			continue
		}
		out = append(out, value)
	}
	return out
}

// findAncestorProducer finds the nearest transitive dependency of node that is
// one of the candidate producers.
func findAncestorProducer(node *models.TaskNode, nodes []*models.TaskNode, candidates map[string]string) string {
	byID := map[string]*models.TaskNode{}
	for _, item := range nodes {
		if item != nil {
			byID[item.ID] = item
		}
	}
	visited := map[string]struct{}{}
	queue := append([]string(nil), node.Dependencies...)
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if _, ok := visited[current]; ok {
			continue
		}
		visited[current] = struct{}{}
		if _, ok := candidates[current]; ok {
			return current
		}
		if dep, ok := byID[current]; ok {
			queue = append(queue, dep.Dependencies...)
		}
	}
	return ""
}

func materializePlannerNodes(blueprints []plannerNodeBlueprint, intent models.IntentContext) ([]*models.TaskNode, error) {
	refToID := make(map[string]string, len(blueprints))
	nodes := make([]*models.TaskNode, 0, len(blueprints))

	for _, raw := range blueprints {
		bp := normalizePlannerBlueprint(raw)
		ref := strings.TrimSpace(bp.Ref)
		if ref == "" {
			return nil, fmt.Errorf("planner node ref is empty")
		}
		if _, exists := refToID[ref]; exists {
			return nil, fmt.Errorf("duplicate planner node ref: %s", ref)
		}

		node := newNode(
			chooseString(bp.Name, ref),
			chooseString(bp.Type, "general_process"),
			normalizeAssignedTo(bp.AssignedTo),
			nil,
			cleanStringSlice(bp.RequiredArtifacts),
			cleanStringSlice(bp.OutputArtifacts),
			bp.Parallelizable,
			intent.RawIntent,
		)
		node.Description = buildPlannerNodeDescription(intent.RawIntent, bp)
		node.Priority = bp.Priority
		attachPlannerContextBindings(node, intent)
		refToID[ref] = node.ID
		nodes = append(nodes, node)
	}

	for idx, raw := range blueprints {
		bp := normalizePlannerBlueprint(raw)
		deps := make([]string, 0, len(bp.Dependencies))
		for _, depRef := range cleanStringSlice(bp.Dependencies) {
			depID, ok := refToID[depRef]
			if !ok {
				return nil, fmt.Errorf("unknown dependency ref %s for node %s", depRef, bp.Ref)
			}
			deps = append(deps, depID)
		}
		nodes[idx].Dependencies = deps
	}

	return nodes, nil
}

func buildPlannerNodeDescription(rawIntent string, bp plannerNodeBlueprint) string {
	if bp.Type == "repo_discovery" {
		return buildRepoDiscoveryDescription(rawIntent)
	}
	detail := strings.TrimSpace(bp.Description)
	if detail == "" {
		detail = bp.Name
	}
	return prompts.PlannerNodeDescription(bilingualTaskName(bp.Name), detail, rawIntent)
}

func normalizeAssignedTo(value string) string {
	switch strings.TrimSpace(value) {
	case "librarian_agent", "coder_agent", "sandbox_agent", "data_agent", "benchmark_agent", "research_coding_agent", "general_agent", "answer_agent":
		return value
	default:
		return "general_agent"
	}
}

func cleanStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}

func chooseString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func normalizePlannerBlueprint(bp plannerNodeBlueprint) plannerNodeBlueprint {
	bp.AssignedTo = normalizeAssignedTo(bp.AssignedTo)
	bp.Type = normalizePlannerTaskType(bp)
	bp.AssignedTo = normalizePlannerAssignedTo(bp)
	bp.Dependencies = cleanStringSlice(bp.Dependencies)
	bp.RequiredArtifacts = cleanStringSlice(bp.RequiredArtifacts)
	bp.OutputArtifacts = cleanStringSlice(bp.OutputArtifacts)
	bp = applyPlannerNodeDefaults(bp)
	return bp
}

func normalizePlannerTaskType(bp plannerNodeBlueprint) string {
	rawType := normalizeToken(bp.Type)
	if rawType == "" {
		rawType = normalizeToken(bp.Name)
	}
	context := strings.Join([]string{
		rawType,
		normalizeToken(bp.Name),
		normalizeToken(bp.Description),
		strings.Join(normalizeTokens(bp.RequiredArtifacts), " "),
		strings.Join(normalizeTokens(bp.OutputArtifacts), " "),
	}, " ")

	// The registry is the single source of truth for known task types. This
	// preserves the blueprint's canonical type before the agent-specific
	// fallback mapping below; dead types are intentionally not accepted.
	if spec, ok := lookupTaskTypeSpec(strings.TrimSpace(bp.Type)); ok && spec.ExecutionKind != ExecutionDead {
		return strings.TrimSpace(bp.Type)
	}

	switch bp.AssignedTo {
	case "benchmark_agent":
		if containsAny(context, "benchmark_dataset_audit", "audit dataset", "dataset audit") {
			return "benchmark_dataset_audit"
		}
		if containsAny(context, "benchmark_split_materialize", "materialize split", "leakage safe split") {
			return "benchmark_split_materialize"
		}
		if containsAny(context, "benchmark_contract_freeze", "metric contract", "reward contract", "freeze benchmark") {
			return "benchmark_contract_freeze"
		}
		if containsAny(context, "benchmark_validate", "hidden benchmark", "hidden evidence") {
			return "benchmark_validate"
		}
	case "research_coding_agent":
		if containsAny(context, "autoresearch_spec", "freeze autoresearch", "research spec", "research contract") {
			return "autoresearch_spec"
		}
		if containsAny(context, "autoresearch_validate", "validate autoresearch", "independent evaluator", "validation report") {
			return "autoresearch_validate"
		}
		if containsAny(context, "autoresearch_run", "autoresearch loop", "trial ledger", "keep reject") {
			return "autoresearch_run"
		}
		if containsAny(context, "dataset_profile", "profile dataset", "inspect dataset", "uploaded dataset") {
			return "dataset_profile"
		}
		if containsAny(context, "benchmark_adapter_preflight", "preflight", "repair adapter", "validate adapter") {
			return "benchmark_adapter_preflight"
		}
		if containsAny(context, "benchmark_adapter_generate", "generate adapter", "benchmark adapter", "adapter code") {
			return "benchmark_adapter_generate"
		}
		if containsAny(context, "benchmark_validate", "validate evidence", "verify benchmark", "benchmark evidence") {
			return "benchmark_validate"
		}
		if containsAny(context, "benchmark_execute", "execute benchmark", "run benchmark", "custom benchmark") {
			return "benchmark_execute"
		}
		if containsAny(context, "fix_and_rerun", "result gap", "debug mismatch", "repair and rerun") {
			return "fix_and_rerun"
		}
		if containsAny(context, "paper_code_execute", "execute baseline", "run paper code", "debug baseline") {
			return "paper_code_execute"
		}
	case "sandbox_agent":
		if containsAny(context, "install_dependencies", "install dependency", "install package", "pip install", "requirements") {
			return "install_dependencies"
		}
		if containsAny(context, "prepare_runtime", "runtime_session", "prepared_runtime", "prepare environment", "setup runtime", "setup environment", "test environment") {
			return "prepare_runtime"
		}
		if containsAny(context, "execute_code", "run benchmark", "benchmark", "baseline_run", "run code", "execute", "test run", "run experiment") {
			return "execute_code"
		}
	case "coder_agent":
		if containsAny(context, "resolve_dependencies", "dependency_spec", "dependency", "imports", "requirements", "package list") {
			return "resolve_dependencies"
		}
		if containsAny(context, "generate_code", "benchmark code", "integration code", "adapter code", "implementation", "script", "generate") {
			return "generate_code"
		}
		if containsAny(context, "repo_discovery", "repository", "repo url", "find repo", "clone repo") {
			return "repo_discovery"
		}
		if containsAny(context, "repo_prepare", "prepare workspace", "workspace", "checkout") {
			return "repo_prepare"
		}
		if containsAny(context, "ablation_design", "ablation", "消融", "parameter sensitivity", "seed stability") {
			return "ablation_design"
		}
	case "librarian_agent":
		if containsAny(context, "claim_rubric_extract", "claim rubric", "freeze claims", "gradable criteria") {
			return "claim_rubric_extract"
		}
		if containsAny(context, "paper_parse", "extract method", "parse paper") {
			return "paper_parse"
		}
		if containsAny(context, "literature_search", "search literature", "find papers", "search papers", "literature review", "文献检索", "论文检索") {
			return "literature_search"
		}
		if containsAny(context, "framework_research", "candidate framework", "research framework", "documentation", "best practice") {
			return "framework_research"
		}
		if containsAny(context, "general_research", "collect background", "background context") {
			return "general_research"
		}
	case "data_agent":
		if containsAny(context, "claim_evidence_build", "claim evidence", "evidence graph", "claim verification") {
			return "claim_evidence_build"
		}
		if containsAny(context, "framework_recommendation", "framework_report", "benchmark report", "selection recommendation") {
			return "framework_recommendation"
		}
		if containsAny(context, "paper_compare", "compare with paper", "comparison report") {
			return "paper_compare"
		}
		if containsAny(context, "render_plot", "result_visualization", "plot", "visualize", "chart") {
			return "render_plot"
		}
		if containsAny(context, "verify_result", "verify", "summarize", "analysis", "report", "summary") {
			return "verify_result"
		}
	}

	switch rawType {
	case "framework_research", "framework_recommendation", "generate_code", "resolve_dependencies", "prepare_runtime", "install_dependencies", "execute_code", "paper_code_execute", "paper_parse", "claim_rubric_extract", "repo_discovery", "repo_prepare", "ablation_design", "paper_compare", "claim_evidence_build", "result_visualization", "fix_and_rerun", "verify_result", "render_plot", "general_research", "literature_search", "general_synthesis", "general_process", "dataset_profile", "benchmark_dataset_audit", "benchmark_split_materialize", "benchmark_contract_freeze", "benchmark_adapter_generate", "benchmark_adapter_preflight", "benchmark_execute", "benchmark_validate", "autoresearch_spec", "autoresearch_run", "autoresearch_validate", "experiment_dataset_prepare", "experiment_spec", "experiment_run", "experiment_validate":
		return rawType
	}

	switch bp.AssignedTo {
	case "sandbox_agent":
		return "execute_code"
	case "coder_agent":
		return "generate_code"
	case "librarian_agent":
		return "general_research"
	case "data_agent":
		return "verify_result"
	case "research_coding_agent":
		return "benchmark_adapter_generate"
	case "benchmark_agent":
		return "benchmark_dataset_audit"
	default:
		return "general_process"
	}
}

func attachPlannerContextBindings(node *models.TaskNode, intent models.IntentContext) {
	if node == nil {
		return
	}
	switch node.Type {
	case "paper_parse":
		node.InputBindings = appendMissingBinding(node.InputBindings, models.InputBinding{
			Target: "document_id", Source: models.BindingSource{Kind: "context_document"}, Required: false,
		})
		node.InputBindings = appendMissingBinding(node.InputBindings, models.InputBinding{
			Target: "paper_metadata", Source: models.BindingSource{Kind: "context_paper"}, Required: false,
		})
		node.OutputArtifacts = ensureArtifacts(node.OutputArtifacts, "parsed_paper", "paper_metadata", "paper_summary")
	case "repo_discovery":
		if intent.IntentType == "Paper_Reproduction" {
			node.RequiredArtifacts = ensureArtifacts(node.RequiredArtifacts, "paper_metadata")
		}
		// Agent blueprints do not carry planner-side constants; without these
		// deterministic inputs repo_discovery would search for the task
		// description instead of the paper title/arXiv id (D2).
		if len(node.Inputs) == 0 {
			node.Inputs = buildRepoDiscoveryInputs(intent)
		}
		node.Description = buildRepoDiscoveryDescription(intent.RawIntent)
	case "literature_search":
		node.OutputArtifacts = ensureArtifacts(node.OutputArtifacts, "literature_results")
		if len(node.Inputs) == 0 {
			node.Inputs = map[string]any{
				"query":   literatureSearchQuery(intent),
				"sources": literatureSearchSources(intent),
				"limit":   5,
			}
		}
	case "general_synthesis":
		if intent.Entities != nil && intent.Entities["paper_metadata"] != nil {
			node.RequiredArtifacts = ensureArtifacts(node.RequiredArtifacts, "paper_summary", "paper_metadata")
		}
	}
}

func appendMissingBinding(bindings []models.InputBinding, binding models.InputBinding) []models.InputBinding {
	for _, existing := range bindings {
		if existing.Target == binding.Target {
			return bindings
		}
	}
	return append(bindings, binding)
}

func normalizeToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer("-", " ", "_", " ", "/", " ", "(", " ", ")", " ", ",", " ", ".", " ", "\n", " ")
	value = replacer.Replace(value)
	return strings.Join(strings.Fields(value), " ")
}

func normalizeTokens(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		normalized := normalizeToken(value)
		if normalized != "" {
			out = append(out, normalized)
		}
	}
	return out
}

func containsAny(s string, keywords ...string) bool {
	for _, keyword := range keywords {
		if strings.Contains(s, normalizeToken(keyword)) {
			return true
		}
	}
	return false
}

func normalizePlannerAssignedTo(bp plannerNodeBlueprint) string {
	switch bp.Type {
	case "benchmark_dataset_audit", "benchmark_split_materialize", "benchmark_contract_freeze", "benchmark_validate":
		return "benchmark_agent"
	case "dataset_profile", "benchmark_adapter_generate", "benchmark_adapter_preflight", "benchmark_execute", "paper_code_execute", "fix_and_rerun", "autoresearch_spec", "autoresearch_run", "autoresearch_validate", "experiment_dataset_prepare", "experiment_spec", "experiment_run", "experiment_validate":
		return "research_coding_agent"
	case "generate_code", "resolve_dependencies", "repo_discovery", "repo_prepare", "render_plot", "result_visualization":
		return "coder_agent"
	case "prepare_runtime", "install_dependencies", "execute_code":
		return "sandbox_agent"
	case "framework_research", "paper_parse", "claim_rubric_extract", "general_research", "literature_search":
		return "librarian_agent"
	case "framework_recommendation", "ablation_design", "paper_compare", "claim_evidence_build", "verify_result":
		return "data_agent"
	case "general_synthesis":
		return "answer_agent"
	case "general_process":
		return "general_agent"
	default:
		return bp.AssignedTo
	}
}

func applyPlannerNodeDefaults(bp plannerNodeBlueprint) plannerNodeBlueprint {
	// The registry is the single source of truth for required output artifacts.
	// Hand-written switch branches below must not redefine them (D5).
	if spec, ok := lookupTaskTypeSpec(bp.Type); ok {
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, spec.RequiredOutputArtifacts...)
	}
	switch bp.Type {
	case "generate_code":
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "generated_code")
	case "resolve_dependencies":
		bp.RequiredArtifacts = ensureArtifacts(bp.RequiredArtifacts, "generated_code")
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "dependency_spec")
	case "prepare_runtime":
		bp.RequiredArtifacts = ensureArtifacts(bp.RequiredArtifacts, "dependency_spec")
		// prepared_runtime belongs to install_dependencies; a duplicate producer
		// makes the plan fail at completion time (D5).
		bp.OutputArtifacts = removeArtifact(bp.OutputArtifacts, "prepared_runtime")
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "runtime_session")
		// A node must never require its own output.
		bp.RequiredArtifacts = removeArtifact(bp.RequiredArtifacts, "runtime_session")
	case "install_dependencies":
		bp.RequiredArtifacts = ensureArtifacts(bp.RequiredArtifacts, "runtime_session", "dependency_spec")
		bp.OutputArtifacts = removeArtifact(bp.OutputArtifacts, "runtime_session")
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "prepared_runtime", "dependency_install_report")
		bp.RequiredArtifacts = removeArtifact(bp.RequiredArtifacts, "prepared_runtime")
		bp.RequiredArtifacts = removeArtifact(bp.RequiredArtifacts, "dependency_install_report")
	case "execute_code":
		bp.RequiredArtifacts = ensureArtifacts(bp.RequiredArtifacts, "generated_code", "prepared_runtime")
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "execution_result")
	case "verify_result":
		// general_response is owned by general_synthesis; verify_result emits
		// the verification report consumed by the final answer (D5/D9).
		bp.OutputArtifacts = removeArtifact(bp.OutputArtifacts, "general_response")
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "verification_report")
	case "benchmark_dataset_audit":
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "dataset_manifest", "benchmark_dataset_audit")
	case "benchmark_split_materialize":
		bp.RequiredArtifacts = ensureArtifacts(bp.RequiredArtifacts, "dataset_manifest", "benchmark_dataset_audit", "workspace_path")
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "benchmark_split_manifest", "benchmark_leakage_report", "benchmark_validation_dataset_manifest", "benchmark_public_test_manifest", "benchmark_input_only_preflight_manifest")
	case "benchmark_adapter_preflight":
		bp.RequiredArtifacts = ensureArtifacts(bp.RequiredArtifacts, "workspace_path", "benchmark_validation_dataset_manifest", "benchmark_input_only_preflight_manifest", "benchmark_contract", "benchmark_adapter_spec", "benchmark_generated_code", "benchmark_code_file_path", "prepared_runtime")
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "validated_benchmark_adapter_spec", "validated_benchmark_generated_code", "validated_benchmark_code_file_path", "benchmark_preflight_report")
	case "benchmark_contract_freeze":
		bp.RequiredArtifacts = ensureArtifacts(bp.RequiredArtifacts, "workspace_path", "benchmark_dataset_audit", "benchmark_split_manifest")
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "benchmark_contract", "benchmark_metric_contract", "benchmark_reward_contract", "benchmark_evaluator_manifest")
	case "paper_parse":
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "parsed_paper", "paper_metadata", "paper_summary")
	case "general_synthesis":
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "general_response")
	case "claim_rubric_extract":
		bp.RequiredArtifacts = ensureArtifacts(bp.RequiredArtifacts, "parsed_paper")
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "claim_rubric", "claim_rubric_report")
	case "claim_evidence_build":
		bp.RequiredArtifacts = ensureArtifacts(bp.RequiredArtifacts, "claim_rubric", "parsed_paper", "repo_manifest", "reproduction_mode_report", "dependency_install_report", "run_metrics", "paper_debug_report", "paper_patch_manifest", "comparison_report")
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "claim_evidence_graph", "claim_verification_report")
	case "repo_discovery":
		bp.RequiredArtifacts = ensureArtifacts(bp.RequiredArtifacts, "parsed_paper")
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "candidate_repositories", "repo_validation_report", "repo_url")
		if strings.TrimSpace(bp.Description) == "" {
			bp.Description = prompts.RepoDiscoveryDescription("请根据归一化意图检索论文对应的公开实现仓库。")
		}
	case "repo_prepare":
		bp.RequiredArtifacts = ensureArtifacts(bp.RequiredArtifacts, "repo_url", "candidate_repositories", "repo_validation_report")
		// reproduction_mode_report is consumed by claim_evidence_build; without a
		// producer the whole agent plan fails validation and falls back to the
		// template even when everything else is correct.
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "workspace_path", "code_file_path", "generated_code", "repo_manifest", "reproduction_mode_report")
	case "paper_code_execute":
		bp.RequiredArtifacts = ensureArtifacts(bp.RequiredArtifacts, "workspace_path", "code_file_path", "generated_code", "prepared_runtime", "repo_manifest")
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "run_metrics", "paper_debug_report", "paper_patch_manifest")
	case "fix_and_rerun":
		bp.RequiredArtifacts = ensureArtifacts(bp.RequiredArtifacts, "workspace_path", "code_file_path", "generated_code", "prepared_runtime", "repo_manifest", "run_metrics", "paper_debug_report", "comparison_report")
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "rerun_metrics", "rerun_report", "gap_debug_report", "gap_patch_manifest")
	case "autoresearch_spec":
		bp.RequiredArtifacts = ensureArtifacts(bp.RequiredArtifacts, "workspace_path", "repo_manifest")
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "research_spec", "research_spec_report", "dependency_spec")
	case "autoresearch_run":
		bp.RequiredArtifacts = ensureArtifacts(bp.RequiredArtifacts, "workspace_path", "prepared_runtime", "research_spec")
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "research_trial_ledger", "research_best_candidate", "research_run_report")
	case "autoresearch_validate":
		bp.RequiredArtifacts = ensureArtifacts(bp.RequiredArtifacts, "workspace_path", "prepared_runtime", "research_spec", "research_trial_ledger", "research_best_candidate")
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "research_validation_report", "research_best_metrics")
	case "experiment_dataset_prepare":
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "workspace_path", "experiment_dataset_manifest", "experiment_dataset_report")
	case "experiment_spec":
		bp.RequiredArtifacts = ensureArtifacts(bp.RequiredArtifacts, "workspace_path", "experiment_dataset_manifest")
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "experiment_spec", "experiment_spec_report", "dependency_spec")
	case "experiment_run":
		bp.RequiredArtifacts = ensureArtifacts(bp.RequiredArtifacts, "workspace_path", "prepared_runtime", "experiment_spec")
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "experiment_trial_ledger", "experiment_best_candidate", "experiment_run_report")
	case "experiment_validate":
		bp.RequiredArtifacts = ensureArtifacts(bp.RequiredArtifacts, "workspace_path", "prepared_runtime", "experiment_spec", "experiment_trial_ledger", "experiment_best_candidate")
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, "experiment_validation_report", "experiment_best_metrics")
	case "render_plot", "result_visualization":
		bp.AssignedTo = "coder_agent"
		key := "plot_image"
		if bp.Type == "result_visualization" {
			key = "result_plot"
		}
		bp.OutputArtifacts = ensureArtifacts(bp.OutputArtifacts, key)
	}
	return bp
}

func removeArtifact(current []string, unwanted string) []string {
	out := make([]string, 0, len(current))
	for _, item := range current {
		if item == unwanted {
			continue
		}
		out = append(out, item)
	}
	return out
}

func ensureArtifacts(current []string, required ...string) []string {
	existing := make(map[string]struct{}, len(current))
	out := append([]string(nil), current...)
	for _, item := range current {
		existing[item] = struct{}{}
	}
	for _, item := range required {
		if _, ok := existing[item]; ok {
			continue
		}
		out = append(out, item)
	}
	return out
}
