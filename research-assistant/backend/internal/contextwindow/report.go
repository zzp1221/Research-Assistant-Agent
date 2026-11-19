package contextwindow

import "context"

type Report struct {
	Requests       RequestCounts     `json:"http_requests"`
	ProfileID      string            `json:"profile_id"`
	Revision       int               `json:"profile_revision"`
	Model          string            `json:"model"`
	Capacity       int               `json:"capacity"`
	InputBudget    int               `json:"input_budget"`
	Mode           string            `json:"compaction_mode"`
	Generation     int               `json:"generation"`
	StateRef       string            `json:"state_ref,omitempty"`
	Observation    Observation       `json:"input_observation"`
	LastCompaction *CompactionRecord `json:"last_compaction,omitempty"`
	Materials      []MaterialReport  `json:"materials,omitempty"`
}
type RequestCounts struct {
	Generation int `json:"generation"`
	Compaction int `json:"compaction"`
	Counting   int `json:"counting"`
}

func CountRequest(ctx context.Context, purpose string) {
	if report := ReportFrom(ctx); report != nil {
		switch purpose {
		case "generation":
			report.Requests.Generation++
		case "compaction":
			report.Requests.Compaction++
		case "counting":
			report.Requests.Counting++
		}
	}
}

type reportKey struct{}

func WithReport(ctx context.Context, report *Report) context.Context {
	return context.WithValue(ctx, reportKey{}, report)
}
func ReportFrom(ctx context.Context) *Report {
	report, _ := ctx.Value(reportKey{}).(*Report)
	return report
}
