import { useCallback, useEffect, useMemo, useState } from 'react';
import { AlertTriangle, Info, Loader2, RefreshCw, Wallet } from 'lucide-react';
import type {
  BudgetAlert,
  UsageTimeseriesPoint,
  UserBudget,
} from '../../contracts/api';
import {
  getBudget,
  getBudgetAlerts,
  getUsageSummary,
  getUsageTimeseries,
  saveBudget,
} from '../../services/api/researchAssistantApi';

interface CostConsolePanelProps {
  onClose: () => void;
}

interface SummaryState {
  today_usd: number;
  month_usd: number;
  spent_usd: number;
  reserved_usd: number;
  limit_usd: number;
  remaining_usd: number;
  policy?: string;
  configured: boolean;
  top_plans: { plan_id: string; cost_usd: number; calls: number }[];
  top_models: { model: string; cost_usd: number; calls: number }[];
}

const emptySummary: SummaryState = {
  today_usd: 0,
  month_usd: 0,
  spent_usd: 0,
  reserved_usd: 0,
  limit_usd: 0,
  remaining_usd: 0,
  configured: false,
  top_plans: [],
  top_models: [],
};

const money = (value?: number): string => '$' + (value ?? 0).toFixed(4);
const money2 = (value?: number): string => '$' + (value ?? 0).toFixed(2);

