import { useCallback, useEffect, useRef, useState } from 'react';
import type { BudgetAlert, UserBudget } from '../../contracts/api';
import { getBudget, getBudgetAlerts, getUsageSummary } from '../../services/api/researchAssistantApi';

type BudgetSummary = Awaited<ReturnType<typeof getUsageSummary>>;

export interface WorkbenchBudgetState {
  summary: BudgetSummary | null;
  budget: UserBudget | null;
  alerts: BudgetAlert[];
  loading: boolean;
  error: string;
  refresh: () => void;
}

/**
 * Workbench-level budget snapshot. The cost console owns editing; this hook
 * only feeds the status ribbon and refreshes when the active plan changes.
 */
export function useWorkbenchBudget(refreshKey?: string | null): WorkbenchBudgetState {
  const [summary, setSummary] = useState<BudgetSummary | null>(null);
  const [budget, setBudget] = useState<UserBudget | null>(null);
  const [alerts, setAlerts] = useState<BudgetAlert[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const mountedRef = useRef(true);

  const reload = useCallback(async () => {
    try {
      const [summaryResponse, budgetResponse, alertsResponse] = await Promise.all([
        getUsageSummary('day'),
        getBudget(),
        getBudgetAlerts(),
      ]);
      if (!mountedRef.current) return;
      setSummary(summaryResponse ?? null);
      setBudget(budgetResponse);
      setAlerts(alertsResponse ?? []);
      setError('');
    } catch (loadError) {
      if (!mountedRef.current) return;
      setError(loadError instanceof Error ? loadError.message : '预算数据不可用');
    } finally {
      if (mountedRef.current) setLoading(false);
    }
  }, []);

  useEffect(() => {
    mountedRef.current = true;
    queueMicrotask(() => void reload());
    const timer = window.setInterval(() => void reload(), 30000);
    return () => {
      mountedRef.current = false;
      window.clearInterval(timer);
    };
  }, [reload, refreshKey]);

  return { summary, budget, alerts, loading, error, refresh: () => void reload() };
}
