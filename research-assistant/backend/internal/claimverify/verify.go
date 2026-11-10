// Package claimverify compares frozen scientific criteria with observations
// whose execution provenance has already been checked by the agent boundary.
package claimverify

import (
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"research-assistant-backend/internal/models"
)

const Version = "claim.numeric-verifier/v1"
const ContractVersion = "claim.numeric-contract/v1"
const ObservationVersion = "metric.observation/v1"
const ExecutionVersion = "metric.execution/v1"

func Finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func SHA256(value string) bool {
	raw, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil && len(raw) == 32
}

func SameHash(a, b string) bool {
	return SHA256(a) && SHA256(b) && strings.EqualFold(strings.TrimPrefix(a, "sha256:"), strings.TrimPrefix(b, "sha256:"))
}

func ValidateContract(criterion models.ClaimCriterion) error {
	c := criterion.NumericContract
	if c == nil || c.Version != ContractVersion || criterion.ExpectedValue == nil || !Finite(*criterion.ExpectedValue) || criterion.MetricName == "" || criterion.Unit == "" {
		return fmt.Errorf("numeric contract, metric, expected value and unit must be explicitly frozen")
	}
	if c.Inclusive == nil {
		return fmt.Errorf("comparison boundary must be explicitly frozen")
	}
	if c.Operator != "within_tolerance" && c.Operator != "at_least" && c.Operator != "at_most" {
		return fmt.Errorf("unknown comparison operator")
	}
	validTolerance := func(v *float64) bool { return v != nil && Finite(*v) && *v >= 0 }
	switch c.ToleranceMode {
	case "absolute":
		if !validTolerance(c.AbsoluteTolerance) || c.RelativeTolerance != nil {
			return fmt.Errorf("absolute tolerance is incomplete or ambiguous")
		}
	case "relative":
		if !validTolerance(c.RelativeTolerance) || c.AbsoluteTolerance != nil {
			return fmt.Errorf("relative tolerance is incomplete or ambiguous")
		}
		if *criterion.ExpectedValue == 0 && c.ZeroReferencePolicy != "exact" {
			return fmt.Errorf("zero relative reference requires an explicit exact-match policy")
		}
	case "combined":
		if !validTolerance(c.AbsoluteTolerance) || !validTolerance(c.RelativeTolerance) {
			return fmt.Errorf("combined tolerance requires both absolute and relative terms")
		}
	case "none":
		if c.AbsoluteTolerance != nil || c.RelativeTolerance != nil {
			return fmt.Errorf("no-tolerance mode contains tolerance values")
		}
	default:
		return fmt.Errorf("tolerance mode must be explicitly frozen")
	}
	if c.ZeroReferencePolicy != "" && c.ZeroReferencePolicy != "exact" && c.ZeroReferencePolicy != "unverifiable" {
		return fmt.Errorf("unknown zero reference policy")
	}
	if c.Minimum != nil && !Finite(*c.Minimum) || c.Maximum != nil && !Finite(*c.Maximum) || c.Minimum != nil && c.Maximum != nil && *c.Minimum > *c.Maximum {
		return fmt.Errorf("invalid metric range")
	}
	if c.Minimum != nil && *criterion.ExpectedValue < *c.Minimum || c.Maximum != nil && *criterion.ExpectedValue > *c.Maximum {
		return fmt.Errorf("expected value is outside the frozen metric range")
	}
	if boundedUnit(criterion.Unit) {
		upper := 1.0
		if canonicalUnit(criterion.Unit) == "percent" {
			upper = 100
		}
		if *criterion.ExpectedValue < 0 || *criterion.ExpectedValue > upper {
			return fmt.Errorf("expected proportion is outside its legal range")
		}
	}
	if c.SourceTaskID == "" || c.MetricArtifact == "" || c.DatasetArtifact == "" || c.ProtocolArtifact == "" || c.EnvironmentArtifact == "" || c.Split == "" || !SHA256(c.DatasetSHA256) || !SHA256(c.ProtocolSHA256) || !SHA256(c.CodeSHA256) || !SHA256(c.EnvironmentSHA256) {
		return fmt.Errorf("frozen execution protocol or evidence provenance is incomplete")
	}
	if c.ExtractionMethod != "json_metric" && c.ExtractionMethod != "benchmark_predictions" {
		return fmt.Errorf("unknown deterministic extraction method")
	}
	if c.ExtractionMethod == "benchmark_predictions" && (c.PredictionsArtifact == "" || c.PredictionTaskType != "classification" && c.PredictionTaskType != "regression") {
		return fmt.Errorf("prediction recomputation contract is incomplete")
	}
	if len(c.Seeds) == 0 || len(c.Seeds) > 100 {
		return fmt.Errorf("explicit seed set is required (at most 100)")
	}
	seen := map[int]bool{}
	for _, seed := range c.Seeds {
		if seen[seed] {
			return fmt.Errorf("duplicate frozen seed")
		}
		seen[seed] = true
	}
	if c.Aggregation != "single" && c.Aggregation != "mean" && c.Aggregation != "minimum" && c.Aggregation != "maximum" {
		return fmt.Errorf("aggregation must be explicitly frozen")
	}
	if c.Aggregation == "single" && len(c.Seeds) != 1 {
		return fmt.Errorf("single aggregation requires exactly one seed")
	}
	return nil
}

