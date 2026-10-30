import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { PlanLibraryPanel } from '../../src/features/plan-library/PlanLibraryPanel';

vi.mock('../../src/services/api/researchAssistantApi', () => ({
  listPlanLibrary: vi.fn(async () => ({
    plans: [
      {
        id: 'plan-1',
        title: 'Attention reproduction',
        intent_type: 'Paper_Reproduction',
        status: 'completed',
        outcome: 'completed',
        project_id: 'project-1',
        total_nodes: 8,
        completed_nodes: 8,
        failed_nodes: 0,
        created_at: '2026-10-28T10:00:00Z',
        updated_at: '2026-10-28T11:00:00Z',
        usage: { llm_cost_usd: 0.42, sandbox_seconds: 65 },
      },
    ],
    total: 1,
    next_cursor: '',
  })),
  listProjects: vi.fn(async () => [
    { id: 'project-1', name: 'Attention Study', created_at: '2026-10-01T00:00:00Z', updated_at: '2026-10-01T00:00:00Z' },
  ]),
  createProject: vi.fn(async (payload: { name: string }) => ({ id: 'project-2', name: payload.name, created_at: '2026-10-29T00:00:00Z', updated_at: '2026-10-29T00:00:00Z' })),
  deleteProject: vi.fn(),
  deletePlan: vi.fn(),
  duplicatePlan: vi.fn(),
  patchPlan: vi.fn(),
  updateProject: vi.fn(),
}));

vi.mock('../../src/config/env', () => ({
  API_BASE_URL: 'http://127.0.0.1:8080',
  API_AUTH_TOKEN: '',
}));

afterEach(() => {
  vi.clearAllMocks();
});

describe('PlanLibraryPanel', () => {
  it('renders plan cards with project, cost and status', async () => {
    render(<PlanLibraryPanel onClose={() => undefined} onOpenPlan={() => undefined} />);
    expect(await screen.findByText('Attention reproduction')).toBeInTheDocument();
    expect(screen.getByText(/Paper_Reproduction · Attention Study/)).toBeInTheDocument();
    expect(screen.getByText('$0.4200')).toBeInTheDocument();
    expect(screen.getByText(/8\/8 节点/)).toBeInTheDocument();
  });

  it('opens a plan when the card title is clicked', async () => {
    const onOpenPlan = vi.fn();
    const onClose = vi.fn();
    render(<PlanLibraryPanel onClose={onClose} onOpenPlan={onOpenPlan} />);
    const title = await screen.findByRole('button', { name: /Attention reproduction/ });
    await userEvent.click(title);
    expect(onOpenPlan).toHaveBeenCalledWith('plan-1');
    expect(onClose).toHaveBeenCalled();
  });

  it('filters by query and status', async () => {
    const api = await import('../../src/services/api/researchAssistantApi');
    render(<PlanLibraryPanel onClose={() => undefined} onOpenPlan={() => undefined} />);
    await screen.findByText('Attention reproduction');
    const search = screen.getByLabelText('搜索计划');
    await userEvent.type(search, 'attention');
    await userEvent.selectOptions(screen.getByLabelText('按状态筛选'), 'failed');
    await userEvent.click(screen.getByRole('button', { name: '搜索' }));
    await waitFor(() => expect(api.listPlanLibrary).toHaveBeenLastCalledWith(expect.objectContaining({ q: 'attention', status: 'failed' })));
  });

  it('creates a project from the visible form', async () => {
    const api = await import('../../src/services/api/researchAssistantApi');
    render(<PlanLibraryPanel onClose={() => undefined} onOpenPlan={() => undefined} />);
    await screen.findByText('Attention reproduction');
    const input = screen.getByLabelText('新建项目');
    await userEvent.type(input, 'New Project');
    const create = screen.getByRole('button', { name: '创建项目' });
    expect(create).toBeEnabled();
    await userEvent.click(create);
    await waitFor(() => expect(api.createProject).toHaveBeenCalledWith({ name: 'New Project' }));
    expect(await screen.findByText(/已创建项目：New Project/)).toBeInTheDocument();
    expect(input).toHaveValue('');
  });
});
