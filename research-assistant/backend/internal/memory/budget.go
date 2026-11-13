package memory

// ContextBudget is shared by chat and DAG retrieval. PromptLimit is the actual
// model input capacity; RequiredTokens reserves task data before optional
// memory. The runtime still performs a final, protocol-aware capacity check.
type ContextBudget struct {
	Source              string `json:"source"`
	PromptLimit         int    `json:"prompt_limit"`
	RequiredTokens      int    `json:"required_tokens"`
	ConfiguredCore      int    `json:"configured_core"`
	ConfiguredRetrieved int    `json:"configured_retrieved"`
	Core                int    `json:"core"`
	Retrieved           int    `json:"retrieved"`
	RecallTokens        int    `json:"recall_tokens"`
	WikiTokens          int    `json:"wiki_tokens"`
	DocumentTokens      int    `json:"document_tokens"`
	MemoryTokens        int    `json:"memory_tokens"`
}

func ResolveContextBudget(settings Settings, promptLimit, requiredTokens int) ContextBudget {
	if promptLimit <= 0 {
		// Store-only callers and injected test models have no model profile.
		// Do not invent capacity; generation preflight must resolve it later.
		return ContextBudget{Source: "owner_settings_model_unresolved", PromptLimit: promptLimit, RequiredTokens: max(0, requiredTokens), ConfiguredCore: settings.CoreBudgetTokens, ConfiguredRetrieved: settings.RetrievedBudgetTokens, Core: max(0, settings.CoreBudgetTokens), Retrieved: max(0, settings.RetrievedBudgetTokens)}
	}
	available := max(0, promptLimit-max(0, requiredTokens))
	core := min(max(0, settings.CoreBudgetTokens), available)
	return ContextBudget{
		Source: "owner_settings_capped_by_model", PromptLimit: promptLimit,
		RequiredTokens: max(0, requiredTokens), ConfiguredCore: settings.CoreBudgetTokens,
		ConfiguredRetrieved: settings.RetrievedBudgetTokens, Core: core,
		Retrieved: min(max(0, settings.RetrievedBudgetTokens), available-core),
	}
}

func (b ContextBudget) Remaining() int {
	return max(0, b.Retrieved-b.RecallTokens-b.WikiTokens-b.DocumentTokens)
}
