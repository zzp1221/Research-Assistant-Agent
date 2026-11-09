import { afterEach, describe, expect, it, vi } from 'vitest';
import { render, screen, within } from '@testing-library/react';
import { ClaimEvidenceGraphView } from '../../src/features/claim-evidence/ClaimEvidenceGraphView';

describe('Claim evidence numeric assessment', () => {
  afterEach(() => vi.unstubAllGlobals());
  it('separates backend observations, model suggestions and review requirements', () => {
    vi.stubGlobal('ResizeObserver', class {
      observe() {}
      unobserve() {}
      disconnect() {}
    });
    const graph = {
      version: 'claim.evidence/v1', status: 'degraded', status_reason: 'external metric requires review', rubric_sha256: 'frozen-rubric', evidence: [],
      summary: { total_claims: 1, total_criteria: 1, verified: 0, partially_reproduced: 0, contradicted: 0, unverifiable: 1, blocked_by_missing_asset: 0, criterion_evidence_coverage: 0 },
      claims: [{ claim_id: 'claim-1', title: 'Accuracy', statement: 'Frozen test accuracy', source_locator: 'Table 1', claim_type: 'quantitative', status: 'unverifiable', confidence: 0,
        criteria: [{ criterion_id: 'criterion-1', description: 'Test accuracy must meet the frozen target', status: 'unverifiable', confidence: 0, evidence_ids: [], reason: 'Independent verification is missing',
          deterministic_verdict: { status: 'contradicted', expected_value: 0.85, observed_value: 0.817, unit: 'fraction', tolerance_mode: 'absolute', absolute_tolerance: 0.01, split: 'test', review_required: true },
          model_proposal: { status: 'verified', observed_value: 'model claimed 0.999' },
        }],
      }],
    };
    render(<ClaimEvidenceGraphView rawGraph={JSON.stringify(graph)} />);
    const assessment = screen.getByRole('region', { name: '数值准则 criterion-1', hidden: true });
    expect(within(assessment).getByText('0.85 fraction')).toBeInTheDocument();
    expect(within(assessment).getByText('0.817 fraction')).toBeInTheDocument();
    expect(within(assessment).getByText('绝对 0.01')).toBeInTheDocument();
    expect(within(assessment).getByText(/最终判定：不可验证/)).toBeInTheDocument();
    expect(within(assessment).getByText(/运行程序自报指标/)).toBeInTheDocument();
    expect(within(assessment).getByText(/模型建议：已验证；model claimed 0.999/)).toBeInTheDocument();
    expect(within(assessment).getByText('Independent verification is missing')).toBeInTheDocument();
  });
});
