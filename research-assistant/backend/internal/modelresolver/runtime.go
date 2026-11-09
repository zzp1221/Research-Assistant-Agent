package modelresolver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"research-assistant-backend/internal/audit"
	"research-assistant-backend/internal/contextwindow"
	"research-assistant-backend/internal/modelprofile"
	"research-assistant-backend/internal/oaichat"
	"research-assistant-backend/internal/responses"
	"research-assistant-backend/internal/usage"

	"github.com/cloudwego/eino/schema"
)

type callKey struct{}
type branchKey struct{}
type constraintsKey struct{}

// WithConstraints supplies current program-owned task/contract state. It is
// appended as fresh user data, never by rewriting canonical native items.
func WithConstraints(ctx context.Context, text string) context.Context {
	return context.WithValue(ctx, constraintsKey{}, text)
}

// WithResolvedCall pins one immutable profile revision and secret in memory.
func WithResolvedCall(ctx context.Context, call modelprofile.Resolved) context.Context {
	return context.WithValue(ctx, callKey{}, call)
}
func ResolvedCall(ctx context.Context) (modelprofile.Resolved, bool) {
	call, ok := ctx.Value(callKey{}).(modelprofile.Resolved)
	return call, ok
}
func WithBranch(ctx context.Context, owner, project, session, branch string) context.Context {
	raw, _ := json.Marshal([]string{owner, project, session, branch})
	return context.WithValue(ctx, branchKey{}, string(raw))
}
func WithSubBranch(ctx context.Context, branch string) context.Context {
	parent, _ := ctx.Value(branchKey{}).(string)
	return WithBranch(ctx, OwnerFromContext(ctx), "", parent, branch)
}
func WithoutBranch(ctx context.Context) context.Context {
	return contextwindow.WithReport(context.WithValue(ctx, branchKey{}, ""), nil)
}
func (r *Resolver) ResolveCall(ctx context.Context) (modelprofile.Resolved, error) {
	if call, ok := ResolvedCall(ctx); ok {
		return call, nil
	}
	return r.ResolveText(OwnerFromContext(ctx))
}

// ResolveStep refreshes a loop's next logical call without changing the
// immutable configuration already used by an in-flight request. An injected
// resolved call is also usable with a storeless resolver (offline evaluations).
func (r *Resolver) ResolveStep(ctx context.Context, owner, profileID string, previous modelprofile.Resolved) (modelprofile.Resolved, error) {
	if r.store == nil {
		if pinned, ok := ResolvedCall(ctx); ok {
			return pinned, nil
		}
		return previous, nil
	}
	if profileID != "" {
		return r.ResolveProfile(owner, profileID)
	}
	return r.ResolveText(owner)
}
func contextDir() string {
	if dir := strings.TrimSpace(os.Getenv("CONTEXT_STORE_DIR")); dir != "" {
		return dir
	}
	return filepath.Join(DefaultStoreDir(), "context-windows")
}

// CompleteManaged is shared by chat, planner, repair and profile-bound agents.
// It never changes model/protocol on a failure and never retries compaction.
func (r *Resolver) CompleteManaged(ctx context.Context, resolved modelprofile.Resolved, messages []oaichat.Message, opts oaichat.Options) (*oaichat.Response, error) {
	p := resolved.Profile
	if err := p.ValidateContext(); err != nil {
		return nil, err
	}
	resolved.Profile = p
	toolMode := len(opts.Tools) > 0
	for _, m := range messages {
		toolMode = toolMode || len(m.ToolCalls) > 0 || m.ToolCallID != ""
	}
	if toolMode && p.Capabilities.ToolCalling != modelprofile.VisionYes {
		return nil, fmt.Errorf("model_tools_unsupported: confirm native tool calling capability")
	}
	budget, err := contextwindow.ResolveBudget(p, opts.MaxOutputTokens)
	if err != nil {
		return nil, err
	}
	opts.MaxOutputTokens = budget.MaxOutput
	if opts.Timeout <= 0 {
		opts.Timeout = defaultTimeout(p.TimeoutSeconds)
	}
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	var instructions []string
	var visible []oaichat.Message
	for _, m := range messages {
		if m.Role == "system" || m.Role == "developer" {
			instructions = append(instructions, m.Text)
		} else {
			visible = append(visible, m)
		}
	}
	if len(visible) == 0 {
		return nil, fmt.Errorf("context_required_items_missing")
	}
	for _, m := range visible {
		if m.Role != "user" && m.Role != "assistant" && !(toolMode && m.Role == "tool" && m.ToolCallID != "") {
			return nil, fmt.Errorf("context_protocol_unsupported: tool interactions require native tool adapter")
		}
	}
	instruction := strings.Join(instructions, "\n\n")
	hashes := make([]string, len(visible))
	for i, m := range visible {
		raw, _ := json.Marshal(m)
		hashes[i] = contextwindow.Hash(raw)
	}
	constraints, _ := ctx.Value(constraintsKey{}).(string)
	inputRaw, _ := json.Marshal(struct {
		Messages        []oaichat.Message
		Constraints     string
		ProfileID       string
		ProfileRevision int
		Budget          contextwindow.Budget
		Options         oaichat.Options
	}{messages, constraints, p.ID, p.Revision, budget, opts})
	inputHash := contextwindow.Hash(inputRaw)
	// Credentials are hashed only to prevent opaque state reuse across accounts;
	// neither the credential nor an unredacted endpoint is persisted.
	bindingRaw, _ := json.Marshal([]string{p.BaseURL, p.Model, p.APIFormat, p.CompactionMode, contextwindow.Hash([]byte(resolved.APIKey))})
	binding := contextwindow.Hash(bindingRaw)
	key, _ := ctx.Value(branchKey{}).(string)
	var state contextwindow.State
	defer func() {
		if report := contextwindow.ReportFrom(ctx); report != nil {
			requests := report.Requests
			*report = contextwindow.Report{Requests: requests, ProfileID: p.ID, Revision: p.Revision, Model: p.Model, Capacity: p.ContextWindowTokens, InputBudget: budget.PromptLimit(), Mode: p.CompactionMode, Generation: state.Generation, Observation: state.Observation}
			if key != "" {
				report.StateRef = contextwindow.Hash([]byte(key))
			}
			if len(state.Compactions) > 0 {
				record := state.Compactions[len(state.Compactions)-1]
				report.LastCompaction = &record
			}
		}
	}()
	if key != "" {
		key = OwnerFromContext(ctx) + ":" + key
		unlock, lockErr := r.contexts.Acquire(ctx, key)
		if lockErr != nil {
			return nil, lockErr
		}
		defer unlock()
		state, err = r.contexts.Load(key)
		if err != nil {
			return nil, err
		}
		if err = r.contexts.Archive(key, inputHash, inputRaw); err != nil {
			return nil, err
		}
	}
	commit := func() error {
		if key == "" {
			state.Generation++
			return nil
		}
		return r.contexts.Commit(key, state.Generation, &state)
	}
	if state.Binding == binding && state.LastInputHash == inputHash && (state.LastText != "" || len(state.LastToolCalls) > 0) {
		return &oaichat.Response{Content: state.LastText, ToolCalls: state.LastToolCalls, FinishReason: state.LastFinishReason, EstimatedTokens: true, Reused: true}, nil
	}
	compatible := state.Binding == binding && len(state.Source) <= len(hashes)
	if compatible {
		for i, h := range state.Source {
			if hashes[i] != h {
				compatible = false
				break
			}
		}
	}
	if !compatible {
		if state.Generation > 0 {
			audit.Record("context_window_rebuilt", p.OwnerID, p.ID, map[string]any{"revision": p.Revision, "generation": state.Generation})
		}
		generation := state.Generation
		state = contextwindow.State{Generation: generation, Binding: binding, ProfileID: p.ID, ProfileRevision: p.Revision, Mode: p.CompactionMode}
	}
	state.ProfileRevision = p.Revision
	if state.LastInputHash == inputHash && (state.LastText != "" || len(state.LastToolCalls) > 0) {
		return &oaichat.Response{Content: state.LastText, ToolCalls: state.LastToolCalls, FinishReason: state.LastFinishReason, EstimatedTokens: true, Reused: true}, nil
	}
	if len(state.Source) > 0 && len(hashes) == len(state.Source) {
		return nil, fmt.Errorf("context_turn_conflict: no new input")
	}
	audit.Record("context_budget_resolved", p.OwnerID, p.ID, map[string]any{"revision": p.Revision, "capacity": p.ContextWindowTokens, "input_budget": budget.PromptLimit(), "generation": state.Generation, "count_source": "estimated"})
	if p.APIFormat == modelprofile.APIResponses {
		return r.completeResponses(ctx, resolved, opts, budget, &state, visible, hashes, instruction, inputHash, commit)
	}
	return r.completeText(ctx, resolved, opts, budget, &state, visible, hashes, instruction, inputHash, commit)
}

