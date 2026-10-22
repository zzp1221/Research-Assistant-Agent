import { useCallback, useEffect, useRef } from 'react';
import type { NodeExecutionState, PlanGraph } from '../../contracts/api';
import { getPlanEvents } from '../../services/api/researchAssistantApi';
import type { RequestIdentity } from '../../services/api/researchAssistantApi';

export type PlanHistoryTarget = Pick<PlanGraph, 'id' | 'status'>;

export interface PlanHistoryHydrator {
  applyHistory: (plan: PlanHistoryTarget | null) => Promise<void>;
}

type PatchNodeState = (taskId: string, updater: (previous: NodeExecutionState) => NodeExecutionState) => void;

/**
 * Rebuilds node logs/results from the durable event log after a refresh or a
 * session switch. The live SSE stream only covers the current browser session,
 * so without this the execution panel would look empty for finished plans.
 */
export function usePlanHistoryHydrator(patchNodeState: PatchNodeState, identity: RequestIdentity): PlanHistoryHydrator {
  const hydratedPlanRef = useRef<string | null>(null);

  const applyHistory = useCallback(
    async (plan: PlanHistoryTarget | null) => {
      if (!plan || plan.status === 'pending') return;
      if (hydratedPlanRef.current === plan.id) return;
      hydratedPlanRef.current = plan.id;
      try {
        const events = await getPlanEvents(plan.id, identity);
        const perTask: Record<string, Partial<NodeExecutionState>> = {};
        for (const event of events) {
          if (!event.task_id) continue;
          const patch = perTask[event.task_id] ?? {};
          if (event.event_type === 'task_log') {
            const message = String(event.payload?.message || '');
            if (message) {
              patch.logs = patch.logs ? `${patch.logs}\n${message}` : message;
            }
          } else if (event.event_type === 'task_completed' || event.event_type === 'task_failed') {
            const result = String(event.payload?.result || event.payload?.result_summary || '');
            const code = String(event.payload?.code || '');
            const structured = String(event.payload?.structured_data || '');
            const error = String(event.payload?.error || '');
            if (result) patch.result = result;
            if (code) patch.code = code;
            if (structured) patch.structuredData = structured;
            const line = event.event_type === 'task_completed' ? '[Plan] task_completed' : `[Plan Error] ${error || 'task failed'}`;
            patch.logs = patch.logs ? `${patch.logs}\n${line}` : line;
          }
          perTask[event.task_id] = patch;
        }
        for (const [taskId, patch] of Object.entries(perTask)) {
          patchNodeState(taskId, (previous) => ({
            logs: previous.logs || patch.logs || '',
            result: previous.result || patch.result || '',
            code: previous.code || patch.code || '',
            structuredData: previous.structuredData || patch.structuredData || '',
            imageBase64: previous.imageBase64 || patch.imageBase64 || '',
          }));
        }
      } catch (error) {
        console.warn('[plan] failed to hydrate history', error);
        hydratedPlanRef.current = null;
      }
    },
    [identity, patchNodeState],
  );

  // A new session may legitimately reuse the component; reset the guard when
  // the runtime is remounted for a different identity.
  useEffect(() => () => {
    hydratedPlanRef.current = null;
  }, []);

  return { applyHistory };
}
