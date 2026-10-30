package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"research-assistant-backend/internal/models"
)

func boundedAutoResearchInput(task *models.Task, key string, fallback, minimum, maximum int) int {
	value, ok := optionalBoundedAutoResearchInput(task, key, minimum, maximum)
	if !ok {
		value = fallback
	}
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func optionalBoundedAutoResearchInput(task *models.Task, key string, minimum, maximum int) (int, bool) {
	raw := strings.TrimSpace(extractTaskInputLike(task, key))
	if raw == "" {
		return 0, false
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	if value < minimum {
		return minimum, true
	}
	if value > maximum {
		return maximum, true
	}
	return value, true
}

func cleanAutoResearchStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func failAutoResearchRun(task *models.Task, ledger *models.ResearchTrialLedger, failure error) error {
	if ledger != nil {
		if ledger.FinishedAt.IsZero() {
			ledger.FinishedAt = time.Now().UTC()
		}
		finalizeAutoResearchLedgerUsage(ledger)
		ledgerJSON, _ := json.Marshal(ledger)
		if task != nil {
			task.Result = string(ledgerJSON)
			task.StructuredData = string(ledgerJSON)
			setResearchCodingArtifacts(task, map[string]string{"research_trial_ledger": string(ledgerJSON)})
		}
	}
	return failResearchCodingTask(task, failure)
}

func finalizeAutoResearchLedgerUsage(ledger *models.ResearchTrialLedger) {
	if ledger == nil {
		return
	}
	usage := models.ResearchResourceUsage{}
	for _, trial := range ledger.Trials {
		for _, command := range trial.GuardResults {
			addAutoResearchCommandUsage(&usage, command, true)
		}
		if len(trial.EvalResults) > 0 {
			for _, command := range trial.EvalResults {
				addAutoResearchCommandUsage(&usage, command, false)
			}
		} else {
			addAutoResearchCommandUsage(&usage, trial.EvalResult, false)
		}
	}
	if ledger.HoldoutResult != nil {
		addAutoResearchCommandUsage(&usage, *ledger.HoldoutResult, false)
	}
	usage.WallDurationMS = autoResearchElapsedMS(ledger.StartedAt, ledger.FinishedAt)
	ledger.ResourceUsage = &usage
}

func addAutoResearchCommandUsage(usage *models.ResearchResourceUsage, command models.ResearchCommandResult, guard bool) {
	if usage == nil || len(command.Command) == 0 {
		return
	}
	usage.CommandRuns++
	if guard {
		usage.GuardRuns++
	} else {
		usage.EvaluatorRuns++
	}
	if command.DurationMS > 0 {
		usage.CommandDurationMS += command.DurationMS
	}
	if command.Error == "" && command.ExitCode == 0 {
		usage.SuccessfulCommands++
	} else {
		usage.FailedCommands++
	}
}

func autoResearchElapsedMS(startedAt, finishedAt time.Time) int64 {
	if startedAt.IsZero() || finishedAt.IsZero() || finishedAt.Before(startedAt) {
		return 0
	}
	return finishedAt.Sub(startedAt).Milliseconds()
}

func autoResearchSHA(raw []byte) string {
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}
