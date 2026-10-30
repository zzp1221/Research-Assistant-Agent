import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { CostConsolePanel } from '../../src/features/cost/CostConsolePanel';

vi.mock('../../src/services/api/researchAssistantApi', () => ({
  getUsageSummary: vi.fn(async () => ({
    owner_id: 'owner-a',
    period: 'day',
    today_usd: 0.25,
    month_usd: 1.5,
    spent_usd: 0.25,
    reserved_usd: 0.05,
    limit_usd: 5,
    remaining_usd: 4.7,
    policy: 'soft',
    configured: true,
    top_plans: [{ plan_id: 'plan-1', cost_usd: 0.2, calls: 3 }],
    top_models: [{ model: 'deepseek-chat', cost_usd: 0.2, calls: 3 }],
  })),
  getBudget: vi.fn(async () => ({ owner_id: 'owner-a', period: 'day', limit_usd: 5, policy: 'soft', updated_at: '2026-10-28T10:00:00Z' })),
  getBudgetAlerts: vi.fn(async () => []),
  getUsageTimeseries: vi.fn(async () => [{ key: '2026-10-28', cost_usd: 0.25, calls: 3 }]),
  saveBudget: vi.fn(async (payload) => ({ owner_id: 'owner-a', ...payload, updated_at: '2026-10-28T10:00:00Z' })),
}));

afterEach(() => {
  vi.clearAllMocks();
});

describe('CostConsolePanel', () => {
  it('renders today, month, budget progress and top lists', async () => {
    render(<CostConsolePanel onClose={() => undefined} />);
    expect(await screen.findByText('$0.2500')).toBeInTheDocument();
    expect(screen.getByText(/1\.5000/)).toBeInTheDocument();
    expect(screen.getAllByText(/5\.0000/).length).toBeGreaterThan(0);
    expect(screen.getByText('$4.7000')).toBeInTheDocument();
    expect(screen.getByText('plan-1')).toBeInTheDocument();
    expect(screen.getByText('deepseek-chat')).toBeInTheDocument();
    expect(screen.getByText(/6%（已用/)).toBeInTheDocument();
  });

  it('saves a budget change', async () => {
    const api = await import('../../src/services/api/researchAssistantApi');
    render(<CostConsolePanel onClose={() => undefined} />);
    const input = await screen.findByLabelText('预算限额（USD）');
    await userEvent.clear(input);
    await userEvent.type(input, '12');
    await userEvent.selectOptions(screen.getByLabelText('超预算策略'), 'hard');
    await userEvent.click(screen.getByRole('button', { name: '保存预算' }));
    await waitFor(() => expect(api.saveBudget).toHaveBeenCalledWith({ period: 'day', limit_usd: 12, policy: 'hard' }));
  });
});