func nativeMessages(messages []oaichat.Message) []responses.Item {
	items := make([]responses.Item, 0, len(messages))
	for _, m := range messages {
		if m.Role == "tool" {
			raw, _ := json.Marshal(map[string]any{"type": "function_call_output", "call_id": m.ToolCallID, "output": m.Text})
			items = append(items, raw)
			continue
		}
		if len(m.ToolCalls) > 0 {
			for _, call := range m.ToolCalls {
				raw, _ := json.Marshal(map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Function.Name, "arguments": call.Function.Arguments})
				items = append(items, raw)
			}
			if m.Text == "" {
				continue
			}
		}
		var content any = m.Text
		if len(m.Images) > 0 {
			parts := []map[string]any{{"type": "input_text", "text": m.Text}}
			for _, image := range m.Images {
				mime := image.MIMEType
				if mime == "" {
					mime = "image/png"
				}
				parts = append(parts, map[string]any{"type": "input_image", "image_url": "data:" + mime + ";base64," + image.Base64})
			}
			content = parts
		}
		raw, _ := json.Marshal(map[string]any{"role": m.Role, "content": content})
		items = append(items, raw)
	}
	return items
}

func (r *Resolver) completeResponses(ctx context.Context, resolved modelprofile.Resolved, opts oaichat.Options, budget contextwindow.Budget, state *contextwindow.State, visible []oaichat.Message, hashes []string, instruction, inputHash string, commit func() error) (*oaichat.Response, error) {
	p := resolved.Profile
	client := responses.Client{HTTP: r.clientFor(p).HTTPClient()}
	if len(state.Window) == 0 && len(visible) > 1 {
		state.Window = nativeMessages(visible[:len(visible)-1])
		state.Source = append([]string{}, hashes[:len(hashes)-1]...)
	}
	newItems := nativeMessages(visible[len(state.Source):])
	if constraints, _ := ctx.Value(constraintsKey{}).(string); constraints != "" {
		newItems = append(nativeMessages([]oaichat.Message{{Role: "user", Text: "[current task constraints and frozen contract; authoritative source data]\n" + constraints}}), newItems...)
	}
	request := responses.Request{Model: p.Model, Instructions: instruction, MaxOutputTokens: budget.MaxOutput, Store: false, Include: []string{"reasoning.encrypted_content"}}
	for _, tool := range opts.Tools {
		request.Tools = append(request.Tools, responses.ToolDefinition{Type: "function", Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters})
	}
	count := func(items []responses.Item) (contextwindow.Observation, error) {
		if responses.HasOpaque(items) || hasImages(visible) {
			if p.Capabilities.RenderedInputCount != modelprofile.VisionYes {
				return contextwindow.Observation{Source: "unknown", Confidence: "unknown"}, fmt.Errorf("context_size_unknown: deployment must confirm rendered input counting")
			}
			q := request
			q.Input = items
			contextwindow.CountRequest(ctx, "counting")
			n, err := client.Count(ctx, p.BaseURL, resolved.APIKey, q)
			if err != nil {
				return contextwindow.Observation{}, err
			}
			return contextwindow.Observation{Tokens: &n, Source: "provider", Confidence: "exact"}, nil
		}
		// Only visible text is estimated. Never serialize opaque state for counting.
		n := usage.EstimateTokens(instruction) + 8
		if len(opts.Tools) > 0 {
			raw, _ := json.Marshal(opts.Tools)
			n += usage.EstimateTokens(string(raw)) + 8
		}
		for _, item := range items {
			var m struct {
				Content json.RawMessage `json:"content"`
				Type    string          `json:"type"`
			}
			_ = json.Unmarshal(item, &m)
			if m.Type == "function_call" || m.Type == "function_call_output" {
				n += usage.EstimateTokens(string(item)) + 8
				continue
			}
			if len(m.Content) == 0 && m.Type != "" {
				return contextwindow.Observation{}, fmt.Errorf("context_size_unknown: unsupported native item")
			}
			n += usage.EstimateTokens(string(m.Content)) + 8
		}
		return contextwindow.Observation{Tokens: &n, Source: "estimated", Confidence: "estimated"}, nil
	}
	required, requiredErr := count(newItems)
	if requiredErr != nil {
		return nil, requiredErr
	}
	if *required.Tokens > budget.PromptLimit() {
		return nil, fmt.Errorf("context_required_items_too_large")
	}
	request.Input = append(append([]responses.Item{}, state.Window...), newItems...)
	observation, err := count(request.Input)
	if err != nil {
		return nil, err
	}
	if *observation.Tokens >= budget.Trigger && len(state.Window) > 0 && p.CompactionMode == modelprofile.CompactionStandalone && !compactedSource(state, inputHash) {
		compactInput := state.Window
		var protected []responses.Item
		// A tool response must retain the complete native assistant output that
		// requested it, including associated reasoning. Never compact that call
		// away while appending a function_call_output after the compaction.
		if len(state.LastToolCalls) > 0 {
			keep := state.LastOutputItems
			if keep <= 0 || keep > len(state.Window) {
				keep = len(state.Window) // Legacy state cannot prove a safe boundary.
			}
			protected = state.Window[len(state.Window)-keep:]
			compactInput = state.Window[:len(state.Window)-keep]
		}
		if len(compactInput) == 0 {
			if *observation.Tokens > budget.PromptLimit() {
				return nil, fmt.Errorf("context_required_items_too_large: latest native tool group exceeds budget")
			}
		} else {
			old, oldErr := count(compactInput)
			if oldErr != nil {
				return nil, oldErr
			}
			if *old.Tokens > budget.PromptLimit() {
				return nil, fmt.Errorf("context_rebuild_required: old window exceeds compaction capacity")
			}
			start := time.Now()
			audit.Record("context_compaction_started", p.OwnerID, p.ID, map[string]any{"mode": p.CompactionMode, "generation": state.Generation, "revision": p.Revision})
			var result *responses.Result
			compactErr := contextwindow.CheckAllowance(ctx, *old.Tokens+budget.MaxOutput+*observation.Tokens+budget.MaxOutput, nil)
			attempted := compactErr == nil
			if attempted {
				contextwindow.CountRequest(ctx, "compaction")
				result, compactErr = client.Compact(ctx, p.BaseURL, resolved.APIKey, p.Model, instruction, compactInput)
			}
			record := contextwindow.CompactionRecord{Purpose: "context_compaction", Mode: p.CompactionMode, SourceHash: inputHash, Attempts: 1, LatencyMS: time.Since(start).Milliseconds(), At: time.Now().UTC(), Status: "completed"}
			if !attempted {
				record.Attempts = 0
			}
			if compactErr != nil {
				record.Status = "outcome_unknown"
				if !attempted {
					record.Status = "budget_blocked"
				}
				record.ErrorCode = "compaction_failed"
			} else {
				record.Usage = result.Usage
				state.Window = append(append([]responses.Item{}, result.Output...), protected...)
				state.Observation = contextwindow.Observation{Source: "unknown", Confidence: "unknown"}
			}
			state.Compactions = append(state.Compactions, record)
			if attempted && state.Compactions[len(state.Compactions)-1].Usage == nil {
				state.Compactions[len(state.Compactions)-1].Usage = &responses.Usage{InputTokens: *old.Tokens, OutputTokens: budget.MaxOutput}
				state.Compactions[len(state.Compactions)-1].UsageEstimated = true
			}
			recordCompaction(ctx, p, &state.Compactions[len(state.Compactions)-1])
			if err := commit(); err != nil {
				return nil, err
			}
			if compactErr == nil {
				request.Input = append(append([]responses.Item{}, state.Window...), newItems...)
				observation, err = count(request.Input)
				if err != nil {
					return nil, err
				}
			} else if *observation.Tokens > budget.PromptLimit() {
				return nil, compactErr
			}
		}
	}
	if *observation.Tokens > budget.PromptLimit() {
		return nil, fmt.Errorf("compaction_insufficient: input exceeds current model budget")
	}
	if p.CompactionMode == modelprofile.CompactionServer {
		// A finite cost cap cannot authorize a server operation with unknown
		// rates. The unchanged input already fits, so generation may continue
		// without requesting automatic compaction.
		if allowanceErr := contextwindow.CheckAllowance(ctx, 2*(*observation.Tokens+budget.MaxOutput), nil); allowanceErr == nil {
			request.ContextManagement = []responses.Management{{Type: "compaction", CompactThreshold: budget.Trigger}}
		} else {
			state.Compactions = append(state.Compactions, contextwindow.CompactionRecord{Purpose: "context_compaction", Mode: p.CompactionMode, SourceHash: inputHash, Status: "budget_blocked", ErrorCode: "compaction_budget_blocked", At: time.Now().UTC()})
			recordCompaction(ctx, p, &state.Compactions[len(state.Compactions)-1])
			if err := commit(); err != nil {
				return nil, err
			}
		}
	}
	if err := checkGenerationAllowance(ctx, p, *observation.Tokens, budget.MaxOutput); err != nil {
		return nil, err
	}
	started := time.Now()
	contextwindow.CountRequest(ctx, "generation")
	result, err := client.Create(ctx, p.BaseURL, resolved.APIKey, request)
	if err != nil {
		recordFailedGeneration(ctx, p, *observation.Tokens, budget.MaxOutput)
		return nil, err
	}
	state.Window = append(request.Input, result.Output...)
	serverCompacted := false
	if p.CompactionMode == modelprofile.CompactionServer {
		for _, raw := range result.Output {
			var item struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(raw, &item)
			if item.Type == "compaction" {
				serverCompacted = true
				break
			}
		}
		if serverCompacted {
			state.Compactions = append(state.Compactions, contextwindow.CompactionRecord{Purpose: "context_compaction", Mode: p.CompactionMode, SourceHash: inputHash, Status: "completed", LatencyMS: time.Since(started).Milliseconds(), At: time.Now().UTC()})
			recordCompaction(ctx, p, &state.Compactions[len(state.Compactions)-1])
			if recorder := usage.FromContext(ctx); recorder != nil {
				recorder.MarkServerCompaction()
			}
		}
	}
	// Only server mode permits dropping the prefix before the latest compaction.
	if p.CompactionMode == modelprofile.CompactionServer {
		for i := len(state.Window) - 1; i >= 0; i-- {
			var item struct {
				Type string `json:"type"`
			}
			_ = json.Unmarshal(state.Window[i], &item)
			if item.Type == "compaction" {
				state.Window = state.Window[i:]
				break
			}
		}
	}
	text := responses.Text(result.Output)
	calls, callErr := responseToolCalls(result.Output)
	if callErr != nil {
		recordFailedGeneration(ctx, p, *observation.Tokens, budget.MaxOutput)
		return nil, callErr
	}
	if text == "" && len(calls) == 0 {
		recordFailedGeneration(ctx, p, *observation.Tokens, budget.MaxOutput)
		return nil, fmt.Errorf("responses_empty_text: no executable tool adapter configured")
	}
	assistantRaw, _ := json.Marshal(oaichat.Message{Role: "assistant", Text: text, ToolCalls: calls})
	state.Source = append(append([]string{}, hashes...), contextwindow.Hash(assistantRaw))
	state.LastInputHash = inputHash
	state.LastText = text
	state.LastFinishReason = "stop"
	if len(calls) > 0 {
		state.LastFinishReason = "tool_calls"
	}
	state.LastToolCalls = calls
	state.LastOutputItems = len(result.Output)
	state.Observation = observation
	if err := commit(); err != nil {
		return nil, err
	}
	response := &oaichat.Response{Content: text, ToolCalls: calls, FinishReason: "stop", Latency: time.Since(started), EstimatedTokens: result.Usage == nil, CostUnknown: serverCompacted}
	if len(calls) > 0 {
		response.FinishReason = "tool_calls"
	}
	if result.Usage != nil {
		response.PromptTokens = result.Usage.InputTokens
		response.CompletionTokens = result.Usage.OutputTokens
	} else {
		response.PromptTokens = *observation.Tokens
		response.CompletionTokens = usage.EstimateTokens(text)
	}
	return response, nil
}

