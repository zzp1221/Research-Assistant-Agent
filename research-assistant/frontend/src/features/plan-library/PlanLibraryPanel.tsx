import { useCallback, useEffect, useMemo, useState, type FormEvent } from 'react';
import {
  Archive,
  ArchiveRestore,
  Copy,
  Download,
  FolderPlus,
  Loader2,
  Pencil,
  RefreshCw,
  Search,
  Trash2,
  X,
} from 'lucide-react';
import type { PlanSummary, Project } from '../../contracts/api';
import { API_AUTH_TOKEN, API_BASE_URL } from '../../config/env';
import {
  createProject,
  deletePlan,
  deleteProject,
  duplicatePlan,
  listPlanLibrary,
  listProjects,
  patchPlan,
  updateProject,
} from '../../services/api/researchAssistantApi';

interface PlanLibraryPanelProps {
  onClose: () => void;
  onOpenPlan: (planId: string) => void;
}

const STATUS_OPTIONS = [
  ['', '全部状态'],
  ['pending', '待运行'],
  ['in_progress', '运行中'],
  ['awaiting_approval', '待审批'],
  ['awaiting_review', '待审阅'],
  ['awaiting_input', '等待回答'],
  ['completed', '已完成'],
  ['failed', '失败'],
  ['canceled', '已取消'],
] as const;

const INTENT_OPTIONS = [
  '',
  'General',
  'Code_Execution',
  'Paper_Reproduction',
  'Framework_Evaluation',
  'AutoResearch',
  'Custom_Benchmark',
  'Benchmark',
];

const statusLabel = (status: string) => STATUS_OPTIONS.find(([value]) => value === status)?.[1] ?? status;
const outcomeLabel = (outcome?: string) => {
  switch (outcome) {
    case 'completed':
      return '成功';
    case 'failed':
      return '失败';
    case 'blocked':
      return '阻塞';
    case 'canceled':
      return '取消';
    default:
      return '';
  }
};

const formatCost = (plan: PlanSummary): string => {
  const cost = plan.usage?.llm_cost_usd ?? 0;
  if (cost <= 0) return '—';
  return '$' + cost.toFixed(4);
};

const formatDuration = (plan: PlanSummary): string => {
  if (!plan.usage?.sandbox_seconds) return '—';
  return Math.round(plan.usage.sandbox_seconds) + 's';
};

const formatTime = (value: string): string => {
  if (!value) return '';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' });
};

async function downloadExport(planId: string) {
  const response = await fetch(API_BASE_URL + '/api/plans/' + encodeURIComponent(planId) + '/export?format=zip', {
    credentials: 'include',
    headers: API_AUTH_TOKEN ? { Authorization: 'Bearer ' + API_AUTH_TOKEN } : undefined,
  });
  if (!response.ok) {
    throw new Error('导出失败（HTTP ' + response.status + '）');
  }
  const blob = await response.blob();
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = 'plan-' + planId + '.zip';
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  URL.revokeObjectURL(url);
}