export function CostConsolePanel({ onClose }: CostConsolePanelProps) {
  const [period, setPeriod] = useState<'day' | 'month'>('day');
  const [summary, setSummary] = useState<SummaryState>(emptySummary);
  const [budget, setBudget] = useState<UserBudget | null>(null);
  const [alerts, setAlerts] = useState<BudgetAlert[]>([]);
  const [points, setPoints] = useState<UsageTimeseriesPoint[]>([]);
  const [group, setGroup] = useState<'day' | 'model' | 'plan'>('day');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [saving, setSaving] = useState(false);
  const [form, setForm] = useState({ period: 'day' as 'day' | 'month', limit: '', policy: 'soft' as 'soft' | 'hard' });

  const reload = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const [summaryResponse, budgetResponse, alertsResponse, pointsResponse] = await Promise.all([
        getUsageSummary(period),
        getBudget(),
        getBudgetAlerts(),
        getUsageTimeseries(group),
      ]);
      setSummary({ ...emptySummary, ...summaryResponse } as SummaryState);
      setBudget(budgetResponse);
      setAlerts(alertsResponse);
      setPoints(pointsResponse);
      if (budgetResponse) {
        setForm({ period: budgetResponse.period, limit: String(budgetResponse.limit_usd), policy: budgetResponse.policy });
      }
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : '加载成本数据失败');
    } finally {
      setLoading(false);
    }
  }, [group, period]);

  useEffect(() => {
    queueMicrotask(() => void reload());
  }, [reload]);

  const handleSave = async () => {
    const limit = Number(form.limit);
    if (!Number.isFinite(limit) || limit < 0) {
      setError('预算限额必须是非负数字');
      return;
    }
    setSaving(true);
    setError('');
    try {
      const saved = await saveBudget({ period: form.period, limit_usd: limit, policy: form.policy });
      setBudget(saved);
      setNotice('预算已保存');
      await reload();
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : '保存预算失败');
    } finally {
      setSaving(false);
    }
  };

  const progress = useMemo(() => {
    if (!summary.configured || summary.limit_usd <= 0) return 0;
    return Math.min(100, Math.round(((summary.spent_usd + summary.reserved_usd) / summary.limit_usd) * 100));
  }, [summary]);

  const maxPoint = useMemo(() => Math.max(0.0001, ...points.map((point) => point.cost_usd)), [points]);

  return (
    <section className="flex h-full flex-col overflow-hidden bg-paper" aria-label="成本与预算控制台">
      <header className="flex h-16 shrink-0 items-center justify-between gap-4 border-b border-line bg-surface px-5">
        <div className="flex min-w-0 items-center gap-2">
          <Wallet className="h-4 w-4 text-ink-soft" />
          <div>
            <div className="text-[9px] font-semibold text-muted">研究工作台</div>
            <h2 className="text-[17px] font-semibold leading-5 tracking-[-0.02em] text-ink">成本与预算</h2>
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-2">
          <div className="flex rounded-xl bg-surface-muted p-0.5" role="group" aria-label="成本周期">
            {(['day', 'month'] as const).map((value) => (
              <button
                key={value}
                type="button"
                onClick={() => setPeriod(value)}
                className={`h-7 rounded-lg px-3 text-[10.5px] font-semibold transition-colors ${period === value ? 'bg-white text-ink shadow-sm' : 'text-muted hover:text-ink'}`}
              >
                {value === 'day' ? '今日' : '本月'}
              </button>
            ))}
          </div>
          <button type="button" onClick={() => void reload()} className="inline-flex h-8 items-center gap-1 rounded-xl border border-line px-2.5 text-[10.5px] text-ink-soft transition-colors hover:border-accent hover:text-accent">
            <RefreshCw className="h-3 w-3" /> 刷新
          </button>
          <button type="button" onClick={onClose} className="h-8 rounded-xl border border-line px-2.5 text-[10.5px] text-ink-soft transition-colors hover:border-accent hover:text-accent">
            关闭
          </button>
        </div>
      </header>

      <div className="min-h-0 flex-1 overflow-y-auto p-5">
        {error && <div className="mb-4 rounded-xl border border-proof/30 bg-proof-soft px-3 py-2 text-[11px] text-proof">{error}</div>}
        {notice && <div className="mb-4 rounded-xl border border-ok/30 bg-ok-soft px-3 py-2 text-[11px] text-ok">{notice}</div>}
        {loading ? (
          <div className="flex items-center gap-2 py-12 text-[12px] text-muted"><Loader2 className="h-4 w-4 animate-spin" />正在加载成本数据…</div>
        ) : (
          <div className="grid gap-4 xl:grid-cols-[minmax(0,1.35fr)_minmax(320px,.65fr)]">
            <div className="space-y-4">
              <section className="rounded-2xl border border-line bg-surface p-4 shadow-card">
                <div className="flex items-start justify-between gap-4">
                  <div>
                    <div className="text-[9px] font-semibold text-muted">预算状态</div>
                    <div className="mt-1 flex flex-wrap items-baseline gap-2">
                      <span className="font-mono text-[30px] font-semibold leading-none text-ink">{summary.configured ? money2(summary.spent_usd + summary.reserved_usd) : '未设置'}</span>
                      <span className="font-mono text-[12px] text-muted">{summary.configured ? `/ ${money2(summary.limit_usd)}` : '每日预算'}</span>
                      <span className={`rounded-full px-2 py-0.5 text-[9px] font-medium ${summary.policy === 'hard' ? 'bg-proof-soft text-proof' : 'bg-surface-muted text-muted'}`}>
                        {summary.policy === 'hard' ? 'hard · 超限取消' : 'soft · 超限暂停'}
                      </span>
                    </div>
                    <p className="mt-1 text-[10.5px] text-muted">
                      今日 {money(summary.today_usd)} · 本月 {money(summary.month_usd)}
                    </p>
                  </div>
                  <div className="text-right">
                    <div className="font-mono text-[11px] text-ink-soft">{progress}%</div>
                    <div className="mt-1 h-1.5 w-24 overflow-hidden rounded-full bg-paper-deep"><div className={`h-full rounded-full ${progress >= 100 ? 'bg-proof' : progress >= 80 ? 'bg-warn' : 'bg-ok'}`} style={{ width: `${progress}%` }} /></div>
                  </div>
                </div>
                <div className="mt-3 h-2 w-full overflow-hidden rounded-full bg-paper-deep">
                  <div className={`h-2 rounded-full ${progress >= 100 ? 'bg-proof' : progress >= 80 ? 'bg-warn' : 'bg-ok'}`} style={{ width: `${progress}%` }} />
                </div>
                <div className="mt-1 flex flex-wrap justify-between gap-2 text-[9.5px] text-muted">
                  <span>{progress}%（已用 {money(summary.spent_usd)} / 预留 {money(summary.reserved_usd)}）</span>
                  <span>限额 {summary.configured ? money(summary.limit_usd) : '—'}</span>
                </div>
                <div className="mt-3 grid grid-cols-3 gap-2 border-t border-line pt-3">
                  <div><div className="text-[8.5px] font-semibold text-muted">已用</div><div className="mt-0.5 font-mono text-[13px] text-ink">{money(summary.spent_usd)}</div></div>
                  <div><div className="text-[8.5px] font-semibold text-muted">预留</div><div className="mt-0.5 font-mono text-[13px] text-ink">{money(summary.reserved_usd)}</div></div>
                  <div><div className="text-[8.5px] font-semibold text-muted">剩余</div><div className={`mt-0.5 font-mono text-[13px] ${summary.configured && summary.remaining_usd <= 0 ? 'text-proof' : 'text-ink'}`}>{summary.configured ? money(summary.remaining_usd) : '—'}</div></div>
                </div>
                <div className="mt-3 flex flex-wrap items-center gap-1 border-t border-line pt-3 text-[9.5px] text-muted">
                  <span className="border border-line px-1.5 py-0.5">任务派发前预留</span><span>→</span>
                  <span className="border border-line px-1.5 py-0.5">完成后结算</span><span>→</span>
                  <span className="border border-warn/30 bg-warn-soft px-1.5 py-0.5 text-warn">80% 预警</span><span>→</span>
                  <span className="border border-proof/30 bg-proof-soft px-1.5 py-0.5 text-proof">100% 按策略处理</span>
                </div>
              </section>

              <section className="rounded-2xl border border-line bg-surface p-3.5">
                <div className="mb-2 flex items-center justify-between">
                  <h3 className="text-[9.5px] font-semibold text-ink">花费趋势</h3>
                  <select value={group} onChange={(event) => setGroup(event.target.value as 'day' | 'model' | 'plan')} className="h-6 border border-line-strong bg-paper px-1 text-[10px] text-ink-soft" aria-label="趋势分组">
                    <option value="day">按日</option>
                    <option value="model">按模型</option>
                    <option value="plan">按计划</option>
                  </select>
                </div>
                {points.length === 0 ? <p className="py-4 font-serif text-[11.5px] text-muted">暂无趋势数据</p> : (
                  <div className="flex h-32 items-end gap-1 border-b border-line pb-1">
                    {points.map((point) => (
                      <div key={point.key} className="group relative flex min-w-0 flex-1 flex-col items-center justify-end" title={point.key + ' · ' + money(point.cost_usd)}>
                        <div className="w-full bg-ink/70 group-hover:bg-ink" style={{ height: Math.max(2, Math.round((point.cost_usd / maxPoint) * 100)) + '%' }} />
                        <span className="mt-1 w-full truncate text-center font-mono text-[8px] text-muted">{point.key}</span>
                      </div>
                    ))}
                  </div>
                )}
              </section>

              <div className="grid gap-3 md:grid-cols-2">
                <section className="rounded-2xl border border-line bg-surface p-3.5">
                  <h3 className="mb-2 text-[9.5px] font-semibold text-ink">Top 计划</h3>
                  {summary.top_plans.length === 0 ? <p className="font-serif text-[11.5px] text-muted">暂无记录</p> : (
                    <ul className="space-y-1">
                      {summary.top_plans.map((plan) => (
                        <li key={plan.plan_id} className="flex items-center justify-between gap-2 font-mono text-[10.5px]">
                          <span className="truncate text-ink-soft" title={plan.plan_id}>{plan.plan_id.slice(0, 12)}</span>
                          <span className="shrink-0 text-ink">{money(plan.cost_usd)}</span>
                        </li>
                      ))}
                    </ul>
                  )}
                </section>
                <section className="rounded-2xl border border-line bg-surface p-3.5">
                  <h3 className="mb-2 text-[9.5px] font-semibold text-ink">Top 模型</h3>
                  {summary.top_models.length === 0 ? <p className="font-serif text-[11.5px] text-muted">暂无记录</p> : (
                    <ul className="space-y-1">
                      {summary.top_models.map((model) => (
                        <li key={model.model} className="flex items-center justify-between gap-2 font-mono text-[10.5px]">
                          <span className="truncate text-ink-soft" title={model.model}>{model.model}</span>
                          <span className="shrink-0 text-ink">{money(model.cost_usd)}</span>
                        </li>
                      ))}
                    </ul>
                  )}
                </section>
              </div>
            </div>

            <aside className="space-y-4">
              <section className="rounded-2xl border border-line bg-surface p-4 shadow-card">
                <div className="flex items-start justify-between gap-2">
                  <div><div className="text-[9px] font-semibold text-muted">预算设置</div><h3 className="mt-0.5 text-[15px] font-semibold text-ink">每日限额</h3></div>
                  {budget ? <span className="border border-line px-1.5 py-0.5 font-mono text-[9px] text-muted">{budget.period === 'day' ? 'DAILY' : 'MONTHLY'}</span> : null}
                </div>
                <div className="mt-3 space-y-2">
                  <div className="flex gap-1.5">
                    <select value={form.period} onChange={(event) => setForm((prev) => ({ ...prev, period: event.target.value as 'day' | 'month' }))} className="h-8 w-20 border border-line-strong bg-paper px-1 text-[11px]">
                      <option value="day">每日</option>
                      <option value="month">每月</option>
                    </select>
                    <input value={form.limit} onChange={(event) => setForm((prev) => ({ ...prev, limit: event.target.value }))} placeholder="USD 限额" inputMode="decimal" className="h-8 min-w-0 flex-1 border border-line-strong bg-paper px-2 text-[11px]" aria-label="预算限额（USD）" />
                  </div>
                  <select value={form.policy} onChange={(event) => setForm((prev) => ({ ...prev, policy: event.target.value as 'soft' | 'hard' }))} className="h-8 w-full border border-line-strong bg-paper px-1 text-[11px]" aria-label="超预算策略">
                    <option value="soft">soft · 到达上限后暂停，等待人工处理</option>
                    <option value="hard">hard · 到达上限后直接取消任务</option>
                  </select>
                  <button type="button" onClick={() => void handleSave()} disabled={saving} className="h-8 w-full bg-ink text-[11.5px] font-semibold text-paper hover:bg-black disabled:opacity-50">{saving ? '保存中…' : '保存预算'}</button>
                  {budget ? <p className="text-[10px] text-muted">当前：{budget.period === 'day' ? '每日' : '每月'} {money(budget.limit_usd)} · {budget.policy}</p> : <p className="text-[10px] text-muted">还没有设置预算；设置后会在任务派发前预留额度。</p>}
                </div>
              </section>

              <section className="rounded-2xl border border-line bg-surface p-3.5">
                <h3 className="mb-2 flex items-center gap-1 text-[9.5px] font-semibold text-ink"><AlertTriangle className="h-3 w-3" />预算预警历史</h3>
                {alerts.length === 0 ? <p className="font-serif text-[11.5px] text-muted">暂无预警</p> : (
                  <ul className="space-y-1">
                    {alerts.map((alert) => (
                      <li key={alert.id} className="flex flex-wrap items-baseline justify-between gap-2 border-b border-line py-1 text-[10px] last:border-0">
                        <span className={alert.kind === 'exceeded' ? 'text-proof' : 'text-warn'}>{alert.kind === 'exceeded' ? '超限' : '80% 预警'} · {alert.scope === 'user' ? '账户' : '计划'} · {alert.message}</span>
                        <span className="font-mono text-[9.5px] text-muted">{money(alert.spent_usd)} / {money(alert.limit_usd)}</span>
                      </li>
                    ))}
                  </ul>
                )}
              </section>

              <section className="border border-line bg-surface-muted p-3 text-[13px] leading-5 text-muted">
                <div className="flex items-center gap-1.5 text-ink-soft"><Info className="h-3 w-3" />金额口径</div>
                <p className="mt-1">所有金额来自实际任务 usage；未配置单价的调用标记为 estimated / unknown。预算在任务派发前预留、完成后结算。</p>
              </section>
            </aside>
          </div>
        )}
      </div>
    </section>
  );
}

export default CostConsolePanel;