func Unverifiable(reason string) models.DeterministicClaimVerdict {
	return models.DeterministicClaimVerdict{VerifierVersion: Version, Status: models.ClaimStatusUnverifiable, Reason: reason, TrustBoundary: "untrusted_or_incomplete_observation"}
}

func Verify(criterion models.ClaimCriterion, observations []models.MetricObservation) models.DeterministicClaimVerdict {
	if err := ValidateContract(criterion); err != nil {
		return Unverifiable(err.Error())
	}
	c := criterion.NumericContract
	if len(observations) != len(c.Seeds) {
		return Unverifiable("observations do not cover the frozen seed set")
	}
	seeds := map[int]bool{}
	for _, seed := range c.Seeds {
		seeds[seed] = true
	}
	values := make([]*big.Rat, 0, len(observations))
	trust := "backend_recomputed_from_execution_artifacts"
	for _, o := range observations {
		if o.Version != ObservationVersion || o.CriterionID != criterion.ID || o.MetricName != criterion.MetricName || !Finite(o.Value) || o.Seed == nil || !seeds[*o.Seed] || o.ExecutionID == "" {
			return Unverifiable("invalid, missing or duplicate metric observation")
		}
		delete(seeds, *o.Seed)
		if !SameHash(o.DatasetSHA256, c.DatasetSHA256) || o.Split != c.Split || !SameHash(o.ProtocolSHA256, c.ProtocolSHA256) || !SameHash(o.CodeSHA256, c.CodeSHA256) || o.EnvironmentRef != c.EnvironmentArtifact || !SameHash(o.EnvironmentSHA256, c.EnvironmentSHA256) || len(o.SourceArtifacts) == 0 {
			return Unverifiable("observation does not match the frozen dataset, split, protocol, code or environment")
		}
		if o.ExtractionMethod != "backend_recomputed" && o.ExtractionMethod != "deterministic_json" {
			return Unverifiable("model or unknown observation source cannot establish a numeric verdict")
		}
		if o.ExtractionMethod == "deterministic_json" {
			trust = "executed_program_reported_metric; deterministic_parser; not_independently_recomputed"
		}
		v, err := convert(o.Value, o.Unit, criterion.Unit)
		if err != nil {
			return Unverifiable(err.Error())
		}
		if c.Minimum != nil && v.Cmp(rat(*c.Minimum)) < 0 || c.Maximum != nil && v.Cmp(rat(*c.Maximum)) > 0 {
			return Unverifiable("observed value is outside the frozen metric range")
		}
		if boundedUnit(criterion.Unit) {
			upper := rat(1)
			if canonicalUnit(criterion.Unit) == "percent" {
				upper = rat(100)
			}
			if v.Sign() < 0 || v.Cmp(upper) > 0 {
				return Unverifiable("observed proportion is outside its legal range")
			}
		}
		values = append(values, v)
	}
	observed := new(big.Rat).Set(values[0])
	switch c.Aggregation {
	case "mean":
		for _, value := range values[1:] {
			observed.Add(observed, value)
		}
		observed.Quo(observed, big.NewRat(int64(len(values)), 1))
	case "minimum":
		for _, value := range values[1:] {
			if value.Cmp(observed) < 0 {
				observed.Set(value)
			}
		}
	case "maximum":
		for _, value := range values[1:] {
			if value.Cmp(observed) > 0 {
				observed.Set(value)
			}
		}
	}
	expected := rat(*criterion.ExpectedValue)
	allowance := new(big.Rat)
	if c.AbsoluteTolerance != nil {
		allowance.Set(rat(*c.AbsoluteTolerance))
	}
	if c.RelativeTolerance != nil {
		allowance.Add(allowance, new(big.Rat).Mul(rat(*c.RelativeTolerance), new(big.Rat).Abs(expected)))
	}
	comparison := 1
	switch c.Operator {
	case "within_tolerance":
		comparison = new(big.Rat).Abs(new(big.Rat).Sub(observed, expected)).Cmp(allowance)
	case "at_least":
		comparison = new(big.Rat).Sub(expected, allowance).Cmp(observed)
	case "at_most":
		comparison = observed.Cmp(new(big.Rat).Add(expected, allowance))
	}
	passed := comparison < 0 || *c.Inclusive && comparison == 0
	status, reason := models.ClaimStatusContradicted, "complete execution evidence does not satisfy the frozen numeric comparison"
	if passed {
		status, reason = models.ClaimStatusVerified, "execution observation satisfies the frozen numeric comparison"
	}
	value, _ := observed.Float64()
	if !Finite(value) {
		return Unverifiable("aggregated observation is non-finite")
	}
	return models.DeterministicClaimVerdict{VerifierVersion: Version, Status: status, Reason: reason, ExpectedValue: criterion.ExpectedValue, ObservedValue: &value, Unit: criterion.Unit, Operator: c.Operator, TrustBoundary: trust, ReviewRequired: trust != "backend_recomputed_from_execution_artifacts", ToleranceMode: c.ToleranceMode, AbsoluteTolerance: c.AbsoluteTolerance, RelativeTolerance: c.RelativeTolerance, Inclusive: c.Inclusive, Split: c.Split, ProtocolSHA256: c.ProtocolSHA256}
}

