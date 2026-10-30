import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  AlertTriangle,
  Bot,
  CheckCircle2,
  Loader2,
  Plus,
  RefreshCw,
  ScrollText,
  Trash2,
  XCircle,
} from 'lucide-react';
import type { AgentProfile, ModelProfile, PromptEvalResult, PromptVersion, ToolDefinition } from '../../contracts/api';
import {
  activatePromptVersion,
  createAgentProfile,
  createPromptVersion,
  deleteAgentProfile,
  evaluateAgentProfile,
  evaluatePromptVersion,
  listAgentProfiles,
  listModelProfiles,
  listPromptVersions,
  listTools,
  updateAgentProfile,
} from '../../services/api/researchAssistantApi';

type Busy = { kind: 'prompt' | 'profile' | 'eval' | 'toggle'; id?: string } | null;

const emptyPrompt = () => ({ name: 'custom-agent', template: 'You are a careful research assistant.\n\nTask: {{task}}' });
const emptyProfile = () => ({ name: '', prompt_version_id: '', model_profile_id: 'env-bootstrap', tool_allowlist: [] as string[], max_steps: 8, output_schema: '' });

const statusBadge = (prompt: PromptVersion) => {
  switch (prompt.status) {
    case 'active':
      return <span className="inline-flex items-center gap-0.5 border border-ok/40 bg-ok-soft px-1.5 py-0.5 text-[10px] text-ok"><CheckCircle2 className="h-2.5 w-2.5" />active</span>;
    case 'archived':
      return <span className="inline-flex items-center gap-0.5 border border-line px-1.5 py-0.5 text-[10px] text-muted"><XCircle className="h-2.5 w-2.5" />archived</span>;
    default:
      return <span className="border border-warn/40 bg-warn-soft px-1.5 py-0.5 text-[10px] text-warn">draft</span>;
  }
};