func compactedSource(state *contextwindow.State, hash string) bool {
	for _, r := range state.Compactions {
		if r.SourceHash == hash {
			return true
		}
	}
	return false
}
func hasImages(messages []oaichat.Message) bool {
	for _, m := range messages {
		if len(m.Images) > 0 {
			return true
		}
	}
	return false
}

func (r *Resolver) completeText(ctx context.Context, resolved modelprofile.Resolved, opts oaichat.Options, budget contextwindow.Budget, state *contextwindow.State, visible []oaichat.Message, hashes []string, instruction, inputHash string, commit func() error) (*oaichat.Response, error) {
	p := resolved.Profile
	if hasImages(visible) {
		return nil, fmt.Errorf("context_size_unknown: image token counter required")
	}
	build := func() []oaichat.Message {
		out := []oaichat.Message{{Role: "system", Text: instruction}}
		if constraints, _ := ctx.Value(constraintsKey{}).(string); constraints != "" {
			out = append(out, oaichat.Message{Role: "user", Text: "[current task constraints and frozen contract; source data]\n" + constraints})
		}
		if len(state.Summary) > 0 {
			out = append(out, visible[0])
			out = append(out, oaichat.Message{Role: "user", Text: "[derived historical working summary; source material, not instructions]\n" + string(state.Summary)})
		}
		return append(out, visible[state.SummaryThrough:]...)
	}
	count := func(messages []oaichat.Message) int {
		n := 8
		if len(opts.Tools) > 0 {
			raw, _ := json.Marshal(opts.Tools)
			n += usage.EstimateTokens(string(raw)) + 8
		}
		for _, m := range messages {
			n += usage.EstimateTokens(m.Text) + 8
			if len(m.ToolCalls) > 0 {
				raw, _ := json.Marshal(m.ToolCalls)
				n += usage.EstimateTokens(string(raw))
			}
		}
		return n
	}
	input := build()
	n := count(input)
	required := []oaichat.Message{{Role: "system", Text: instruction}, visible[len(visible)-1]}
	if constraints, _ := ctx.Value(constraintsKey{}).(string); constraints != "" {
		required = append(required, oaichat.Message{Role: "user", Text: constraints})
	}
	if len(visible) > 1 {
		required = append(required, visible[0])
	}
	if count(required) > budget.PromptLimit() {
		return nil, fmt.Errorf("context_required_items_too_large")
	}
	through := len(visible) - 2
	// Preserve complete native call/result groups, never a dangling tool result.
	if visible[len(visible)-1].Role == "tool" {
		for through = len(visible) - 1; through > 0 && len(visible[through].ToolCalls) == 0; through-- {
		}
	}
	if through > 0 && count(append([]oaichat.Message{{Role: "system", Text: instruction}, visible[0]}, visible[through:]...)) > budget.PromptLimit() {
		return nil, fmt.Errorf("context_required_items_too_large: latest tool group exceeds budget")
	}
	if n >= budget.Trigger && through > max(state.SummaryThrough, 1) && p.CompactionMode == modelprofile.CompactionText && !compactedSource(state, inputHash) {
		materials := make([]map[string]string, 0, through)
		sources := map[string]bool{}
		for _, id := range hashes[:through] {
			sources[id] = true
		}
		start := state.SummaryThrough
		if start < 1 {
			start = 1
		}
		for i, m := range visible[start:through] {
			text := m.Text
			if len(m.ToolCalls) > 0 || m.ToolCallID != "" {
				raw, _ := json.Marshal(m)
				text = string(raw)
			}
			materials = append(materials, map[string]string{"id": hashes[start+i], "role": m.Role, "text": text})
		}
		raw, _ := json.Marshal(map[string]any{"previous_summary": state.Summary, "new_material": materials})
		summaryInput := []oaichat.Message{{Role: "system", Text: contextwindow.SummaryInstruction}, {Role: "user", Text: string(raw)}}
		target := int(float64(budget.PromptLimit()) * p.TextSummaryTargetRatio)
		if target > budget.MaxOutput {
			target = budget.MaxOutput
		}
		if count(summaryInput)+target+budget.Safety >= p.ContextWindowTokens {
			return nil, fmt.Errorf("context_rebuild_required: summary source exceeds model capacity")
		}
		started := time.Now()
		var result *oaichat.Response
		totalTokens := count(summaryInput) + target + n + budget.MaxOutput
		var cost *float64
		if value, source := usage.EstimateCostWithPrice(count(summaryInput)+n, target+budget.MaxOutput, usage.Price{PromptPer1K: p.PromptPricePer1K, CompletionPer1K: p.CompletionPricePer1K}); source != "unknown" {
			cost = &value
		}
		compactErr := contextwindow.CheckAllowance(ctx, totalTokens, cost)
		attempted := compactErr == nil
		if attempted {
			contextwindow.CountRequest(ctx, "compaction")
			result, compactErr = r.clientFor(p).Complete(ctx, p.BaseURL, resolved.APIKey, p.Model, summaryInput, oaichat.Options{MaxOutputTokens: target, Timeout: opts.Timeout, ResponseFormatJSON: true})
		}
		record := contextwindow.CompactionRecord{Purpose: "context_compaction", Mode: p.CompactionMode, SourceHash: inputHash, Attempts: 1, LatencyMS: time.Since(started).Milliseconds(), At: time.Now().UTC(), Status: "completed"}
		if !attempted {
			record.Attempts = 0
		}
		if compactErr == nil {
			if result == nil || usage.EstimateTokens(result.Content) > target || (result.FinishReason != "" && result.FinishReason != "stop") {
				compactErr = fmt.Errorf("summary_invalid")
			} else {
				compactErr = contextwindow.ValidateSummary([]byte(result.Content), sources)
			}
		}
		if result != nil {
			record.Usage = &responses.Usage{InputTokens: result.PromptTokens, OutputTokens: result.CompletionTokens}
			record.UsageEstimated = result.EstimatedTokens
			if cost, source := usage.EstimateCostWithPrice(result.PromptTokens, result.CompletionTokens, usage.Price{PromptPer1K: p.PromptPricePer1K, CompletionPer1K: p.CompletionPricePer1K}); source != "unknown" {
				record.CostUSD = &cost
			}
		}
		if compactErr == nil {
			state.Summary = json.RawMessage(result.Content)
			state.SummaryThrough = through
			// The summary is committed before generation and depends on every
			// source item through this boundary, including new repair attempts.
			// A later edited or shortened history must rebuild from its sources.
			state.Source = append([]string{}, hashes[:through]...)
		} else {
			record.Status = "failed"
			if !attempted {
				record.Status = "budget_blocked"
			}
			record.ErrorCode = "summary_invalid"
		}
		state.Compactions = append(state.Compactions, record)
		if attempted && state.Compactions[len(state.Compactions)-1].Usage == nil {
			state.Compactions[len(state.Compactions)-1].Usage = &responses.Usage{InputTokens: count(summaryInput), OutputTokens: target}
			state.Compactions[len(state.Compactions)-1].UsageEstimated = true
		}
		recordCompaction(ctx, p, &state.Compactions[len(state.Compactions)-1])
		if err := commit(); err != nil {
			return nil, err
		}
		input = build()
		n = count(input)
		if compactErr != nil && n > budget.PromptLimit() {
			return nil, fmt.Errorf("context_compaction_failed: summary generation or validation failed")
		}
	}
	if n > budget.PromptLimit() {
		return nil, fmt.Errorf("context_required_items_too_large: input exceeds model budget")
	}
	if err := checkGenerationAllowance(ctx, p, n, budget.MaxOutput); err != nil {
		return nil, err
	}
	contextwindow.CountRequest(ctx, "generation")
	response, err := r.clientFor(p).Complete(ctx, p.BaseURL, resolved.APIKey, p.Model, input, opts)
	if err != nil {
		recordFailedGeneration(ctx, p, n, opts.MaxOutputTokens)
		return nil, fmt.Errorf("model_generation_failed: %w", err)
	}
	if response == nil || (response.Content == "" && len(response.ToolCalls) == 0) {
		recordFailedGeneration(ctx, p, n, opts.MaxOutputTokens)
		return nil, fmt.Errorf("model returned empty response")
	}
	assistantRaw, _ := json.Marshal(oaichat.Message{Role: "assistant", Text: response.Content, ToolCalls: response.ToolCalls})
	state.Source = append(append([]string{}, hashes...), contextwindow.Hash(assistantRaw))
	state.LastInputHash = inputHash
	state.LastText = response.Content
	state.LastFinishReason = response.FinishReason
	state.LastToolCalls = response.ToolCalls
	state.Observation = contextwindow.Observation{Tokens: &n, Source: "estimated", Confidence: "estimated"}
	if err := commit(); err != nil {
		return nil, err
	}
	return response, nil
}