export function PlanLibraryPanel({ onClose, onOpenPlan }: PlanLibraryPanelProps) {
  const [plans, setPlans] = useState<PlanSummary[]>([]);
  const [projects, setProjects] = useState<Project[]>([]);
  const [total, setTotal] = useState(0);
  const [cursor, setCursor] = useState('');
  const [query, setQuery] = useState('');
  const [status, setStatus] = useState('');
  const [intentType, setIntentType] = useState('');
  const [projectId, setProjectId] = useState('');
  const [archived, setArchived] = useState<'false' | 'true' | 'all'>('false');
  const [sort, setSort] = useState<'updated_desc' | 'created_desc' | 'cost_desc'>('updated_desc');
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [busyPlanId, setBusyPlanId] = useState('');
  const [renamingId, setRenamingId] = useState('');
  const [renameValue, setRenameValue] = useState('');
  const [newProjectName, setNewProjectName] = useState('');
  const [creatingProject, setCreatingProject] = useState(false);

  const reload = useCallback(async (nextCursor = '') => {
    if (nextCursor) {
      setLoadingMore(true);
    } else {
      setLoading(true);
    }
    setError('');
    try {
      const [planResponse, projectResponse] = await Promise.all([
        listPlanLibrary({
          q: query.trim() || undefined,
          status: status || undefined,
          intent_type: intentType || undefined,
          project_id: projectId || undefined,
          archived,
          sort,
          cursor: nextCursor || undefined,
          limit: 24,
        }),
        nextCursor ? Promise.resolve(projects) : listProjects(true),
      ]);
      if (!nextCursor) {
        setProjects(projectResponse as Project[]);
      }
      setTotal(planResponse.total ?? 0);
      setCursor(planResponse.next_cursor ?? '');
      setPlans((previous) => (nextCursor ? [...previous, ...(planResponse.plans ?? [])] : planResponse.plans ?? []));
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : '加载计划库失败');
    } finally {
      setLoading(false);
      setLoadingMore(false);
    }
  }, [archived, intentType, projectId, projects, query, sort, status]);

  useEffect(() => {
    // Debounce text input so typing does not stampede the plan library.
    const timer = window.setTimeout(() => {
      void reload();
    }, 250);
    return () => window.clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [archived, intentType, projectId, sort, query, status]);

  const applyFilters = () => {
    void reload();
  };

  const projectName = (id?: string) => {
    if (!id) return 'Inbox';
    return projects.find((project) => project.id === id)?.name ?? '未知项目';
  };

  const handleToggleArchive = async (plan: PlanSummary) => {
    setBusyPlanId(plan.id);
    setError('');
    try {
      await patchPlan(plan.id, { archived: !plan.archived });
      setNotice(plan.archived ? '已恢复计划' : '已归档计划');
      await reload();
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : '归档失败');
    } finally {
      setBusyPlanId('');
    }
  };

  const handleMove = async (plan: PlanSummary, nextProjectId: string) => {
    setBusyPlanId(plan.id);
    try {
      await patchPlan(plan.id, { project_id: nextProjectId });
      setNotice('已移动计划');
      await reload();
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : '移动失败');
    } finally {
      setBusyPlanId('');
    }
  };

  const handleDuplicate = async (plan: PlanSummary) => {
    setBusyPlanId(plan.id);
    try {
      await duplicatePlan(plan.id);
      setNotice('已复制计划');
      await reload();
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : '复制失败');
    } finally {
      setBusyPlanId('');
    }
  };

  const handleDelete = async (plan: PlanSummary) => {
    if (!window.confirm('删除计划「' + plan.title + '」？该操作不可恢复，运行中的计划会被拒绝。')) return;
    setBusyPlanId(plan.id);
    try {
      await deletePlan(plan.id, { userId: '', sessionId: '' });
      setNotice('已删除计划');
      await reload();
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : '删除失败（运行中的计划不能删除）');
    } finally {
      setBusyPlanId('');
    }
  };

  const handleExport = async (plan: PlanSummary) => {
    setBusyPlanId(plan.id);
    try {
      await downloadExport(plan.id);
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : '导出失败');
    } finally {
      setBusyPlanId('');
    }
  };

  const submitRename = async () => {
    if (!renamingId) return;
    const title = renameValue.trim();
    if (!title) {
      setRenamingId('');
      return;
    }
    setBusyPlanId(renamingId);
    try {
      await patchPlan(renamingId, { title });
      setRenamingId('');
      setNotice('已重命名');
      await reload();
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : '重命名失败');
    } finally {
      setBusyPlanId('');
    }
  };

  const handleCreateProject = async (event?: FormEvent) => {
    event?.preventDefault();
    const name = newProjectName.trim();
    if (!name || creatingProject) return;
    setCreatingProject(true);
    setError('');
    setNotice('');
    try {
      const project = await createProject({ name });
      setNewProjectName('');
      setNotice(`已创建项目：${project.name}`);
      const refreshed = await listProjects(true);
      setProjects(refreshed);
      setProjectId(project.id);
      setArchived('false');
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : '创建项目失败');
    } finally {
      setCreatingProject(false);
    }
  };

  const handleArchiveProject = async (project: Project) => {
    try {
      await updateProject(project.id, { archived: !project.archived });
      setProjects(await listProjects(true));
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : '项目更新失败');
    }
  };

  const handleDeleteProject = async (project: Project) => {
    if (!window.confirm('删除空项目「' + project.name + '」？包含计划的非空项目会被拒绝。')) return;
    try {
      await deleteProject(project.id);
      setProjects(await listProjects(true));
      if (projectId === project.id) setProjectId('');
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : '删除项目失败（项目内仍有计划）');
    }
  };

  const activeProjects = useMemo(() => projects.filter((project) => !project.archived), [projects]);
  const archivedProjects = useMemo(() => projects.filter((project) => project.archived), [projects]);

  return (
    <section className="flex h-full flex-col overflow-hidden rounded-3xl bg-paper" aria-label="计划库">
      <header className="flex shrink-0 items-start justify-between gap-3 border-b border-line bg-surface px-4 py-3">
        <div>
          <div className="text-[9.5px] font-semibold text-muted">研究工作台</div>
          <h2 className="mt-0.5 text-[18px] font-semibold tracking-[-0.02em] text-ink">计划库</h2>
          <p className="mt-1 text-[11px] leading-5 text-muted">
            服务端保存每一次运行；可按项目、状态、意图搜索，支持重命名、归档、复制与导出。
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-1.5">
          <button type="button" onClick={() => void reload()} className="inline-flex h-8 items-center gap-1 rounded-xl border border-line px-2.5 text-[10.5px] text-ink-soft transition-colors hover:border-accent hover:text-accent">
            <RefreshCw className="h-3 w-3" />
            刷新
          </button>
          <button type="button" onClick={onClose} className="h-8 rounded-xl border border-line px-2.5 text-[10.5px] text-ink-soft transition-colors hover:border-accent hover:text-accent">
            关闭
          </button>
        </div>
      </header>

      <div className="flex min-h-0 flex-1">
        <aside className="flex w-56 shrink-0 flex-col border-r border-line bg-surface">
          <div className="min-h-0 flex-1 overflow-y-auto p-3">
            <h3 className="mb-2 text-[9.5px] font-semibold text-muted">项目</h3>
            <button
              type="button"
              onClick={() => { setProjectId(''); setArchived('false'); }}
              className={'mb-1 w-full rounded-lg px-2 py-1.5 text-left text-[11px] transition-colors ' + (projectId === '' && archived !== 'true' ? 'bg-surface-strong font-semibold text-ink' : 'text-ink-soft hover:bg-surface-muted')}
            >
              全部计划 <span className="journal-num text-[10px] text-muted">{total}</span>
            </button>
            <button
              type="button"
              onClick={() => { setProjectId('inbox'); setArchived('false'); }}
              className={'mb-1 w-full rounded-lg px-2 py-1.5 text-left text-[11px] transition-colors ' + (projectId === 'inbox' ? 'bg-surface-strong font-semibold text-ink' : 'text-ink-soft hover:bg-surface-muted')}
            >
              Inbox
            </button>
            {activeProjects.map((project) => (
              <div key={project.id} className="group mb-0.5 flex items-center">
                <button
                  type="button"
                  onClick={() => { setProjectId(project.id); setArchived('false'); }}
                  className={'min-w-0 flex-1 rounded-lg px-2 py-1.5 text-left text-[11px] transition-colors ' + (projectId === project.id ? 'bg-surface-strong font-semibold text-ink' : 'text-ink-soft hover:bg-surface-muted')}
                >
                  <span className="block truncate">{project.name}</span>
                </button>
                <button type="button" title="归档项目" aria-label={'归档项目 ' + project.name} onClick={() => void handleArchiveProject(project)} className="invisible px-1 text-[10px] text-muted hover:text-ink group-hover:visible">
                  <Archive className="h-3 w-3" />
                </button>
                <button type="button" title="删除空项目" aria-label={'删除项目 ' + project.name} onClick={() => void handleDeleteProject(project)} className="invisible px-1 text-[10px] text-muted hover:text-proof group-hover:visible">
                  <Trash2 className="h-3 w-3" />
                </button>
              </div>
            ))}
            {archivedProjects.length > 0 && (
              <>
                <h3 className="mb-2 mt-4 text-[9.5px] font-semibold text-muted">已归档项目</h3>
                {archivedProjects.map((project) => (
                  <div key={project.id} className="mb-0.5 flex items-center gap-1">
                    <span className="min-w-0 flex-1 truncate px-2 py-1 font-serif text-[12px] text-muted">{project.name}</span>
                    <button type="button" title="恢复项目" onClick={() => void handleArchiveProject(project)} className="px-1 text-muted hover:text-ink">
                      <ArchiveRestore className="h-3 w-3" />
                    </button>
                  </div>
                ))}
              </>
            )}
            <form className="mt-4 border-t border-line pt-3" onSubmit={(event) => void handleCreateProject(event)}>
              <label className="text-[9.5px] font-semibold text-muted" htmlFor="new-project-name">新建项目</label>
              <div className="mt-1 flex gap-1">
                <input
                  id="new-project-name"
                  value={newProjectName}
                  onChange={(event) => setNewProjectName(event.target.value)}
                  placeholder="论文 / 数据集 / 课题"
                  className="h-8 min-w-0 flex-1 rounded-lg border border-line bg-surface px-2 text-[11px] text-ink"
                />
                <button
                  type="submit"
                  disabled={!newProjectName.trim() || creatingProject}
                  aria-label="创建项目"
                  className="inline-flex h-8 shrink-0 items-center gap-1 rounded-lg bg-accent px-2.5 text-[10px] font-semibold text-white transition-colors hover:bg-accent-strong disabled:cursor-not-allowed disabled:bg-surface-strong disabled:text-muted"
                >
                  {creatingProject ? <Loader2 className="h-3 w-3 animate-spin" /> : <FolderPlus className="h-3 w-3" />}
                  创建
                </button>
              </div>
              <p className="mt-1 text-[9.5px] text-muted">输入名称后按 Enter 或点「创建」</p>
            </form>
          </div>
        </aside>

        <div className="flex min-w-0 flex-1 flex-col">
          <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-line bg-surface px-4 py-2">
            <div className="relative min-w-[12rem] flex-1">
              <Search className="pointer-events-none absolute left-2 top-1/2 h-3 w-3 -translate-y-1/2 text-muted" />
              <input
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                onKeyDown={(event) => { if (event.key === 'Enter') applyFilters(); }}
                placeholder="搜索标题或意图…"
                aria-label="搜索计划"
                className="h-8 w-full rounded-lg border border-line bg-surface pl-7 pr-2 text-[11px] text-ink"
              />
            </div>
            <select value={status} onChange={(event) => setStatus(event.target.value)} aria-label="按状态筛选" className="h-8 rounded-lg border border-line bg-surface px-2 text-[11px] text-ink-soft">
              {STATUS_OPTIONS.map(([value, label]) => <option key={value} value={value}>{label}</option>)}
            </select>
            <select value={sort} onChange={(event) => setSort(event.target.value as 'updated_desc' | 'created_desc' | 'cost_desc')} aria-label="排序" className="h-8 rounded-lg border border-line bg-surface px-2 text-[11px] text-ink-soft">
              <option value="updated_desc">最近更新</option>
              <option value="created_desc">最近创建</option>
              <option value="cost_desc">成本最高</option>
            </select>
            <details className="relative">
              <summary className="flex h-8 cursor-pointer list-none items-center gap-1 rounded-lg border border-line bg-surface px-2 text-[11px] text-ink-soft hover:border-accent hover:text-accent">
                筛选
                {intentType || archived === 'true' ? <span className="h-1.5 w-1.5 rounded-full bg-proof" /> : null}
              </summary>
              <div className="absolute left-0 top-[calc(100%+5px)] z-20 w-56 rounded-xl border border-line bg-surface p-2.5 shadow-float">
                <label className="block text-[10px] text-muted">意图
                  <select value={intentType} onChange={(event) => setIntentType(event.target.value)} aria-label="按意图筛选" className="mt-1 h-8 w-full rounded-lg border border-line bg-surface px-2 text-[11px] text-ink-soft">
                    {INTENT_OPTIONS.map((value) => <option key={value} value={value}>{value || '全部意图'}</option>)}
                  </select>
                </label>
                <label className="mt-2 flex items-center gap-1.5 border-t border-line pt-2 font-serif text-[11.5px] text-ink-soft">
                  <input type="checkbox" checked={archived === 'true'} onChange={(event) => setArchived(event.target.checked ? 'true' : 'false')} />
                  只看已归档
                </label>
              </div>
            </details>
            <button type="button" onClick={applyFilters} className="h-8 rounded-lg bg-accent px-3 text-[11px] font-semibold text-white transition-colors hover:bg-accent-strong">搜索</button>
          </div>

          {(error || notice) && (
            <div className="shrink-0 px-4 pt-2">
              {error && <div className="rounded-xl border border-proof/30 bg-proof-soft px-3 py-2 text-[11px] text-proof">{error}</div>}
              {notice && <div className="rounded-xl border border-ok/30 bg-ok-soft px-3 py-2 text-[11px] text-ok">{notice}</div>}
            </div>
          )}

          <div className="min-h-0 flex-1 overflow-y-auto p-4">
            {loading ? (
              <div className="flex items-center gap-2 py-10 text-[12px] text-muted"><Loader2 className="h-4 w-4 animate-spin" />正在加载计划…</div>
            ) : plans.length === 0 ? (
              <p className="rounded-2xl border border-dashed border-line px-4 py-10 text-center text-[11px] text-muted">没有符合条件的计划。</p>
            ) : (
              <>
                <div className="grid gap-2 lg:grid-cols-2 2xl:grid-cols-3">
                  {plans.map((plan) => (
                    <article key={plan.id} className="flex flex-col rounded-2xl border border-line bg-surface p-3 transition-colors hover:border-accent/40 hover:shadow-card">
                      <div className="flex items-start justify-between gap-2">
                        <button type="button" onClick={() => { onOpenPlan(plan.id); onClose(); }} className="min-w-0 flex-1 text-left">
                          <h3 className="truncate text-[13px] font-semibold text-ink hover:text-accent">{plan.title || '未命名计划'}</h3>
                          <p className="mt-0.5 font-mono text-[9.5px] text-muted">{plan.id.slice(0, 8).toUpperCase()} · {plan.intent_type} · {projectName(plan.project_id)}</p>
                        </button>
                        <span className={'shrink-0 rounded-full px-2 py-0.5 text-[9.5px] font-medium ' + (plan.status === 'completed' ? 'bg-ok-soft text-ok' : plan.status === 'failed' ? 'bg-proof-soft text-proof' : plan.status === 'in_progress' ? 'bg-warn-soft text-warn' : 'bg-surface-muted text-muted')}>
                          {statusLabel(plan.status)}{plan.outcome ? ' · ' + outcomeLabel(plan.outcome) : ''}
                        </span>
                      </div>
                      <div className="mt-2 grid grid-cols-4 gap-1 border-t border-line pt-2 font-mono text-[9.5px] text-ink-soft">
                        <span>{plan.completed_nodes}/{plan.total_nodes} 节点</span>
                        <span>{formatCost(plan)}</span>
                        <span>{formatDuration(plan)}</span>
                        <span className="text-right text-muted">{formatTime(plan.updated_at)}</span>
                      </div>
                      {plan.failed_nodes > 0 && <p className="mt-1 font-serif text-[11px] text-proof">{plan.failed_nodes} 个失败节点</p>}
                      <div className="mt-2 flex items-center gap-1 border-t border-line pt-2 text-[11px]">
                        <button type="button" onClick={() => { onOpenPlan(plan.id); onClose(); }} className="rounded-lg border border-line px-2 py-1 text-[10px] text-ink-soft transition-colors hover:border-accent hover:text-accent">打开</button>
                        <details className="relative">
                          <summary className="list-none cursor-pointer rounded-lg border border-line px-2 py-1 text-[10px] text-ink-soft transition-colors hover:border-accent hover:text-accent">更多</summary>
                          <div className="absolute left-0 top-[calc(100%+4px)] z-30 w-44 rounded-xl border border-line bg-surface p-1.5 shadow-float">
                            <button type="button" onClick={() => { setRenamingId(plan.id); setRenameValue(plan.title); }} className="flex w-full items-center gap-1.5 px-2.5 py-1.5 text-left text-[11px] hover:bg-surface-muted"><Pencil className="h-3 w-3" />重命名</button>
                            <button type="button" onClick={() => void handleToggleArchive(plan)} disabled={busyPlanId === plan.id} className="flex w-full items-center gap-1.5 px-2.5 py-1.5 text-left text-[11px] hover:bg-surface-muted disabled:opacity-40"><Archive className="h-3 w-3" />{plan.archived ? '恢复计划' : '归档计划'}</button>
                            <button type="button" onClick={() => void handleDuplicate(plan)} disabled={busyPlanId === plan.id} className="flex w-full items-center gap-1.5 px-2.5 py-1.5 text-left text-[11px] hover:bg-surface-muted disabled:opacity-40"><Copy className="h-3 w-3" />复制计划</button>
                            <button type="button" onClick={() => void handleExport(plan)} disabled={busyPlanId === plan.id} className="flex w-full items-center gap-1.5 px-2.5 py-1.5 text-left text-[11px] hover:bg-surface-muted disabled:opacity-40"><Download className="h-3 w-3" />导出 ZIP</button>
                            <label className="mt-1 block border-t border-line px-2.5 py-1.5 text-[10px] text-muted">移动到
                              <select value={plan.project_id ?? ''} onChange={(event) => void handleMove(plan, event.target.value)} aria-label={'移动计划 ' + plan.title} className="mt-1 h-6 w-full border border-line-strong bg-paper px-1 text-[10px] text-ink-soft">
                                <option value="">Inbox</option>
                                {activeProjects.map((project) => <option key={project.id} value={project.id}>{project.name}</option>)}
                              </select>
                            </label>
                            <button type="button" onClick={() => void handleDelete(plan)} disabled={busyPlanId === plan.id} className="mt-1 flex w-full items-center gap-1.5 border-t border-line px-2.5 py-1.5 text-left text-[11px] text-proof hover:bg-proof-soft disabled:opacity-40"><Trash2 className="h-3 w-3" />删除计划</button>
                          </div>
                        </details>
                        <span className="ml-auto font-mono text-[9px] text-muted">{formatTime(plan.updated_at)}</span>
                      </div>
                      {renamingId === plan.id && (
                        <div className="mt-2 flex gap-1 border-t border-line pt-2">
                          <input ref={(element) => { if (element && document.activeElement !== element) element.focus(); }} value={renameValue} onChange={(event) => setRenameValue(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter') void submitRename(); if (event.key === 'Escape') setRenamingId(''); }} className="h-7 min-w-0 flex-1 border border-line-strong bg-paper px-2 text-[12px]" aria-label="新标题" />
                          <button type="button" onClick={() => void submitRename()} className="h-7 bg-ink px-2 text-[11px] text-paper">保存</button>
                          <button type="button" onClick={() => setRenamingId('')} className="h-7 border border-line-strong px-2 text-[11px] text-ink-soft"><X className="h-3 w-3" /></button>
                        </div>
                      )}
                    </article>
                  ))}
                </div>
                {cursor && (
                  <div className="mt-3 flex justify-center">
                    <button type="button" onClick={() => void reload(cursor)} disabled={loadingMore} className="inline-flex h-8 items-center gap-1 border border-line-strong px-4 text-[12px] text-ink-soft hover:border-ink hover:text-ink disabled:opacity-50">
                      {loadingMore ? <Loader2 className="h-3 w-3 animate-spin" /> : null}
                      加载更多（{plans.length}/{total}）
                    </button>
                  </div>
                )}
              </>
            )}
          </div>
        </div>
      </div>
    </section>
  );
}