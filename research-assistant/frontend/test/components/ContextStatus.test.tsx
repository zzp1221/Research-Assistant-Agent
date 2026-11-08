import { render, screen } from '@testing-library/react';
import { expect, it } from 'vitest';
import { ContextStatus } from '../../src/features/chat/ContextStatus';

it('shows unknown opaque counts and costs without a fabricated percentage', () => {
  const { container } = render(<ContextStatus report={{ profile_id: 'p', profile_revision: 2, model: 'model', capacity: 32000, input_budget: 22208, compaction_mode: 'standalone', generation: 3, input_observation: { source: 'unknown', confidence: 'unknown' }, last_compaction: { mode: 'standalone', status: 'completed', request_attempts: 1, latency_ms: 200, at: '2026-11-07T00:00:00Z' } }} />);
  expect(screen.getByText(/本次输入：未知/)).toBeTruthy();
  expect(screen.getByText(/成本未知/)).toBeTruthy();
  expect(container.textContent).not.toContain('%');
  expect(container.querySelector('[role="progressbar"]')).toBeNull();
});
