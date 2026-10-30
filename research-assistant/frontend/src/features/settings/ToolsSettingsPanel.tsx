import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  AlertTriangle,
  CheckCircle2,
  Loader2,
  Plug,
  Plus,
  RefreshCw,
  ShieldCheck,
  Trash2,
  XCircle,
} from 'lucide-react';
import type { AgentProfile, MCPServer, ToolDefinition, ToolGrant } from '../../contracts/api';
import {
  createTool,
  createToolGrant,
  deleteTool,
  deleteToolGrant,
  listAgentProfiles,
  listToolGrants,
  listTools,
  testTool,
  updateTool,
} from '../../services/api/researchAssistantApi';

type Busy = { kind: 'save' | 'delete' | 'test' | 'grant'; id?: string } | null;

const transportLabel: Record<string, string> = {
  native: 'Native',
  http: 'HTTP',
  mcp_stdio: 'MCP · stdio',
  mcp_http: 'MCP · HTTP',
};

const emptyMcpDraft = () => ({
  id: 'mcp:',
  name: '',
  transport: 'mcp_stdio',
  command: '/app/mockmcp',
  args: '',
  env: '',
  url: '',
  headers: '',
  timeout_sec: 20,
});

const emptyHttpDraft = () => ({
  id: 'http:',
  name: '',
  url: '',
  method: 'POST',
  headers: '',
  input_schema: '{"type":"object","properties":{}}',
  approval: 'never',
  side_effect: false,
  scopes: '',
});

export function ToolsSettingsPanel({ onClose }: { onClose?: () => void }) {
  const [tools, setTools] = useState<ToolDefinition[]>([]);
  const [servers, setServers] = useState<MCPServer[]>([]);
  const [grants, setGrants] = useState<ToolGrant[]>([]);
  const [profiles, setProfiles] = useState<AgentProfile[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busy, setBusy] = useState<Busy>(null);
  const [mcpOpen, setMcpOpen] = useState(false);
  const [httpOpen, setHttpOpen] = useState(false);
  const [mcpDraft, setMcpDraft] = useState(emptyMcpDraft);
  const [httpDraft, setHttpDraft] = useState(emptyHttpDraft);
  const [testTarget, setTestTarget] = useState<ToolDefinition | null>(null);
  const [testArgs, setTestArgs] = useState('{"message":"hello"}');
  const [grantDraft, setGrantDraft] = useState({ agent_profile_id: '', tool_id: '' });

  const reload = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const [toolResponse, grantResponse, profileResponse] = await Promise.all([
        listTools(),
        listToolGrants(),
        listAgentProfiles(),
      ]);
      setTools(toolResponse.tools ?? []);
      setServers(toolResponse.servers ?? []);
      setGrants(grantResponse ?? []);
      setProfiles(profileResponse ?? []);
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : '加载工具配置失败');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void reload();
  }, [reload]);

  const grouped = useMemo(() => {
    const map = new Map<string, ToolDefinition[]>();
    tools.forEach((tool) => {
      const key = tool.server_id || tool.transport;
      map.set(key, [...(map.get(key) ?? []), tool]);
    });
    return [...map.entries()];
  }, [tools]);

  const saveMcp = async () => {
    setBusy({ kind: 'save' });
    setError('');
    setNotice('');
    try {
      const args = mcpDraft.args.split(/\s+/).map((value) => value.trim()).filter(Boolean);
      const env: Record<string, string> = {};
      mcpDraft.env.split('\n').forEach((line) => {
        const index = line.indexOf('=');
        if (index > 0) env[line.slice(0, index).trim()] = line.slice(index + 1).trim();
      });
      const headers: Record<string, string> = {};
      mcpDraft.headers.split('\n').forEach((line) => {
        const index = line.indexOf('=');
        if (index > 0) headers[line.slice(0, index).trim()] = line.slice(index + 1).trim();
      });
      await createTool({
        id: mcpDraft.id.trim(),
        name: mcpDraft.name.trim() || mcpDraft.id.trim(),
        transport: mcpDraft.transport,
        scopes: [],
        side_effect: false,
        approval: 'never',
        mcp: {
          id: mcpDraft.id.trim(),
          name: mcpDraft.name.trim(),
          command: mcpDraft.command.trim(),
          args,
          env,
          url: mcpDraft.url.trim(),
          headers,
          timeout_sec: mcpDraft.timeout_sec,
        },
      });
      setNotice('MCP server 已注册并完成 tools/list 握手。');
      setMcpOpen(false);
      setMcpDraft(emptyMcpDraft());
      await reload();
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : '注册 MCP server 失败');
    } finally {
      setBusy(null);
    }
  };

  const saveHttp = async () => {
    setBusy({ kind: 'save' });
    setError('');
    setNotice('');
    try {
      const headers: Record<string, string> = {};
      httpDraft.headers.split('\n').forEach((line) => {
        const index = line.indexOf('=');
        if (index > 0) headers[line.slice(0, index).trim()] = line.slice(index + 1).trim();
      });
      await createTool({
        id: httpDraft.id.trim(),
        name: httpDraft.name.trim() || httpDraft.id.trim(),
        version: '1.0.0',
        transport: 'http',
        input_schema: httpDraft.input_schema,
        scopes: httpDraft.scopes.split(',').map((value) => value.trim()).filter(Boolean),
        side_effect: httpDraft.side_effect,
        approval: httpDraft.approval,
        http: { url: httpDraft.url.trim(), method: httpDraft.method, headers },
      });
      setNotice('HTTP 工具已注册。');
      setHttpOpen(false);
      setHttpDraft(emptyHttpDraft());
      await reload();
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : '注册 HTTP 工具失败');
    } finally {
      setBusy(null);
    }
  };

  const toggleEnabled = async (tool: ToolDefinition) => {
    setBusy({ kind: 'save', id: tool.id });
    try {
      await updateTool(tool.id, { enabled: !tool.enabled });
      await reload();
    } catch (toggleError) {
      setError(toggleError instanceof Error ? toggleError.message : '更新工具失败');
    } finally {
      setBusy(null);
    }
  };

  const removeTool = async (tool: ToolDefinition) => {
    setBusy({ kind: 'delete', id: tool.id });
    try {
      await deleteTool(tool.id);
      setNotice(`已停用 ${tool.id}`);
      await reload();
    } catch (deleteError) {
      setError(deleteError instanceof Error ? deleteError.message : '停用工具失败');
    } finally {
      setBusy(null);
    }
  };

  const runTest = async () => {
    if (!testTarget) return;
    setBusy({ kind: 'test', id: testTarget.id });
    setError('');
    setNotice('');
    try {
      const parsed = JSON.parse(testArgs) as Record<string, unknown>;
      const result = await testTool(testTarget.id, { arguments: parsed, dry_run: true });
      setNotice(`测试通过：${result.output}`);
      setTestTarget(null);
    } catch (testError) {
      setError(testError instanceof Error ? testError.message : '测试调用失败');
    } finally {
      setBusy(null);
    }
  };

  const addGrant = async () => {
    if (!grantDraft.agent_profile_id || !grantDraft.tool_id) {
      setError('选择 AgentProfile 与工具后再授权');
      return;
    }
    setBusy({ kind: 'grant' });
    try {
      await createToolGrant(grantDraft);
      setNotice('授权已保存');
      setGrantDraft({ agent_profile_id: '', tool_id: '' });
      await reload();
    } catch (grantError) {
      setError(grantError instanceof Error ? grantError.message : '授权失败');
    } finally {
      setBusy(null);
    }
  };

  const removeGrant = async (grant: ToolGrant) => {
    setBusy({ kind: 'grant', id: grant.id });
    try {
      await deleteToolGrant(grant.id);
      await reload();
    } catch (grantError) {
      setError(grantError instanceof Error ? grantError.message : '删除授权失败');
    } finally {
      setBusy(null);
    }
  };

  const toolCard = (tool: ToolDefinition) => (
    <article key={tool.id} className="border border-line bg-surface p-3">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="font-mono text-[12px] font-semibold text-ink">{tool.id}</span>
            <span className="border border-line-strong px-1.5 py-0.5 text-[10px] text-ink-soft">{transportLabel[tool.transport] ?? tool.transport}</span>
            <span className="border border-line px-1.5 py-0.5 text-[10px] text-muted">v{tool.version}</span>
            {tool.builtin ? <span className="border border-line px-1.5 py-0.5 text-[10px] text-muted">内置</span> : null}
            {tool.enabled ? (
              <span className="inline-flex items-center gap-0.5 border border-ok/40 bg-ok-soft px-1.5 py-0.5 text-[10px] text-ok"><CheckCircle2 className="h-2.5 w-2.5" />启用</span>
            ) : (
              <span className="inline-flex items-center gap-0.5 border border-proof/40 bg-proof-soft px-1.5 py-0.5 text-[10px] text-proof"><XCircle className="h-2.5 w-2.5" />停用</span>
            )}
          </div>
          <p className="mt-1 font-serif text-[12px] leading-5 text-ink-soft">{tool.description || '无描述'}</p>
          <p className="mt-0.5 font-mono text-[10.5px] text-muted">
            scopes: {(tool.scopes ?? []).join(', ') || '—'} · approval: {tool.approval || 'default'} · side_effect: {tool.side_effect ? 'yes' : 'no'}
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-1">
          <button type="button" onClick={() => { setTestTarget(tool); setTestArgs('{"message":"hello"}'); }} className="h-6 border border-line-strong px-2 text-[11.5px] text-ink-soft hover:border-ink hover:text-ink">测试</button>
          <button type="button" onClick={() => void toggleEnabled(tool)} disabled={Boolean(busy)} className="h-6 border border-line-strong px-2 text-[11.5px] text-ink-soft hover:border-ink hover:text-ink disabled:opacity-50">
            {busy?.kind === 'save' && busy.id === tool.id ? <Loader2 className="h-3 w-3 animate-spin" /> : tool.enabled ? '停用' : '启用'}
          </button>
          {!tool.builtin && (
            <button type="button" onClick={() => void removeTool(tool)} disabled={Boolean(busy)} className="inline-flex h-6 items-center gap-1 border border-proof/40 px-2 text-[11.5px] text-proof hover:bg-proof-soft disabled:opacity-50">
              <Trash2 className="h-3 w-3" />删除
            </button>
          )}
        </div>
      </div>
    </article>
  );

  return (
    <section className="flex h-full flex-col overflow-hidden bg-paper" aria-label="工具设置">
      <header className="flex shrink-0 items-start justify-between gap-3 border-b border-line bg-surface px-4 py-3">
        <div>
          <div className="journal-caps text-[10px]">工作台设置 · 工具</div>
          <h2 className="mt-0.5 flex items-center gap-1.5 font-serif text-[16px] font-semibold text-ink">
            <Plug className="h-4 w-4" />
            工具注册表与 MCP
          </h2>
          <p className="mt-1 max-w-2xl font-serif text-[12px] leading-5 text-ink-soft">
            工具调用经过 schema 校验、AgentProfile 白名单、ToolGrant 与审批；每次调用都会写入 ToolCallRecord，可回放。
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-1.5">
          <button type="button" onClick={() => void reload()} className="inline-flex h-7 items-center gap-1 border border-line-strong px-2 text-[12px] text-ink-soft hover:border-ink hover:text-ink">
            <RefreshCw className="h-3 w-3" />刷新
          </button>
          {onClose && <button type="button" onClick={onClose} className="h-7 border border-line-strong px-2 text-[12px] text-ink-soft hover:border-ink hover:text-ink">关闭</button>}
        </div>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3">
        {error && <div className="mb-3 border border-proof/40 bg-proof-soft px-3 py-2 font-serif text-[12px] text-proof">{error}</div>}
        {notice && <div className="mb-3 border border-ok/40 bg-ok-soft px-3 py-2 font-serif text-[12px] text-ok">{notice}</div>}
        {loading ? (
          <div className="flex items-center gap-2 py-8 text-[12px] text-muted"><Loader2 className="h-4 w-4 animate-spin" />正在加载工具…</div>
        ) : (
          <>
            <div className="mb-2 flex flex-wrap items-baseline justify-between gap-2">
              <h3 className="journal-caps text-[10.5px] font-semibold text-ink">工具 · {tools.length}</h3>
              <div className="flex gap-1.5">
                <button type="button" onClick={() => { setMcpOpen(true); setNotice(''); }} className="inline-flex h-7 items-center gap-1 bg-ink px-2.5 text-[12px] font-semibold text-paper hover:bg-black"><Plus className="h-3.5 w-3.5" />添加 MCP Server</button>
                <button type="button" onClick={() => { setHttpOpen(true); setNotice(''); }} className="inline-flex h-7 items-center gap-1 border border-line-strong px-2.5 text-[12px] text-ink-soft hover:border-ink hover:text-ink"><Plus className="h-3.5 w-3.5" />添加 HTTP 工具</button>
              </div>
            </div>
            {grouped.map(([group, groupTools]) => (
              <div key={group} className="mb-3">
                <p className="mb-1 font-mono text-[10.5px] text-muted">{group}</p>
                <div className="grid gap-2 md:grid-cols-2">{groupTools.map(toolCard)}</div>
              </div>
            ))}

            <h3 className="mb-2 mt-5 journal-caps text-[10.5px] font-semibold text-ink">MCP Servers · {servers.length}</h3>
            {servers.length === 0 ? (
              <p className="border border-dashed border-line px-3 py-3 font-serif text-[12px] text-muted">还没有 MCP server。注册时会执行 initialize + tools/list，并保存 schema fingerprint。</p>
            ) : (
              <div className="space-y-1.5">
                {servers.map((server) => (
                  <div key={server.id} className="flex flex-wrap items-center justify-between gap-2 border border-line bg-surface px-3 py-2">
                    <div className="min-w-0">
                      <span className="font-mono text-[12px] font-semibold text-ink">{server.id}</span>
                      <span className="ml-2 border border-line px-1.5 py-0.5 text-[10px] text-muted">{transportLabel[server.transport] ?? server.transport}</span>
                      <p className="mt-0.5 font-mono text-[10.5px] text-muted">{server.command || server.url || '—'} · {server.status || 'unknown'}</p>
                    </div>
                    <span className="font-mono text-[10px] text-muted">{server.fingerprint?.slice(0, 22)}…</span>
                  </div>
                ))}
              </div>
            )}

            <h3 className="mb-2 mt-5 journal-caps text-[10.5px] font-semibold text-ink">Tool Grants · {grants.length}</h3>
            <div className="mb-2 grid gap-2 border border-line bg-surface p-2 md:grid-cols-[1fr_1fr_auto]">
              <select value={grantDraft.agent_profile_id} onChange={(event) => setGrantDraft({ ...grantDraft, agent_profile_id: event.target.value })} className="h-8 border border-line-strong bg-paper px-2 text-[12px] text-ink">
                <option value="">选择 AgentProfile</option>
                {profiles.map((profile) => <option key={profile.id} value={profile.id}>{profile.name} · {profile.id}</option>)}
              </select>
              <select value={grantDraft.tool_id} onChange={(event) => setGrantDraft({ ...grantDraft, tool_id: event.target.value })} className="h-8 border border-line-strong bg-paper px-2 text-[12px] text-ink">
                <option value="">选择工具</option>
                {tools.map((tool) => <option key={tool.id} value={tool.id}>{tool.id}</option>)}
              </select>
              <button type="button" onClick={() => void addGrant()} disabled={Boolean(busy)} className="inline-flex h-8 items-center gap-1 bg-ink px-2.5 text-[12px] font-semibold text-paper hover:bg-black disabled:opacity-50"><ShieldCheck className="h-3.5 w-3.5" />授权</button>
            </div>
            {grants.length === 0 ? (
              <p className="border border-dashed border-line px-3 py-3 font-serif text-[12px] text-muted">没有授权。自定义 AgentProfile 调用工具前必须先授权。</p>
            ) : (
              <div className="space-y-1">
                {grants.map((grant) => (
                  <div key={grant.id} className="flex items-center justify-between gap-2 border border-line px-2 py-1">
                    <span className="truncate font-mono text-[11px] text-ink-soft">{grant.agent_profile_id} → {grant.tool_id}</span>
                    <button type="button" onClick={() => void removeGrant(grant)} disabled={Boolean(busy)} className="text-[11px] text-proof hover:underline disabled:opacity-50">移除</button>
                  </div>
                ))}
              </div>
            )}

            <div className="mt-4 flex items-start gap-1.5 border-t border-line pt-3 font-serif text-[11.5px] leading-5 text-muted">
              <AlertTriangle className="mt-0.5 h-3 w-3 shrink-0" />
              <p>MCP env/header 只写不回显；stdio 命令必须在服务端 allowlist 中，HTTP 私网地址默认被 SSRF 防护拒绝。</p>
            </div>
          </>
        )}
      </div>

      {mcpOpen && (
        <div className="absolute inset-0 z-30 flex items-start justify-center overflow-y-auto bg-ink/20 p-4" role="dialog" aria-modal="true" aria-label="注册 MCP Server">
          <div className="w-full max-w-xl border border-ink bg-surface shadow-xl">
            <header className="flex items-center justify-between border-b border-line px-4 py-2.5">
              <h3 className="font-serif text-[15px] font-semibold text-ink">注册 MCP Server</h3>
              <button type="button" onClick={() => setMcpOpen(false)} className="h-7 w-7 text-muted hover:text-ink" aria-label="关闭">×</button>
            </header>
            <div className="grid gap-2.5 px-4 py-3 md:grid-cols-2">
              <label className="block"><span className="journal-caps text-[10px] text-muted">Server ID</span><input value={mcpDraft.id} onChange={(event) => setMcpDraft({ ...mcpDraft, id: event.target.value })} placeholder="mcp:my-server" className="mt-1 h-8 w-full border border-line-strong bg-paper px-2 font-mono text-[12px] text-ink" /></label>
              <label className="block"><span className="journal-caps text-[10px] text-muted">名称</span><input value={mcpDraft.name} onChange={(event) => setMcpDraft({ ...mcpDraft, name: event.target.value })} className="mt-1 h-8 w-full border border-line-strong bg-paper px-2 text-[12px] text-ink" /></label>
              <label className="block"><span className="journal-caps text-[10px] text-muted">传输</span><select value={mcpDraft.transport} onChange={(event) => setMcpDraft({ ...mcpDraft, transport: event.target.value })} className="mt-1 h-8 w-full border border-line-strong bg-paper px-2 text-[12px] text-ink"><option value="mcp_stdio">stdio</option><option value="mcp_http">HTTP</option></select></label>
              <label className="block"><span className="journal-caps text-[10px] text-muted">超时（秒）</span><input type="number" min={1} value={mcpDraft.timeout_sec} onChange={(event) => setMcpDraft({ ...mcpDraft, timeout_sec: Number(event.target.value) })} className="mt-1 h-8 w-full border border-line-strong bg-paper px-2 text-[12px] text-ink" /></label>
              {mcpDraft.transport === 'mcp_stdio' ? (
                <>
                  <label className="block md:col-span-2"><span className="journal-caps text-[10px] text-muted">Command（绝对路径，需在 allowlist）</span><input value={mcpDraft.command} onChange={(event) => setMcpDraft({ ...mcpDraft, command: event.target.value })} className="mt-1 h-8 w-full border border-line-strong bg-paper px-2 font-mono text-[12px] text-ink" /></label>
                  <label className="block md:col-span-2"><span className="journal-caps text-[10px] text-muted">Args（空格分隔）</span><input value={mcpDraft.args} onChange={(event) => setMcpDraft({ ...mcpDraft, args: event.target.value })} className="mt-1 h-8 w-full border border-line-strong bg-paper px-2 font-mono text-[12px] text-ink" /></label>
                  <label className="block md:col-span-2"><span className="journal-caps text-[10px] text-muted">Env（KEY=VALUE，每行一个，只写不回显）</span><textarea value={mcpDraft.env} onChange={(event) => setMcpDraft({ ...mcpDraft, env: event.target.value })} rows={2} className="mt-1 w-full border border-line-strong bg-paper px-2 py-1 font-mono text-[12px] text-ink" /></label>
                </>
              ) : (
                <label className="block md:col-span-2"><span className="journal-caps text-[10px] text-muted">URL（HTTPS；私网需管理员）</span><input value={mcpDraft.url} onChange={(event) => setMcpDraft({ ...mcpDraft, url: event.target.value })} placeholder="https://example.com/mcp" className="mt-1 h-8 w-full border border-line-strong bg-paper px-2 font-mono text-[12px] text-ink" /></label>
              )}
              <label className="block md:col-span-2"><span className="journal-caps text-[10px] text-muted">Headers（KEY=VALUE，每行一个）</span><textarea value={mcpDraft.headers} onChange={(event) => setMcpDraft({ ...mcpDraft, headers: event.target.value })} rows={2} className="mt-1 w-full border border-line-strong bg-paper px-2 py-1 font-mono text-[12px] text-ink" /></label>
            </div>
            <footer className="flex items-center justify-end gap-1.5 border-t border-line px-4 py-2.5">
              <button type="button" onClick={() => setMcpOpen(false)} className="h-7 border border-line-strong px-2.5 text-[12px] text-ink-soft hover:border-ink hover:text-ink">取消</button>
              <button type="button" onClick={() => void saveMcp()} disabled={Boolean(busy)} className="inline-flex h-7 items-center gap-1 bg-ink px-2.5 text-[12px] font-semibold text-paper hover:bg-black disabled:opacity-50">{busy?.kind === 'save' ? <Loader2 className="h-3 w-3 animate-spin" /> : <Plug className="h-3 w-3" />}连接并注册</button>
            </footer>
          </div>
        </div>
      )}

      {httpOpen && (
        <div className="absolute inset-0 z-30 flex items-start justify-center overflow-y-auto bg-ink/20 p-4" role="dialog" aria-modal="true" aria-label="注册 HTTP 工具">
          <div className="w-full max-w-xl border border-ink bg-surface shadow-xl">
            <header className="flex items-center justify-between border-b border-line px-4 py-2.5">
              <h3 className="font-serif text-[15px] font-semibold text-ink">注册 HTTP 工具</h3>
              <button type="button" onClick={() => setHttpOpen(false)} className="h-7 w-7 text-muted hover:text-ink" aria-label="关闭">×</button>
            </header>
            <div className="grid gap-2.5 px-4 py-3 md:grid-cols-2">
              <label className="block"><span className="journal-caps text-[10px] text-muted">Tool ID</span><input value={httpDraft.id} onChange={(event) => setHttpDraft({ ...httpDraft, id: event.target.value })} placeholder="http:lab/query" className="mt-1 h-8 w-full border border-line-strong bg-paper px-2 font-mono text-[12px] text-ink" /></label>
              <label className="block"><span className="journal-caps text-[10px] text-muted">名称</span><input value={httpDraft.name} onChange={(event) => setHttpDraft({ ...httpDraft, name: event.target.value })} className="mt-1 h-8 w-full border border-line-strong bg-paper px-2 text-[12px] text-ink" /></label>
              <label className="block md:col-span-2"><span className="journal-caps text-[10px] text-muted">URL</span><input value={httpDraft.url} onChange={(event) => setHttpDraft({ ...httpDraft, url: event.target.value })} placeholder="https://lab.example.com/tools/query" className="mt-1 h-8 w-full border border-line-strong bg-paper px-2 font-mono text-[12px] text-ink" /></label>
              <label className="block"><span className="journal-caps text-[10px] text-muted">方法</span><select value={httpDraft.method} onChange={(event) => setHttpDraft({ ...httpDraft, method: event.target.value })} className="mt-1 h-8 w-full border border-line-strong bg-paper px-2 text-[12px] text-ink"><option>POST</option><option>GET</option></select></label>
              <label className="block"><span className="journal-caps text-[10px] text-muted">审批</span><select value={httpDraft.approval} onChange={(event) => setHttpDraft({ ...httpDraft, approval: event.target.value })} className="mt-1 h-8 w-full border border-line-strong bg-paper px-2 text-[12px] text-ink"><option value="never">never</option><option value="write">write</option><option value="always">always</option><option value="network">network</option></select></label>
              <label className="block md:col-span-2"><span className="journal-caps text-[10px] text-muted">Input Schema（JSON Schema）</span><textarea value={httpDraft.input_schema} onChange={(event) => setHttpDraft({ ...httpDraft, input_schema: event.target.value })} rows={3} className="mt-1 w-full border border-line-strong bg-paper px-2 py-1 font-mono text-[12px] text-ink" /></label>
              <label className="block md:col-span-2"><span className="journal-caps text-[10px] text-muted">Headers（KEY=VALUE，每行一个，只写不回显）</span><textarea value={httpDraft.headers} onChange={(event) => setHttpDraft({ ...httpDraft, headers: event.target.value })} rows={2} className="mt-1 w-full border border-line-strong bg-paper px-2 py-1 font-mono text-[12px] text-ink" /></label>
              <label className="inline-flex items-center gap-1.5"><input type="checkbox" checked={httpDraft.side_effect} onChange={(event) => setHttpDraft({ ...httpDraft, side_effect: event.target.checked })} /><span className="font-serif text-[12px] text-ink-soft">有副作用（需要审批）</span></label>
              <label className="block"><span className="journal-caps text-[10px] text-muted">Scopes（逗号分隔）</span><input value={httpDraft.scopes} onChange={(event) => setHttpDraft({ ...httpDraft, scopes: event.target.value })} placeholder="net, fs.read" className="mt-1 h-8 w-full border border-line-strong bg-paper px-2 text-[12px] text-ink" /></label>
            </div>
            <footer className="flex items-center justify-end gap-1.5 border-t border-line px-4 py-2.5">
              <button type="button" onClick={() => setHttpOpen(false)} className="h-7 border border-line-strong px-2.5 text-[12px] text-ink-soft hover:border-ink hover:text-ink">取消</button>
              <button type="button" onClick={() => void saveHttp()} disabled={Boolean(busy)} className="inline-flex h-7 items-center gap-1 bg-ink px-2.5 text-[12px] font-semibold text-paper hover:bg-black disabled:opacity-50">{busy?.kind === 'save' ? <Loader2 className="h-3 w-3 animate-spin" /> : <Plus className="h-3 w-3" />}保存</button>
            </footer>
          </div>
        </div>
      )}

      {testTarget && (
        <div className="absolute inset-0 z-30 flex items-start justify-center overflow-y-auto bg-ink/20 p-4" role="dialog" aria-modal="true" aria-label="测试工具">
          <div className="w-full max-w-lg border border-ink bg-surface shadow-xl">
            <header className="flex items-center justify-between border-b border-line px-4 py-2.5">
              <h3 className="font-serif text-[15px] font-semibold text-ink">测试 {testTarget.id}</h3>
              <button type="button" onClick={() => setTestTarget(null)} className="h-7 w-7 text-muted hover:text-ink" aria-label="关闭">×</button>
            </header>
            <div className="px-4 py-3">
              <span className="journal-caps text-[10px] text-muted">Arguments（JSON）</span>
              <textarea value={testArgs} onChange={(event) => setTestArgs(event.target.value)} rows={5} className="mt-1 w-full border border-line-strong bg-paper px-2 py-1 font-mono text-[12px] text-ink" />
              <p className="mt-1 font-serif text-[11.5px] text-muted">默认 dry-run：有副作用的工具不会真正执行。</p>
            </div>
            <footer className="flex items-center justify-end gap-1.5 border-t border-line px-4 py-2.5">
              <button type="button" onClick={() => setTestTarget(null)} className="h-7 border border-line-strong px-2.5 text-[12px] text-ink-soft hover:border-ink hover:text-ink">取消</button>
              <button type="button" onClick={() => void runTest()} disabled={Boolean(busy)} className="inline-flex h-7 items-center gap-1 bg-ink px-2.5 text-[12px] font-semibold text-paper hover:bg-black disabled:opacity-50">{busy?.kind === 'test' ? <Loader2 className="h-3 w-3 animate-spin" /> : null}运行测试</button>
            </footer>
          </div>
        </div>
      )}
    </section>
  );
}

export default ToolsSettingsPanel;