func rat(v float64) *big.Rat {
	r, _ := new(big.Rat).SetString(strconv.FormatFloat(v, 'g', -1, 64))
	return r
}
func canonicalUnit(u string) string {
	switch strings.ToLower(strings.TrimSpace(u)) {
	case "fraction", "ratio", "proportion":
		return "fraction"
	case "%", "percent", "percentage":
		return "percent"
	case "s", "second", "seconds":
		return "seconds"
	case "ms", "millisecond", "milliseconds":
		return "milliseconds"
	default:
		return strings.TrimSpace(u)
	}
}
func boundedUnit(unit string) bool {
	u := canonicalUnit(unit)
	return u == "fraction" || u == "percent"
}
func convert(value float64, from, to string) (*big.Rat, error) {
	from, to = canonicalUnit(from), canonicalUnit(to)
	if from == "" || to == "" {
		return nil, fmt.Errorf("metric units are missing")
	}
	v := rat(value)
	if from == to {
		return v, nil
	}
	switch {
	case from == "percent" && to == "fraction":
		return v.Quo(v, rat(100)), nil
	case from == "fraction" && to == "percent":
		return v.Mul(v, rat(100)), nil
	case from == "milliseconds" && to == "seconds":
		return v.Quo(v, rat(1000)), nil
	case from == "seconds" && to == "milliseconds":
		return v.Mul(v, rat(1000)), nil
	default:
		return nil, fmt.Errorf("incompatible metric units")
	}
}
