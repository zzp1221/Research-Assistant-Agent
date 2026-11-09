package claimverify

import (
	"math"
	"strings"
	"testing"

	"research-assistant-backend/internal/models"
)

func ptr[T any](value T) *T { return &value }

func frozenFixture() (models.ClaimCriterion, []models.MetricObservation) {
	hash := strings.Repeat("a", 64)
	c := models.ClaimCriterion{ID: "c", MetricName: "accuracy", ExpectedValue: ptr(.85), Unit: "fraction", NumericContract: &models.ClaimNumericContract{
		Version: ContractVersion, Operator: "within_tolerance", ToleranceMode: "absolute", AbsoluteTolerance: ptr(.01), Inclusive: ptr(true),
		SourceTaskID: "run", MetricArtifact: "run_metrics", ExtractionMethod: "json_metric", DatasetArtifact: "dataset", DatasetSHA256: hash,
		Split: "test", ProtocolArtifact: "protocol", ProtocolSHA256: hash, CodeSHA256: hash, EnvironmentArtifact: "environment_manifest", EnvironmentSHA256: hash,
		Seeds: []int{0}, Aggregation: "single",
	}}
	o := models.MetricObservation{Version: ObservationVersion, CriterionID: "c", MetricName: "accuracy", Value: .847, Unit: "fraction", DatasetSHA256: hash, Split: "test", ProtocolSHA256: hash, Seed: ptr(0), ExecutionID: "exec", CodeSHA256: hash, EnvironmentRef: "environment_manifest", EnvironmentSHA256: hash, SourceArtifacts: map[string]string{"run_metrics": hash}, ExtractionMethod: "deterministic_json"}
	return c, []models.MetricObservation{o}
}

func TestFrozenComparisonOperatorsUnitsAndBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*models.ClaimCriterion, []models.MetricObservation)
		want   models.ClaimVerificationStatus
	}{
		{"within", func(_ *models.ClaimCriterion, _ []models.MetricObservation) {}, models.ClaimStatusVerified},
		{"negative_result", func(_ *models.ClaimCriterion, o []models.MetricObservation) { o[0].Value = .817 }, models.ClaimStatusContradicted},
		{"exact_decimal_boundary", func(_ *models.ClaimCriterion, o []models.MetricObservation) { o[0].Value = .84 }, models.ClaimStatusVerified},
		{"exclusive_decimal_boundary", func(c *models.ClaimCriterion, o []models.MetricObservation) {
			c.NumericContract.Inclusive = ptr(false)
			o[0].Value = .84
		}, models.ClaimStatusContradicted},
		{"outside_boundary_not_rounded", func(_ *models.ClaimCriterion, o []models.MetricObservation) { o[0].Value = .8399999999999 }, models.ClaimStatusContradicted},
		{"percent_to_fraction", func(_ *models.ClaimCriterion, o []models.MetricObservation) { o[0].Value, o[0].Unit = 84.7, "percent" }, models.ClaimStatusVerified},
		{"fraction_to_percent", func(c *models.ClaimCriterion, _ []models.MetricObservation) {
			c.ExpectedValue, c.Unit, c.NumericContract.AbsoluteTolerance = ptr(85.0), "percent", ptr(1.0)
		}, models.ClaimStatusVerified},
		{"milliseconds_to_seconds", func(c *models.ClaimCriterion, o []models.MetricObservation) {
			c.ExpectedValue, c.Unit = ptr(.85), "seconds"
			o[0].Value, o[0].Unit = 847, "milliseconds"
		}, models.ClaimStatusVerified},
		{"relative", func(c *models.ClaimCriterion, o []models.MetricObservation) {
			c.NumericContract.ToleranceMode, c.NumericContract.AbsoluteTolerance, c.NumericContract.RelativeTolerance = "relative", nil, ptr(.1)
			o[0].Value = .765
		}, models.ClaimStatusVerified},
		{"combined_terms", func(c *models.ClaimCriterion, o []models.MetricObservation) {
			c.NumericContract.ToleranceMode, c.NumericContract.RelativeTolerance = "combined", ptr(.1)
			o[0].Value = .755
		}, models.ClaimStatusVerified},
		{"zero_reference_unspecified", func(c *models.ClaimCriterion, o []models.MetricObservation) {
			c.ExpectedValue = ptr(0.0)
			c.NumericContract.ToleranceMode, c.NumericContract.AbsoluteTolerance, c.NumericContract.RelativeTolerance = "relative", nil, ptr(.1)
			o[0].Value = 0
		}, models.ClaimStatusUnverifiable},
		{"zero_reference_exact", func(c *models.ClaimCriterion, o []models.MetricObservation) {
			c.ExpectedValue = ptr(0.0)
			c.NumericContract.ToleranceMode, c.NumericContract.AbsoluteTolerance, c.NumericContract.RelativeTolerance, c.NumericContract.ZeroReferencePolicy = "relative", nil, ptr(.1), "exact"
			o[0].Value = 0
		}, models.ClaimStatusVerified},
		{"at_least_no_implicit_tolerance", func(c *models.ClaimCriterion, _ []models.MetricObservation) {
			c.NumericContract.Operator, c.NumericContract.ToleranceMode, c.NumericContract.AbsoluteTolerance = "at_least", "none", nil
		}, models.ClaimStatusContradicted},
		{"at_least_explicit_tolerance", func(c *models.ClaimCriterion, _ []models.MetricObservation) { c.NumericContract.Operator = "at_least" }, models.ClaimStatusVerified},
		{"at_most", func(c *models.ClaimCriterion, _ []models.MetricObservation) {
			c.NumericContract.Operator, c.NumericContract.ToleranceMode, c.NumericContract.AbsoluteTolerance = "at_most", "none", nil
		}, models.ClaimStatusVerified},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, o := frozenFixture()
			tc.mutate(&c, o)
			verdict := Verify(c, o)
			if verdict.Status != tc.want {
				t.Fatalf("got %s, want %s: %+v", verdict.Status, tc.want, verdict)
			}
		})
	}
}