export function AgentSettingsPanel({ onClose }: { onClose?: () => void }) {
  const [profiles, setProfiles] = useState<AgentProfile[]>([]);
  const [prompts, setPrompts] = useState<PromptVersion[]>([]);
  const [models, setModels] = useState<ModelProfile[]>([]);
  const [tools, setTools] = useState<ToolDefinition[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState<Busy>(null);
  const [promptOpen, setPromptOpen] = useState(false);
  const [profileOpen, setProfileOpen] = useState(false);
  const [promptDraft, setPromptDraft] = useState(emptyPrompt);
  const [profileDraft, setProfileDraft] = useState(emptyProfile);
  const [evalResults, setEvalResults] = useState<Record<string, PromptEvalResult>>({});

  const reload = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const [profileResponse, promptResponse, modelResponse, toolResponse] = await Promise.all([
        listAgentProfiles(),
        listPromptVersions(),
        listModelProfiles(),
        listTools(),
      ]);
      setProfiles(profileResponse ?? []);
      setPrompts(promptResponse ?? []);
      setModels(modelResponse.profiles ?? []);
      setTools(toolResponse.tools ?? []);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : '加载 Agent 配置失败');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  const builtinProfiles = useMemo(() => profiles.filter((profile) => profile.builtin), [profiles]);
  const customProfiles = useMemo(() => profiles.filter((profile) => !profile.builtin), [profiles]);
  const activePrompts = useMemo(() => prompts.filter((prompt) => prompt.status !== 'archived'), [prompts]);

  const savePrompt = async () => {
    setBusy({ kind: 'prompt' });
    setError('');
    setNotice('');
    try {
      const prompt = await createPromptVersion(promptDraft);
      setNotice(`Prompt ${prompt.name} v${prompt.version} 已创建（draft）`);
      setPromptOpen(false);
      setPromptDraft(emptyPrompt());
      await reload();
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : '创建 Prompt 失败');
    } finally {
      setBusy(null);
    }
  };

  const activatePrompt = async (prompt: PromptVersion) => {
    setBusy({ kind: 'prompt', id: prompt.id });
    try {
      await activatePromptVersion(prompt.id);
      setNotice(`已激活 ${prompt.name} v${prompt.version}（旧 active 自动归档）`);
      await reload();
    } catch (activateError) {
      setError(activateError instanceof Error ? activateError.message : '激活失败');
    } finally {
      setBusy(null);
    }
  };

  const runPromptEval = async (prompt: PromptVersion) => {
    setBusy({ kind: 'eval', id: prompt.id });
    setError('');
    try {
      const variables: Record<string, string> = {};
      (prompt.variables ?? []).forEach((name) => { variables[name] = 'sample'; });
      const result = await evaluatePromptVersion(prompt.id, { cases: [{ name: 'default', variables }] });
      setEvalResults((current) => ({ ...current, [prompt.id]: result }));
      setNotice(`${prompt.name} v${prompt.version} 评测：${result.passed}/${result.total}`);
      await reload();
    } catch (evalError) {
      setError(evalError instanceof Error ? evalError.message : '评测失败');
    } finally {
      setBusy(null);
    }
  };

  const saveProfile = async () => {
    setBusy({ kind: 'profile' });
    setError('');
    setNotice('');
    try {
      const profile = await createAgentProfile({
        name: profileDraft.name,
        prompt_version_id: profileDraft.prompt_version_id,
        model_profile_id: profileDraft.model_profile_id,
        tool_allowlist: profileDraft.tool_allowlist,
        max_steps: profileDraft.max_steps,
        output_schema: profileDraft.output_schema,
      });
      setNotice(`AgentProfile ${profile.name} 已创建：${profile.id}`);
      setProfileOpen(false);
      setProfileDraft(emptyProfile());
      await reload();
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : '创建 AgentProfile 失败');
    } finally {
      setBusy(null);
    }
  };

  const runProfileEval = async (profile: AgentProfile) => {
    setBusy({ kind: 'eval', id: profile.id });
    setError('');
    try {
      const result = await evaluateAgentProfile(profile.id, { cases: [] });
      setNotice(`${profile.name} 评测：${result.passed}/${result.total}（${result.mode}）`);
      const promptVersionID = profile.prompt_version_id;
      if (promptVersionID) {
        setEvalResults((current) => ({ ...current, [promptVersionID]: result }));
      }
      await reload();
    } catch (evalError) {
      setError(evalError instanceof Error ? evalError.message : '评测失败');
    } finally {
      setBusy(null);
    }
  };

  const toggleProfile = async (profile: AgentProfile) => {
    setBusy({ kind: 'toggle', id: profile.id });
    try {
      await updateAgentProfile(profile.id, { enabled: !profile.enabled });
      await reload();
    } catch (toggleError) {
      setError(toggleError instanceof Error ? toggleError.message : '更新失败');
    } finally {
      setBusy(null);
    }
  };

  const removeProfile = async (profile: AgentProfile) => {
    setBusy({ kind: 'toggle', id: profile.id });
    try {
      await deleteAgentProfile(profile.id);
      setNotice(`已停用 ${profile.name}`);
      await reload();
    } catch (removeError) {
      setError(removeError instanceof Error ? removeError.message : '停用失败');
    } finally {
      setBusy(null);
    }
  };

  const profileCard = (profile: AgentProfile) => (
    <article key={profile.id} className="border border-line bg-surface p-3">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="font-serif text-[13px] font-semibold text-ink">{profile.name}</span>
            <span className="font-mono text-[10.5px] text-muted">{profile.id}</span>
            {profile.builtin ? <span className="border border-line px-1.5 py-0.5 text-[10px] text-muted">内置</span> : null}
            {profile.enabled ? <span className="border border-ok/40 bg-ok-soft px-1.5 py-0.5 text-[10px] text-ok">启用</span> : <span className="border border-proof/40 bg-proof-soft px-1.5 py-0.5 text-[10px] text-proof">停用</span>}
          </div>
          <p className="mt-1 font-mono text-[10.5px] text-muted">
            prompt: {profile.prompt_version_id || '—'} · model: {profile.model_profile_id || '—'} · steps: {profile.max_steps ?? '—'}
          </p>
          <p className="mt-0.5 font-mono text-[10.5px] text-muted">tools: {(profile.tool_allowlist ?? []).join(', ') || '—'}</p>
        </div>
        <div className="flex shrink-0 items-center gap-1">
          {!profile.builtin && (
            <>
              <button type="button" onClick={() => void runProfileEval(profile)} disabled={Boolean(busy)} className="h-6 border border-line-strong px-2 text-[11.5px] text-ink-soft hover:border-ink hover:text-ink disabled:opacity-50">
                {busy?.kind === 'eval' && busy.id === profile.id ? <Loader2 className="h-3 w-3 animate-spin" /> : '评测'}
              </button>
              <button type="button" onClick={() => void toggleProfile(profile)} disabled={Boolean(busy)} className="h-6 border border-line-strong px-2 text-[11.5px] text-ink-soft hover:border-ink hover:text-ink disabled:opacity-50">{profile.enabled ? '停用' : '启用'}</button>
              <button type="button" onClick={() => void removeProfile(profile)} disabled={Boolean(busy)} className="inline-flex h-6 items-center gap-1 border border-proof/40 px-2 text-[11.5px] text-proof hover:bg-proof-soft disabled:opacity-50"><Trash2 className="h-3 w-3" />删除</button>
            </>
          )}
        </div>
      </div>
    </article>
  );

  return (
    <section className="flex h-full flex-col overflow-hidden bg-paper" aria-label="Agent 设置">
      <header className="flex shrink-0 items-start justify-between gap-3 border-b border-line bg-surface px-4 py-3">
        <div>
          <div className="journal-caps text-[10px]">工作台设置 · Agent</div>
          <h2 className="mt-0.5 flex items-center gap-1.5 font-serif text-[16px] font-semibold text-ink"><Bot className="h-4 w-4" />AgentProfile 与 Prompt 版本</h2>
          <p className="mt-1 max-w-2xl font-serif text-[12px] leading-5 text-ink-soft">
            AgentProfile = PromptVersion + ModelProfile + ToolAllowlist + MaxSteps。Prompt 版本不可变，切换 active 即可回滚。
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-1.5">
          <button type="button" onClick={() => void reload()} className="inline-flex h-7 items-center gap-1 border border-line-strong px-2 text-[12px] text-ink-soft hover:border-ink hover:text-ink"><RefreshCw className="h-3 w-3" />刷新</button>
          {onClose && <button type="button" onClick={onClose} className="h-7 border border-line-strong px-2 text-[12px] text-ink-soft hover:border-ink hover:text-ink">关闭</button>}
        </div>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3">
        {error && <div className="mb-3 border border-proof/40 bg-proof-soft px-3 py-2 font-serif text-[12px] text-proof">{error}</div>}
        {notice && <div className="mb-3 border border-ok/40 bg-ok-soft px-3 py-2 font-serif text-[12px] text-ok">{notice}</div>}
        {loading ? (
          <div className="flex items-center gap-2 py-8 text-[12px] text-muted"><Loader2 className="h-4 w-4 animate-spin" />正在加载 Agent 配置…</div>
        ) : (
          <>
            <div className="mb-2 flex flex-wrap items-baseline justify-between gap-2">
              <h3 className="journal-caps text-[10.5px] font-semibold text-ink">Prompt 版本 · {prompts.length}</h3>
              <button type="button" onClick={() => { setPromptOpen(true); setNotice(''); }} className="inline-flex h-7 items-center gap-1 bg-ink px-2.5 text-[12px] font-semibold text-paper hover:bg-black"><Plus className="h-3.5 w-3.5" />新建 Prompt 版本</button>
            </div>
            {prompts.length === 0 ? (
              <p className="border border-dashed border-line px-3 py-4 font-serif text-[12px] text-muted">还没有 Prompt 版本。创建 draft → 评测 → 激活。</p>
            ) : (
              <div className="space-y-2">
                {prompts.map((prompt) => (
                  <article key={prompt.id} className="border border-line bg-surface p-3">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <div className="flex flex-wrap items-center gap-1.5">
                        <ScrollText className="h-3.5 w-3.5 text-muted" />
                        <span className="font-serif text-[13px] font-semibold text-ink">{prompt.name}</span>
                        <span className="font-mono text-[11px] text-muted">v{prompt.version}</span>
                        {statusBadge(prompt)}
                        {typeof prompt.eval_score === 'number' && prompt.eval_score > 0 ? <span className="border border-line px-1.5 py-0.5 text-[10px] text-muted">eval {Math.round(prompt.eval_score * 100)}%</span> : null}
                      </div>
                      <div className="flex items-center gap-1">
                        <button type="button" onClick={() => void runPromptEval(prompt)} disabled={Boolean(busy)} className="h-6 border border-line-strong px-2 text-[11.5px] text-ink-soft hover:border-ink hover:text-ink disabled:opacity-50">
                          {busy?.kind === 'eval' && busy.id === prompt.id ? <Loader2 className="h-3 w-3 animate-spin" /> : '评测'}
                        </button>
                        {prompt.status !== 'active' && (
                          <button type="button" onClick={() => void activatePrompt(prompt)} disabled={Boolean(busy)} className="h-6 border border-line-strong px-2 text-[11.5px] text-ink-soft hover:border-ink hover:text-ink disabled:opacity-50">激活</button>
                        )}
                      </div>
                    </div>
                    <p className="mt-1 whitespace-pre-wrap border border-line bg-paper px-2 py-1 font-mono text-[11px] leading-5 text-ink-soft">{prompt.template}</p>
                    <p className="mt-1 font-mono text-[10.5px] text-muted">variables: {(prompt.variables ?? []).join(', ') || '—'}{prompt.eval_detail ? ` · ${prompt.eval_detail}` : ''}</p>
                    {evalResults[prompt.id] && (
                      <p className="mt-0.5 font-mono text-[10.5px] text-muted">
                        eval: {evalResults[prompt.id].passed}/{evalResults[prompt.id].total} · {evalResults[prompt.id].cases.map((item) => `${item.name ?? 'case'}:${item.passed ? 'pass' : 'fail'}`).join(' ')}
                      </p>
                    )}
                  </article>
                ))}
              </div>
            )}

            <div className="mb-2 mt-5 flex flex-wrap items-baseline justify-between gap-2">
              <h3 className="journal-caps text-[10.5px] font-semibold text-ink">自定义 AgentProfile · {customProfiles.length}</h3>
              <button type="button" onClick={() => { setProfileOpen(true); setNotice(''); }} className="inline-flex h-7 items-center gap-1 bg-ink px-2.5 text-[12px] font-semibold text-paper hover:bg-black"><Plus className="h-3.5 w-3.5" />创建 Agent</button>
            </div>
            {customProfiles.length === 0 ? (
              <p className="border border-dashed border-line px-3 py-4 font-serif text-[12px] text-muted">还没有自定义 Agent。创建后可在计划节点使用 profile:&lt;id&gt;。</p>
            ) : (
              <div className="grid gap-2 md:grid-cols-2">{customProfiles.map(profileCard)}</div>
            )}

            <h3 className="mb-2 mt-5 journal-caps text-[10.5px] font-semibold text-ink">内置 AgentProfile · {builtinProfiles.length}（只读）</h3>
            <div className="grid gap-2 md:grid-cols-2">{builtinProfiles.map(profileCard)}</div>

            <div className="mt-4 flex items-start gap-1.5 border-t border-line pt-3 font-serif text-[11.5px] leading-5 text-muted">
              <AlertTriangle className="mt-0.5 h-3 w-3 shrink-0" />
              <p>内置 Agent 仍走固定 runner；自定义 profile 走 PromptVersion + ModelProfile，未知 profile 会在计划校验时直接报错，不再静默降级。</p>
            </div>
          </>
        )}
      </div>

      {promptOpen && (
        <div className="absolute inset-0 z-30 flex items-start justify-center overflow-y-auto bg-ink/20 p-4" role="dialog" aria-modal="true" aria-label="新建 Prompt 版本">
          <div className="w-full max-w-xl border border-ink bg-surface shadow-xl">
            <header className="flex items-center justify-between border-b border-line px-4 py-2.5">
              <h3 className="font-serif text-[15px] font-semibold text-ink">新建 Prompt 版本</h3>
              <button type="button" onClick={() => setPromptOpen(false)} className="h-7 w-7 text-muted hover:text-ink" aria-label="关闭">×</button>
            </header>
            <div className="grid gap-2.5 px-4 py-3">
              <label className="block"><span className="journal-caps text-[10px] text-muted">名称（同名自动递增版本）</span><input value={promptDraft.name} onChange={(event) => setPromptDraft({ ...promptDraft, name: event.target.value })} className="mt-1 h-8 w-full border border-line-strong bg-paper px-2 text-[12px] text-ink" /></label>
              <label className="block"><span className="journal-caps text-[10px] text-muted">模板（支持 {'{{variable}}'}）</span><textarea value={promptDraft.template} onChange={(event) => setPromptDraft({ ...promptDraft, template: event.target.value })} rows={8} className="mt-1 w-full border border-line-strong bg-paper px-2 py-1 font-mono text-[12px] text-ink" /></label>
            </div>
            <footer className="flex items-center justify-end gap-1.5 border-t border-line px-4 py-2.5">
              <button type="button" onClick={() => setPromptOpen(false)} className="h-7 border border-line-strong px-2.5 text-[12px] text-ink-soft hover:border-ink hover:text-ink">取消</button>
              <button type="button" onClick={() => void savePrompt()} disabled={Boolean(busy)} className="inline-flex h-7 items-center gap-1 bg-ink px-2.5 text-[12px] font-semibold text-paper hover:bg-black disabled:opacity-50">{busy?.kind === 'prompt' ? <Loader2 className="h-3 w-3 animate-spin" /> : <Plus className="h-3 w-3" />}创建 draft</button>
            </footer>
          </div>
        </div>
      )}

      {profileOpen && (
        <div className="absolute inset-0 z-30 flex items-start justify-center overflow-y-auto bg-ink/20 p-4" role="dialog" aria-modal="true" aria-label="创建 AgentProfile">
          <div className="w-full max-w-xl border border-ink bg-surface shadow-xl">
            <header className="flex items-center justify-between border-b border-line px-4 py-2.5">
              <h3 className="font-serif text-[15px] font-semibold text-ink">创建 AgentProfile</h3>
              <button type="button" onClick={() => setProfileOpen(false)} className="h-7 w-7 text-muted hover:text-ink" aria-label="关闭">×</button>
            </header>
            <div className="grid gap-2.5 px-4 py-3 md:grid-cols-2">
              <label className="block md:col-span-2"><span className="journal-caps text-[10px] text-muted">名称</span><input value={profileDraft.name} onChange={(event) => setProfileDraft({ ...profileDraft, name: event.target.value })} className="mt-1 h-8 w-full border border-line-strong bg-paper px-2 text-[12px] text-ink" /></label>
              <label className="block"><span className="journal-caps text-[10px] text-muted">Prompt 版本</span><select value={profileDraft.prompt_version_id} onChange={(event) => setProfileDraft({ ...profileDraft, prompt_version_id: event.target.value })} className="mt-1 h-8 w-full border border-line-strong bg-paper px-2 text-[12px] text-ink"><option value="">选择</option>{activePrompts.map((prompt) => <option key={prompt.id} value={prompt.id}>{prompt.name} v{prompt.version} ({prompt.status})</option>)}</select></label>
              <label className="block"><span className="journal-caps text-[10px] text-muted">模型 Profile</span><select value={profileDraft.model_profile_id} onChange={(event) => setProfileDraft({ ...profileDraft, model_profile_id: event.target.value })} className="mt-1 h-8 w-full border border-line-strong bg-paper px-2 text-[12px] text-ink"><option value="env-bootstrap">env-bootstrap（部署默认模型）</option>{models.map((model) => <option key={model.id} value={model.id}>{model.global ? '[全局] ' : ''}{model.name} · {model.model}</option>)}</select></label>
              <label className="block"><span className="journal-caps text-[10px] text-muted">MaxSteps</span><input type="number" min={1} max={64} value={profileDraft.max_steps} onChange={(event) => setProfileDraft({ ...profileDraft, max_steps: Number(event.target.value) })} className="mt-1 h-8 w-full border border-line-strong bg-paper px-2 text-[12px] text-ink" /></label>
              <label className="block"><span className="journal-caps text-[10px] text-muted">Output Schema（可选 JSON）</span><input value={profileDraft.output_schema} onChange={(event) => setProfileDraft({ ...profileDraft, output_schema: event.target.value })} className="mt-1 h-8 w-full border border-line-strong bg-paper px-2 font-mono text-[12px] text-ink" /></label>
              <div className="md:col-span-2">
                <span className="journal-caps text-[10px] text-muted">Tool Allowlist</span>
                <div className="mt-1 grid max-h-32 gap-1 overflow-y-auto border border-line bg-paper p-2 md:grid-cols-2">
                  {tools.map((tool) => (
                    <label key={tool.id} className="inline-flex items-center gap-1.5">
                      <input
                        type="checkbox"
                        checked={profileDraft.tool_allowlist.includes(tool.id)}
                        onChange={(event) => {
                          const next = event.target.checked
                            ? [...profileDraft.tool_allowlist, tool.id]
                            : profileDraft.tool_allowlist.filter((id) => id !== tool.id);
                          setProfileDraft({ ...profileDraft, tool_allowlist: next });
                        }}
                      />
                      <span className="font-mono text-[11px] text-ink-soft">{tool.id}</span>
                    </label>
                  ))}
                </div>
                <p className="mt-1 font-serif text-[11px] text-muted">工具还需要在“工具”页创建 ToolGrant 后，profile 才能调用。</p>
              </div>
            </div>
            <footer className="flex items-center justify-end gap-1.5 border-t border-line px-4 py-2.5">
              <button type="button" onClick={() => setProfileOpen(false)} className="h-7 border border-line-strong px-2.5 text-[12px] text-ink-soft hover:border-ink hover:text-ink">取消</button>
              <button type="button" onClick={() => void saveProfile()} disabled={Boolean(busy)} className="inline-flex h-7 items-center gap-1 bg-ink px-2.5 text-[12px] font-semibold text-paper hover:bg-black disabled:opacity-50">{busy?.kind === 'profile' ? <Loader2 className="h-3 w-3 animate-spin" /> : <Bot className="h-3 w-3" />}创建 Agent</button>
            </footer>
          </div>
        </div>
      )}
    </section>
  );
}

export default AgentSettingsPanel;