func checkGenerationAllowance(ctx context.Context, p modelprofile.Profile, input, output int) error {
	var cost *float64
	if estimate, source := usage.EstimateCostWithPrice(input, output, usage.Price{PromptPer1K: p.PromptPricePer1K, CompletionPer1K: p.CompletionPricePer1K}); source != "unknown" {
		cost = &estimate
	}
	if err := contextwindow.CheckAllowance(ctx, input+output, cost); err != nil {
		return fmt.Errorf("model_generation_budget_exhausted: %w", err)
	}
	return nil
}

// A failed request may have consumed provider tokens without returning usage.
// Charge its reserved upper estimate locally so retries cannot reuse the same
// allowance; diagnostics explicitly identify these values as estimates.
func recordFailedGeneration(ctx context.Context, p modelprofile.Profile, input, output int) {
	if recorder := usage.FromContext(ctx); recorder != nil {
		if p.CompactionMode == modelprofile.CompactionServer {
			recorder.RecordLLMUnknownCost(p.Model, input, output, true)
			return
		}
		recorder.RecordLLMWithPrice(p.Model, input, output, true, usage.Price{PromptPer1K: p.PromptPricePer1K, CompletionPer1K: p.CompletionPricePer1K})
	}
}

func recordCompaction(ctx context.Context, p modelprofile.Profile, record *contextwindow.CompactionRecord) {
	event := "context_compaction_completed"
	if record.Status != "completed" {
		event = "context_compaction_failed"
	}
	audit.Record(event, p.OwnerID, p.ID, map[string]any{"mode": p.CompactionMode, "revision": p.Revision, "status": record.Status, "attempts": record.Attempts, "latency_ms": record.LatencyMS, "cost_known": record.CostUSD != nil})
	if recorder := usage.FromContext(ctx); recorder != nil && record.Attempts > 0 {
		in, out := 0, 0
		if record.Usage != nil {
			in = record.Usage.InputTokens
			out = record.Usage.OutputTokens
		}
		recorder.RecordCompaction(p.Model, in, out, record.Usage == nil || record.UsageEstimated, usage.Price{PromptPer1K: p.PromptPricePer1K, CompletionPer1K: p.CompletionPricePer1K}, p.CompactionMode == modelprofile.CompactionText)
	}
}

