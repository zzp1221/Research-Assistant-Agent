# ADR 000: <title>

- Status: proposed
- Date: YYYY-MM-DD
- Owners: <name>
- Related: <links>

## Context

What user/research problem is being solved? What is the current behavior?

## Data flow

- Inputs: where does data come from? (ResearchContext attachment/document/paper_metadata/artifact)
- Outputs: which artifact keys and OutputKind?
- Ownership: who produces and who consumes each artifact?

## Contract

- OutputKind: final_answer / evidence_report / metrics / benchmark_report / plan / artifact_ref
- EvidenceClass: execution_derived / paper_derived / llm_inference
- Required input bindings and schemas.

## Failure modes

- Missing attachment/document/paper_metadata: clarification_required or blocked.
- Missing dependency/GPU/dataset/model: blocked with MissingRequirement.
- Mock/demo: must be explicit, outcome=mock, never completed as a real result.

## Acceptance

- Contract tests:
- Semantic E2E:
- Old-plan compatibility:
