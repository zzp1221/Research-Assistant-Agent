import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { AgentSettingsPanel } from '../../src/features/settings/AgentSettingsPanel';
import * as api from '../../src/services/api/researchAssistantApi';

vi.mock('../../src/services/api/researchAssistantApi', () => ({
  activatePromptVersion: vi.fn(), createAgentProfile: vi.fn(), createPromptVersion: vi.fn(), deleteAgentProfile: vi.fn(),
  evaluateAgentProfile: vi.fn(), evaluatePromptVersion: vi.fn(), listAgentProfiles: vi.fn(), listModelProfiles: vi.fn(), listPromptVersions: vi.fn(), listTools: vi.fn(), updateAgentProfile: vi.fn(),
}));

beforeEach(() => {
  vi.mocked(api.listAgentProfiles).mockResolvedValue([]);
  vi.mocked(api.listModelProfiles).mockResolvedValue({ profiles: [] } as Awaited<ReturnType<typeof api.listModelProfiles>>);
  vi.mocked(api.listTools).mockResolvedValue({ tools: [] } as Awaited<ReturnType<typeof api.listTools>>);
  vi.mocked(api.listPromptVersions).mockResolvedValue([{ id: 'prompt', name: 'Analysis', version: 1, template: 'Answer {{task}}', variables: ['task'], status: 'draft', created_at: '' }]);
});

it('sends an explicit template grader and displays errors with N/A', async () => {
  vi.mocked(api.evaluatePromptVersion).mockResolvedValue({ prompt_version_id: 'prompt', mode: 'template', score: 0, score_available: false, scored_rate: null, planned_rate: 0, passed: 0, total: 1, scored: 0, attempted: 0, failed: 0, errors: 1, skipped: 0, grader_version: 'agent-eval/v1', evaluated_at: '', cases: [{ name: 'test', status: 'error', mode: 'template', grader_version: 'agent-eval/v1', passed: false, score: 0, attempted: false, model_invoked: false, detail: 'no grader' }] });
  render(<AgentSettingsPanel />);
  fireEvent.click(await screen.findByRole('button', { name: '评测' }));
  await waitFor(() => expect(api.evaluatePromptVersion).toHaveBeenCalledWith('prompt', { mode: 'template', cases: [{ name: 'variable-bindings', variables: { task: 'sample' }, check_rendering: true }], model_profile_id: undefined }));
  expect((await screen.findAllByText(/得分 N\/A/)).length).toBeGreaterThan(0);
  expect(screen.getByText(/test:error \(no grader\)/)).toBeTruthy();
});

it('requires explicit cases before invoking a real model', async () => {
  render(<AgentSettingsPanel />);
  await screen.findByRole('button', { name: '评测' });
  fireEvent.change(screen.getByLabelText('评测模式'), { target: { value: 'model' } });
  fireEvent.click(screen.getByRole('button', { name: '评测' }));
  expect(await screen.findByText('模型与任务评测需要显式用例和判分条件')).toBeTruthy();
});
