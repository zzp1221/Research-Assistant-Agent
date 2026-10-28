import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ModelProfile } from '../../src/contracts/api';

const profiles: ModelProfile[] = [
  {
    id: 'mine-1', owner_id: 'alice', global: false, name: '我的主模型', provider: 'openai_compatible',
    base_url: 'https://api.example.com/v1', model: 'deepseek-chat', api_key_masked: '****1234', api_key_last4: '1234',
    roles: ['main'], supports_vision: 'unknown', timeout_seconds: 120, revision: 1,
    created_at: '2026-10-27T00:00:00Z', updated_at: '2026-10-27T00:00:00Z',
  },
  {
    id: 'global-1', owner_id: '', global: true, name: '全局视觉模型', provider: 'openai_compatible',
    base_url: 'https://vision.example.com/v1', model: 'vision-1', api_key_masked: '****9999',
    roles: ['vision'], supports_vision: 'yes', timeout_seconds: 120, revision: 1,
    created_at: '2026-10-27T00:00:00Z', updated_at: '2026-10-27T00:00:00Z',
  },
];

const listModelProfiles = vi.fn();
const getModelRouting = vi.fn();
const saveModelRouting = vi.fn();
const createModelProfile = vi.fn();
const updateModelProfile = vi.fn();
const deleteModelProfile = vi.fn();
const testModelProfile = vi.fn();
const probeModelVision = vi.fn();

vi.mock('../../src/services/api/researchAssistantApi', () => ({
  listModelProfiles: () => listModelProfiles(),
  getModelRouting: () => getModelRouting(),
  saveModelRouting: (payload: unknown) => saveModelRouting(payload),
  createModelProfile: (payload: unknown) => createModelProfile(payload),
  updateModelProfile: (id: string, payload: unknown) => updateModelProfile(id, payload),
  deleteModelProfile: (id: string) => deleteModelProfile(id),
  testModelProfile: (id: string) => testModelProfile(id),
  probeModelVision: (id: string) => probeModelVision(id),
}));

const { ModelSettingsPanel } = await import('../../src/features/settings/ModelSettingsPanel');

beforeEach(() => {
  vi.clearAllMocks();
  listModelProfiles.mockResolvedValue({ profiles, encryption_available: true, is_admin: false, max_per_user: 10 });
  getModelRouting.mockResolvedValue({
    routing: { owner_id: 'alice', main_profile_id: 'mine-1', vision_profile_ids: ['global-1'] },
    global_routing: { owner_id: '' },
  });
  saveModelRouting.mockResolvedValue({ owner_id: 'alice', main_profile_id: 'mine-1', vision_profile_ids: ['global-1'] });
  createModelProfile.mockResolvedValue(profiles[0]);
  updateModelProfile.mockResolvedValue(profiles[0]);
  deleteModelProfile.mockResolvedValue({ deleted: true });
  testModelProfile.mockResolvedValue({ ok: true, status: 'ok', latency_ms: 12 });
  probeModelVision.mockResolvedValue({ status: 'yes' });
});

describe('ModelSettingsPanel', () => {
  it('lists personal and global profiles with the masked key only', async () => {
    render(<ModelSettingsPanel />);
    expect(await screen.findByText('我的主模型')).toBeTruthy();
    expect(screen.getByText('全局视觉模型')).toBeTruthy();
    expect(screen.getByText(/\*\*\*\*1234/)).toBeTruthy();
    expect(document.body.textContent).not.toContain('sk-verysecret');
    expect(screen.getByText('视觉：是')).toBeTruthy();
    expect(screen.getByText('视觉：未知')).toBeTruthy();
  });

  it('opens the create form with a write-only api key field', async () => {
    const user = userEvent.setup();
    render(<ModelSettingsPanel />);
    await screen.findByText('我的主模型');
    await user.click(screen.getByRole('button', { name: /添加模型/ }));
    expect(screen.getByText(/API Key（只写不回显）/)).toBeTruthy();
    const password = document.querySelector('input[type="password"]') as HTMLInputElement;
    expect(password).toBeTruthy();
    expect(password.value).toBe('');
  });

  it('tests a profile and reports the latency', async () => {
    const user = userEvent.setup();
    render(<ModelSettingsPanel />);
    await screen.findByText('我的主模型');
    await user.click(screen.getAllByRole('button', { name: /测试文本/ })[0]);
    expect(await screen.findByText(/文本连通性正常/)).toBeTruthy();
  });

  it('shows the privacy and cost notices', async () => {
    render(<ModelSettingsPanel />);
    await screen.findByText('我的主模型');
    expect(screen.getByText(/视觉判断会为每张图额外消耗一次多模态调用/)).toBeTruthy();
    expect(screen.getByText(/图像、相关代码与执行日志会发送到所选 provider/)).toBeTruthy();
  });
});