func TestInvalidOrUnboundObservationsNeverProduceScientificVerdict(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*models.ClaimCriterion, []models.MetricObservation)
	}{
		{"legacy_criterion", func(c *models.ClaimCriterion, _ []models.MetricObservation) { c.NumericContract = nil }},
		{"ambiguous_tolerance", func(c *models.ClaimCriterion, _ []models.MetricObservation) {
			c.NumericContract.RelativeTolerance = ptr(.2)
		}},
		{"missing_boundary", func(c *models.ClaimCriterion, _ []models.MetricObservation) { c.NumericContract.Inclusive = nil }},
		{"negative_tolerance", func(c *models.ClaimCriterion, _ []models.MetricObservation) {
			c.NumericContract.AbsoluteTolerance = ptr(-1.0)
		}},
		{"nan", func(_ *models.ClaimCriterion, o []models.MetricObservation) { o[0].Value = math.NaN() }},
		{"infinity", func(_ *models.ClaimCriterion, o []models.MetricObservation) { o[0].Value = math.Inf(1) }},
		{"ratio_range", func(_ *models.ClaimCriterion, o []models.MetricObservation) { o[0].Value = 85 }},
		{"explicit_range", func(c *models.ClaimCriterion, o []models.MetricObservation) {
			c.Unit, o[0].Unit, c.NumericContract.Maximum = "score", "score", ptr(.8)
		}},
		{"wrong_split", func(_ *models.ClaimCriterion, o []models.MetricObservation) { o[0].Split = "train" }},
		{"wrong_protocol", func(_ *models.ClaimCriterion, o []models.MetricObservation) {
			o[0].ProtocolSHA256 = strings.Repeat("b", 64)
		}},
		{"missing_code", func(_ *models.ClaimCriterion, o []models.MetricObservation) { o[0].CodeSHA256 = "" }},
		{"missing_environment", func(_ *models.ClaimCriterion, o []models.MetricObservation) { o[0].EnvironmentRef = "" }},
		{"missing_seed", func(_ *models.ClaimCriterion, o []models.MetricObservation) { o[0].Seed = nil }},
		{"model_observation", func(_ *models.ClaimCriterion, o []models.MetricObservation) {
			o[0].ExtractionMethod = "model_extracted"
		}},
		{"missing_original", func(_ *models.ClaimCriterion, o []models.MetricObservation) { o[0].SourceArtifacts = nil }},
		{"incompatible_unit", func(_ *models.ClaimCriterion, o []models.MetricObservation) { o[0].Unit = "seconds" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, o := frozenFixture()
			tc.mutate(&c, o)
			if verdict := Verify(c, o); verdict.Status != models.ClaimStatusUnverifiable {
				t.Fatalf("invalid evidence produced %s: %+v", verdict.Status, verdict)
			}
		})
	}
}

func TestSeedAggregationRequiresExactFrozenSet(t *testing.T) {
	c, o := frozenFixture()
	c.NumericContract.Seeds, c.NumericContract.Aggregation = []int{0, 42}, "mean"
	second := o[0]
	second.Seed, second.Value = ptr(42), .853
	o = append(o, second)
	if verdict := Verify(c, o); verdict.Status != models.ClaimStatusVerified || *verdict.ObservedValue != .85 {
		t.Fatalf("wrong frozen mean: %+v", verdict)
	}
	o[1].Seed = ptr(0)
	if verdict := Verify(c, o); verdict.Status != models.ClaimStatusUnverifiable {
		t.Fatalf("duplicate seed counted as independent run: %+v", verdict)
	}
}
