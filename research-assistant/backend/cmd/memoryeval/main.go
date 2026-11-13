package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"

	"research-assistant-backend/internal/appconfig"
	"research-assistant-backend/internal/memoryeval"
	"research-assistant-backend/internal/modelprofile"
)

func main() {
	real := flag.Bool("real", false, "call configured provider; default uses a deterministic local mock")
	out := flag.String("out", "", "private report output directory (required)")
	repetitions := flag.Int("repetitions", 2, "repetitions of each frozen task in each arm")
	limit := flag.Int("limit", 0, "pilot task limit; zero runs all tasks")
	suitePath := flag.String("suite", "", "custom frozen suite JSON")
	suiteSHA := flag.String("suite-sha256", "", "required SHA256 for a custom frozen suite")
	window := flag.Int("context-window", 6000, "evaluation context window in tokens")
	output := flag.Int("max-output", 768, "generation and compaction output reserve")
	trigger := flag.Float64("trigger", .55, "compaction trigger as a fraction of input budget")
	summaryRatio := flag.Float64("summary-ratio", .15, "summary output target as a fraction of input budget")
	flag.Parse()
	raw := memoryeval.Pilot
	if *suitePath != "" {
		var err error
		raw, err = os.ReadFile(*suitePath)
		if err != nil || *suiteSHA == "" || memoryeval.Digest(raw) != *suiteSHA {
			fail("custom suite is missing or does not match its frozen SHA256")
		}
	}
	if *out == "" {
		fail("-out is required")
	}
	profile := modelprofile.Profile{ID: "evaluation", OwnerID: "eval-owner", Model: "deterministic-mock", Revision: 1, ContextWindowTokens: *window, MaxOutputTokens: *output, TimeoutSeconds: 90, ContextPolicy: modelprofile.ContextPolicy{APIFormat: modelprofile.APIChat, CompactionMode: modelprofile.CompactionText, CompactionTriggerRatio: *trigger, TextSummaryTargetRatio: *summaryRatio}}
	call := modelprofile.Resolved{Profile: profile, APIKey: "mock"}
	providerKind := "deterministic_mock"
	if *real {
		config, err := appconfig.LoadLLMConfig()
		if err != nil {
			fail("real provider configuration unavailable")
		}
		call.Profile.BaseURL, call.Profile.Model, call.APIKey = config.BaseURL, config.Model, config.APIKey
		providerKind = "real_provider"
	} else {
		server := httptest.NewServer(http.HandlerFunc(memoryeval.MockHandler))
		defer server.Close()
		call.Profile.BaseURL = server.URL
		// Only the CLI's own ephemeral mock endpoint is private.
		_ = os.Setenv("ALLOW_PRIVATE_MODEL_ENDPOINTS", "true")
	}
	report, err := memoryeval.Run(context.Background(), raw, memoryeval.Options{Call: call, Repetitions: *repetitions, Limit: *limit, OutputDir: *out, ProviderKind: providerKind})
	if err != nil {
		fail(err.Error())
	}
	for _, arm := range []string{"A", "B", "C"} {
		passed, total := 0, 0
		for _, sample := range report.Samples {
			if sample.Arm == arm {
				total++
				if sample.Passed {
					passed++
				}
			}
		}
		fmt.Printf("%s: %d/%d\n", arm, passed, total)
	}
	fmt.Printf("suite_sha256=%s; provider=%s; report=%s/report.json\n", report.SuiteSHA256, report.ProviderKind, *out)
}

func fail(message string) { fmt.Fprintln(os.Stderr, message); os.Exit(1) }
