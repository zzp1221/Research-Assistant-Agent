import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  AlertTriangle,
  CheckCircle2,
  CircleDashed,
  Eye,
  KeyRound,
  Loader2,
  Plus,
  RefreshCw,
  Save,
  Settings2,
  Trash2,
  XCircle,
} from 'lucide-react';
import type {
  ModelProfile,
  ModelProfilePayload,
  ModelRouting,
} from '../../contracts/api';
import {
  createModelProfile,
  deleteModelProfile,
  getModelRouting,
  listModelProfiles,
  probeModelVision,
  saveModelRouting,
  testModelProfile,
  updateModelProfile,
} from '../../services/api/researchAssistantApi';

type BusyState = { kind: 'test' | 'probe' | 'save' | 'delete'; id?: string } | null;

interface ModelSettingsPanelProps {
  onClose?: () => void;
}

const visionBadge = (profile: ModelProfile) => {
  switch (profile.supports_vision) {
    case 'yes':
      return { label: '视觉：是', className: 'text-ok border-ok/40 bg-ok-soft', icon: <CheckCircle2 className="h-3 w-3" /> };
    case 'no':
      return { label: '视觉：否', className: 'text-proof border-proof/40 bg-proof-soft', icon: <XCircle className="h-3 w-3" /> };
    default:
      return { label: '视觉：未知', className: 'text-warn border-warn/40 bg-warn-soft', icon: <CircleDashed className="h-3 w-3" /> };
  }
};

const emptyDraft = (): ModelProfilePayload => ({
  name: '',
  provider: 'openai_compatible',
  base_url: '',
  model: '',
  api_key: '',
  roles: ['main'],
  supports_vision: 'unknown',
  timeout_seconds: 120,
});

/** Model profile settings: per-user profiles plus the read-only global list. */
export function ModelSettingsPanel({ onClose }: ModelSettingsPanelProps) {
  const [profiles, setProfiles] = useState<ModelProfile[]>([]);
  const [routing, setRouting] = useState<ModelRouting | null>(null);
  const [globalRouting, setGlobalRouting] = useState<ModelRouting | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState<BusyState>(null);
  const [draft, setDraft] = useState<ModelProfilePayload>(emptyDraft);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [formOpen, setFormOpen] = useState(false);
  const [encryptionAvailable, setEncryptionAvailable] = useState(true);
  const [isAdmin, setIsAdmin] = useState(false);
  const [makeGlobal, setMakeGlobal] = useState(false);

  const reload = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const [profileResponse, routingResponse] = await Promise.all([listModelProfiles(), getModelRouting()]);
      setProfiles(profileResponse.profiles ?? []);
      setEncryptionAvailable(profileResponse.encryption_available !== false);
      setIsAdmin(Boolean(profileResponse.is_admin));
      setRouting(routingResponse.routing ?? null);
      setGlobalRouting(routingResponse.global_routing ?? null);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : '加载模型配置失败');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  const myProfiles = useMemo(() => profiles.filter((profile) => !profile.global), [profiles]);
  const globalProfiles = useMemo(() => profiles.filter((profile) => profile.global), [profiles]);

  const openCreate = () => {
    setEditingId(null);
    setDraft(emptyDraft());
    setMakeGlobal(false);
    setFormOpen(true);
    setNotice('');
  };

  const openEdit = (profile: ModelProfile) => {
    setEditingId(profile.id);
    setDraft({
      name: profile.name,
      provider: profile.provider,
      base_url: profile.base_url,
      model: profile.model,
      api_key: '',
      roles: profile.roles ?? ['main'],
      supports_vision: profile.supports_vision,
      timeout_seconds: profile.timeout_seconds,
      max_output_tokens: profile.max_output_tokens,
      context_window_tokens: profile.context_window_tokens,
      prompt_price_per_1k: profile.prompt_price_per_1k,
      completion_price_per_1k: profile.completion_price_per_1k,
    });
    setMakeGlobal(profile.global);
    setFormOpen(true);
    setNotice('');
  };

  const save = async () => {
    setBusy({ kind: 'save' });
    setError('');
    try {
      if (editingId) {
        await updateModelProfile(editingId, draft);
        setNotice('模型配置已更新，下一次任务立即生效。');
      } else {
        await createModelProfile({ ...draft, global: makeGlobal });
        setNotice('模型配置已创建。');
      }
      setFormOpen(false);
      await reload();
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : '保存模型配置失败');
    } finally {
      setBusy(null);
    }
  };

  const remove = async (profile: ModelProfile) => {
    if (!window.confirm(`删除模型配置「${profile.name}」？被路由引用的配置需先解除引用。`)) return;
    setBusy({ kind: 'delete', id: profile.id });
    setError('');
    try {
      await deleteModelProfile(profile.id);
      setNotice('模型配置已删除。');
      await reload();
    } catch (deleteError) {
      setError(deleteError instanceof Error ? deleteError.message : '删除失败');
    } finally {
      setBusy(null);
    }
  };

  const runTest = async (profile: ModelProfile) => {
    setBusy({ kind: 'test', id: profile.id });
    setError('');
    try {
      const result = await testModelProfile(profile.id);
      setNotice(result.ok ? `文本连通性正常（${result.latency_ms ?? 0}ms）` : `文本测试失败：${result.detail ?? result.status}`);
    } catch (testError) {
      setError(testError instanceof Error ? testError.message : '测试失败');
    } finally {
      setBusy(null);
    }
  };

  const runProbe = async (profile: ModelProfile) => {
    setBusy({ kind: 'probe', id: profile.id });
    setError('');
    try {
      const result = await probeModelVision(profile.id);
      setNotice(`视觉探测：${result.status}${result.detail ? `（${result.detail}）` : ''}`);
      await reload();
    } catch (probeError) {
      setError(probeError instanceof Error ? probeError.message : '视觉探测失败');
    } finally {
      setBusy(null);
    }
  };

  const saveRouting = async (nextMain: string, nextVision: string[], nextFallback: string[]) => {
    setBusy({ kind: 'save' });
    setError('');
    try {
      await saveModelRouting({ main_profile_id: nextMain, vision_profile_ids: nextVision, fallback_profile_ids: nextFallback });
      setNotice('模型路由已保存，下一次任务立即生效。');
      await reload();
    } catch (routingError) {
      setError(routingError instanceof Error ? routingError.message : '保存路由失败');
    } finally {
      setBusy(null);
    }
  };

  const toggleVision = (profileId: string) => {
    if (!routing) return;
    const current = routing.vision_profile_ids ?? [];
    const next = current.includes(profileId)
      ? current.filter((id) => id !== profileId)
      : [...current, profileId];
    void saveRouting(routing.main_profile_id, next, routing.fallback_profile_ids ?? []);
  };

  const card = (profile: ModelProfile) => {
    const badge = visionBadge(profile);
    return (
      <article key={profile.id} className="border border-line bg-surface p-3" data-testid="model-profile-card">
        <div className="flex flex-wrap items-start justify-between gap-2">
          <div className="min-w-0">
            <h4 className="truncate font-serif text-[14px] font-semibold text-ink">{profile.name}</h4>
            <p className="mt-0.5 journal-num text-[11px] text-muted">
              {profile.provider} · {profile.model} · {profile.api_key_masked || '未设置密钥'}
            </p>
          </div>
          <span className={`inline-flex items-center gap-1 border px-1.5 py-0.5 journal-caps text-[10px] ${badge.className}`}>
            {badge.icon}
            {badge.label}
          </span>
        </div>
        <p className="mt-1 font-serif text-[11.5px] leading-5 text-ink-soft">
          超时 {profile.timeout_seconds}s
          {profile.prompt_price_per_1k ? ` · 输入 ${profile.prompt_price_per_1k} USD/1k` : ''}
          {profile.completion_price_per_1k ? ` · 输出 ${profile.completion_price_per_1k} USD/1k` : ''}
          {profile.vision_probed_at ? ` · 最近探测 ${new Date(profile.vision_probed_at).toLocaleString('zh-CN')}` : ''}
        </p>
        {profile.vision_probe_reason ? (
          <p className="mt-1 break-words font-serif text-[11px] leading-5 text-muted">{profile.vision_probe_reason}</p>
        ) : null}
        <div className="mt-2 flex flex-wrap gap-1.5">
          {!profile.global && (
            <button type="button" onClick={() => openEdit(profile)} className="h-6 border border-line-strong px-2 text-[11.5px] text-ink-soft hover:border-ink hover:text-ink">
              编辑
            </button>
          )}
          <button
            type="button"
            onClick={() => void runTest(profile)}
            disabled={Boolean(busy)}
            className="inline-flex h-6 items-center gap-1 border border-line-strong px-2 text-[11.5px] text-ink-soft hover:border-ink hover:text-ink disabled:opacity-50"
          >
            {busy?.kind === 'test' && busy.id === profile.id ? <Loader2 className="h-3 w-3 animate-spin" /> : null}
            测试文本
          </button>
          <button
            type="button"
            onClick={() => void runProbe(profile)}
            disabled={Boolean(busy)}
            className="inline-flex h-6 items-center gap-1 border border-line-strong px-2 text-[11.5px] text-ink-soft hover:border-ink hover:text-ink disabled:opacity-50"
          >
            {busy?.kind === 'probe' && busy.id === profile.id ? <Loader2 className="h-3 w-3 animate-spin" /> : <Eye className="h-3 w-3" />}
            探测视觉
          </button>
          {!profile.global && (
            <button
              type="button"
              onClick={() => void remove(profile)}
              disabled={Boolean(busy)}
              className="inline-flex h-6 items-center gap-1 border border-proof/40 px-2 text-[11.5px] text-proof hover:bg-proof-soft disabled:opacity-50"
            >
              <Trash2 className="h-3 w-3" />
              删除
            </button>
          )}
        </div>
      </article>
    );
  };

  return (
    <section className="flex h-full flex-col overflow-hidden bg-paper" aria-label="模型设置">
      <header className="flex shrink-0 items-start justify-between gap-3 border-b border-line bg-surface px-4 py-3">
        <div>
          <div className="journal-caps text-[10px]">工作台设置 · 模型</div>
          <h2 className="mt-0.5 flex items-center gap-1.5 font-serif text-[16px] font-semibold text-ink">
            <Settings2 className="h-4 w-4" />
            运行期模型配置
          </h2>
          <p className="mt-1 max-w-2xl font-serif text-[12px] leading-5 text-ink-soft">
            主模型负责文本与报告，视觉模型只负责判图。保存后无需重启，下一次任务立即生效；运行中的计划继续使用创建时的 profile 快照。
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-1.5">
          <button type="button" onClick={() => void reload()} className="inline-flex h-7 items-center gap-1 border border-line-strong px-2 text-[12px] text-ink-soft hover:border-ink hover:text-ink">
            <RefreshCw className="h-3 w-3" />
            刷新
          </button>
          {onClose && (
            <button type="button" onClick={onClose} className="h-7 border border-line-strong px-2 text-[12px] text-ink-soft hover:border-ink hover:text-ink">
              关闭
            </button>
          )}
        </div>
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3">
        {!encryptionAvailable && (
          <div className="mb-3 flex items-start gap-2 border border-warn/40 bg-warn-soft px-3 py-2">
            <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0 text-warn" />
            <p className="font-serif text-[12px] leading-5 text-warn">
              服务端未配置 MODEL_PROFILE_ENCRYPTION_KEY，无法保存 API Key。管理员配置 32 字节密钥后即可启用运行期模型配置。
            </p>
          </div>
        )}
        {error && <div className="mb-3 border border-proof/40 bg-proof-soft px-3 py-2 font-serif text-[12px] text-proof">{error}</div>}
        {notice && <div className="mb-3 border border-ok/40 bg-ok-soft px-3 py-2 font-serif text-[12px] text-ok">{notice}</div>}
        {loading ? (
          <div className="flex items-center gap-2 py-8 text-[12px] text-muted">
            <Loader2 className="h-4 w-4 animate-spin" />
            正在加载模型配置…
          </div>
        ) : (
          <>
            <div className="mb-2 flex items-baseline justify-between gap-2">
              <h3 className="journal-caps text-[10.5px] font-semibold text-ink">我的模型 · {myProfiles.length}</h3>
              <button
                type="button"
                onClick={openCreate}
                disabled={!encryptionAvailable}
                className="inline-flex h-7 items-center gap-1 bg-ink px-2.5 text-[12px] font-semibold text-paper hover:bg-black disabled:cursor-not-allowed disabled:opacity-50"
              >
                <Plus className="h-3.5 w-3.5" />
                添加模型
              </button>
            </div>
            {myProfiles.length === 0 ? (
              <p className="border border-dashed border-line px-3 py-4 font-serif text-[12px] text-muted">
                还没有个人模型配置。可以添加多个 OpenAI-compatible profile，并指定哪个负责判图。
              </p>
            ) : (
              <div className="grid gap-2 md:grid-cols-2">{myProfiles.map(card)}</div>
            )}

            <h3 className="mb-2 mt-5 journal-caps text-[10.5px] font-semibold text-ink">
              全局模型 · {globalProfiles.length}（只读{isAdmin ? '，管理员可编辑' : ''}）
            </h3>
            {globalProfiles.length === 0 ? (
              <p className="border border-dashed border-line px-3 py-4 font-serif text-[12px] text-muted">管理员尚未配置全局模型。</p>
            ) : (
              <div className="grid gap-2 md:grid-cols-2">{globalProfiles.map(card)}</div>
            )}

            <h3 className="mb-2 mt-5 journal-caps text-[10.5px] font-semibold text-ink">模型路由</h3>
            <div className="grid gap-2 border border-line bg-surface p-3 md:grid-cols-2">
              <label className="block">
                <span className="journal-caps text-[10px] text-muted">主模型（文本与报告）</span>
                <select
                  value={routing?.main_profile_id ?? ''}
                  onChange={(event) => void saveRouting(event.target.value, routing?.vision_profile_ids ?? [], routing?.fallback_profile_ids ?? [])}
                  className="mt-1 h-8 w-full border border-line-strong bg-paper px-2 text-[12px] text-ink"
                >
                  <option value="">未指定（使用该账号第一个 main 配置）</option>
                  {profiles.map((profile) => (
                    <option key={profile.id} value={profile.id}>
                      {profile.global ? '[全局] ' : ''}{profile.name} · {profile.model}
                    </option>
                  ))}
                </select>
              </label>
              <div>
                <span className="journal-caps text-[10px] text-muted">视觉模型（按优先级判图）</span>
                <div className="mt-1 space-y-1">
                  {profiles.filter((profile) => !routing?.vision_profile_ids?.includes(profile.id)).length === 0 && (routing?.vision_profile_ids?.length ?? 0) === 0 && (
                    <p className="font-serif text-[11.5px] text-muted">未选择；没有视觉模型时校验结果为“不可确认”。</p>
                  )}
                  {(routing?.vision_profile_ids ?? []).map((id, index) => {
                    const profile = profiles.find((candidate) => candidate.id === id);
                    if (!profile) return null;
                    return (
                      <div key={id} className="flex items-center justify-between border border-line px-2 py-1">
                        <span className="truncate font-serif text-[12px] text-ink-soft">
                          {index + 1}. {profile.name} · {profile.supports_vision === 'yes' ? '可判图' : profile.supports_vision === 'no' ? '不支持视觉' : '未探测'}
                        </span>
                        <button type="button" onClick={() => toggleVision(id)} className="text-[11px] text-proof hover:underline">
                          移除
                        </button>
                      </div>
                    );
                  })}
                  <select
                    value=""
                    onChange={(event) => {
                      if (!event.target.value) return;
                      const next = [...(routing?.vision_profile_ids ?? []), event.target.value];
                      void saveRouting(routing?.main_profile_id ?? '', next, routing?.fallback_profile_ids ?? []);
                    }}
                    className="mt-1 h-8 w-full border border-line-strong bg-paper px-2 text-[12px] text-ink"
                  >
                    <option value="">+ 添加视觉候选</option>
                    {profiles
                      .filter((profile) => !routing?.vision_profile_ids?.includes(profile.id))
                      .map((profile) => (
                        <option key={profile.id} value={profile.id}>
                          {profile.global ? '[全局] ' : ''}{profile.name} · {profile.model}
                        </option>
                      ))}
                  </select>
                </div>
              </div>
              {globalRouting && globalRouting.main_profile_id && (
                <p className="md:col-span-2 font-serif text-[11.5px] leading-5 text-muted">
                  全局默认路由：{profiles.find((profile) => profile.id === globalRouting.main_profile_id)?.name ?? globalRouting.main_profile_id}
                </p>
              )}
            </div>

            <div className="mt-4 space-y-1 border-t border-line pt-3 font-serif text-[11.5px] leading-5 text-muted">
              <p className="inline-flex items-start gap-1.5">
                <KeyRound className="mt-0.5 h-3 w-3 shrink-0" />
                API Key 只写不回显：保存后仅显示末四位，明文不会写入日志或计划快照。
              </p>
              <p>成本提示：视觉判断会为每张图额外消耗一次多模态调用，注意各配置的单价。</p>
              <p>隐私提示：启用视觉校验后，图像、相关代码与执行日志会发送到所选 provider。</p>
            </div>
          </>
        )}
      </div>

      {formOpen && (
        <div className="absolute inset-0 z-30 flex items-start justify-center overflow-y-auto bg-ink/20 p-4" role="dialog" aria-modal="true" aria-label="模型配置表单">
          <div className="w-full max-w-xl border border-ink bg-surface shadow-xl">
            <header className="flex items-center justify-between border-b border-line px-4 py-2.5">
              <h3 className="font-serif text-[15px] font-semibold text-ink">{editingId ? '编辑模型配置' : '添加模型配置'}</h3>
              <button type="button" onClick={() => setFormOpen(false)} className="h-7 w-7 border border-transparent text-muted hover:border-line-strong hover:text-ink" aria-label="关闭表单">
                ×
              </button>
            </header>
            <div className="grid gap-2.5 px-4 py-3 md:grid-cols-2">
              <label className="block">
                <span className="journal-caps text-[10px] text-muted">名称</span>
                <input value={draft.name ?? ''} onChange={(event) => setDraft({ ...draft, name: event.target.value })} className="mt-1 h-8 w-full border border-line-strong bg-paper px-2 text-[12px] text-ink" />
              </label>
              <label className="block">
                <span className="journal-caps text-[10px] text-muted">模型标识</span>
                <input value={draft.model ?? ''} onChange={(event) => setDraft({ ...draft, model: event.target.value })} placeholder="deepseek-chat" className="mt-1 h-8 w-full border border-line-strong bg-paper px-2 text-[12px] text-ink" />
              </label>
              <label className="block md:col-span-2">
                <span className="journal-caps text-[10px] text-muted">Base URL（OpenAI 兼容，/v1）</span>
                <input value={draft.base_url ?? ''} onChange={(event) => setDraft({ ...draft, base_url: event.target.value })} placeholder="https://api.example.com/v1" className="mt-1 h-8 w-full border border-line-strong bg-paper px-2 text-[12px] text-ink" />
              </label>
              <label className="block md:col-span-2">
                <span className="journal-caps text-[10px] text-muted">API Key（{editingId ? '留空表示不变' : '只写不回显'}）</span>
                <input type="password" value={draft.api_key ?? ''} onChange={(event) => setDraft({ ...draft, api_key: event.target.value })} autoComplete="new-password" className="mt-1 h-8 w-full border border-line-strong bg-paper px-2 text-[12px] text-ink" />
              </label>
              <label className="block">
                <span className="journal-caps text-[10px] text-muted">超时（秒）</span>
                <input type="number" min={5} value={draft.timeout_seconds ?? 120} onChange={(event) => setDraft({ ...draft, timeout_seconds: Number(event.target.value) })} className="mt-1 h-8 w-full border border-line-strong bg-paper px-2 text-[12px] text-ink" />
              </label>
              <label className="block">
                <span className="journal-caps text-[10px] text-muted">上下文窗口（tokens）</span>
                <input type="number" min={1024} step={1024} value={draft.context_window_tokens ?? 128000} onChange={(event) => setDraft({ ...draft, context_window_tokens: Number(event.target.value) })} className="mt-1 h-8 w-full border border-line-strong bg-paper px-2 text-[12px] text-ink" />
              </label>
              <label className="block">
                <span className="journal-caps text-[10px] text-muted">视觉能力（人工覆盖）</span>
                <select value={draft.supports_vision ?? 'unknown'} onChange={(event) => setDraft({ ...draft, supports_vision: event.target.value })} className="mt-1 h-8 w-full border border-line-strong bg-paper px-2 text-[12px] text-ink">
                  <option value="unknown">unknown（运行时尝试）</option>
                  <option value="yes">yes</option>
                  <option value="no">no</option>
                </select>
              </label>
              <label className="block">
                <span className="journal-caps text-[10px] text-muted">输入单价 / 1k tokens</span>
                <input type="number" min={0} step={0.0001} value={draft.prompt_price_per_1k ?? 0} onChange={(event) => setDraft({ ...draft, prompt_price_per_1k: Number(event.target.value) })} className="mt-1 h-8 w-full border border-line-strong bg-paper px-2 text-[12px] text-ink" />
              </label>
              <label className="block">
                <span className="journal-caps text-[10px] text-muted">输出单价 / 1k tokens</span>
                <input type="number" min={0} step={0.0001} value={draft.completion_price_per_1k ?? 0} onChange={(event) => setDraft({ ...draft, completion_price_per_1k: Number(event.target.value) })} className="mt-1 h-8 w-full border border-line-strong bg-paper px-2 text-[12px] text-ink" />
              </label>
              {isAdmin && (
                <label className="inline-flex items-center gap-1.5 md:col-span-2">
                  <input type="checkbox" checked={makeGlobal} onChange={(event) => setMakeGlobal(event.target.checked)} disabled={Boolean(editingId)} />
                  <span className="font-serif text-[12px] text-ink-soft">保存为全局模型（仅管理员）</span>
                </label>
              )}
            </div>
            <footer className="flex items-center justify-end gap-1.5 border-t border-line px-4 py-2.5">
              <button type="button" onClick={() => setFormOpen(false)} className="h-7 border border-line-strong px-2.5 text-[12px] text-ink-soft hover:border-ink hover:text-ink">
                取消
              </button>
              <button
                type="button"
                onClick={() => void save()}
                disabled={Boolean(busy) || !encryptionAvailable}
                className="inline-flex h-7 items-center gap-1 bg-ink px-2.5 text-[12px] font-semibold text-paper hover:bg-black disabled:opacity-50"
              >
                {busy?.kind === 'save' ? <Loader2 className="h-3 w-3 animate-spin" /> : <Save className="h-3 w-3" />}
                保存
              </button>
            </footer>
          </div>
        </div>
      )}
    </section>
  );
}

export default ModelSettingsPanel;