// GenerateSchema adapts text at the Eino boundary; native output items remain
// exclusively in ContextStore. Tools cannot be executed through this adapter.
func (r *Resolver) GenerateSchema(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
	call, err := r.ResolveCall(ctx)
	if err != nil {
		return nil, err
	}
	messages := make([]oaichat.Message, 0, len(input))
	for _, m := range input {
		if m == nil {
			continue
		}
		if len(m.ToolCalls) > 0 || m.ToolCallID != "" || len(m.MultiContent) > 0 || len(m.UserInputMultiContent) > 0 || len(m.AssistantGenMultiContent) > 0 {
			return nil, fmt.Errorf("context_protocol_unsupported: structured message requires protocol adapter")
		}
		messages = append(messages, oaichat.Message{Role: string(m.Role), Text: m.Content})
	}
	response, err := r.CompleteManaged(ctx, call, messages, oaichat.Options{})
	if err != nil {
		return nil, err
	}
	msg := &schema.Message{Role: schema.Assistant, Content: response.Content}
	msg.ResponseMeta = &schema.ResponseMeta{Usage: &schema.TokenUsage{PromptTokens: response.PromptTokens, CompletionTokens: response.CompletionTokens}}
	msg.Extra = map[string]any{"usage_estimated": response.EstimatedTokens, "usage_already_recorded": response.Reused, "cost_unknown": response.CostUnknown}
	return msg, nil
}
