import { createContext, useContext } from 'react';
import type { useResearchAssistantRuntime } from '../hooks/useResearchAssistantRuntime';

type ResearchAssistantRuntimeApi = ReturnType<typeof useResearchAssistantRuntime>;

/**
 * Connection lifecycle of the plan observation stream (UI-02).
 *
 * Deliberately separate from the business plan status: losing the stream must
 * never turn a running plan into a failed one, nor reopen the run button.
 */
export type PlanConnectionState = 'idle' | 'connecting' | 'live' | 'reconnecting' | 'interrupted';

export interface ObservePlanOptions {
  /** The execute request issued from this view already returned 202. */
  startAccepted?: boolean;
}

/**
 * Structural view of the observation fields the runtime adds to
 * `executionState`. Every field is optional so the workbench keeps working
 * while the runtime hook and this UI land in parallel.
 */
export interface PlanObservationExecutionFields {
  connectionState?: PlanConnectionState;
  connectionDetail?: string;
  startAccepted?: boolean;
  serverPlanStatus?: string;
  planPaused?: boolean;
  observedPlanId?: string;
}

export interface ResearchAssistantRuntimeContextValue {
  state: {
    executionState: ResearchAssistantRuntimeApi['executionState'];
    selectedTaskState: ResearchAssistantRuntimeApi['selectedTaskState'];
  };
  actions: {
    onNodeClick: ResearchAssistantRuntimeApi['onNodeClick'];
    handleOpenTaskView: ResearchAssistantRuntimeApi['handleOpenTaskView'];
    handleExecuteTask: ResearchAssistantRuntimeApi['handleExecuteTask'];
    handleRunAllTasks: ResearchAssistantRuntimeApi['handleRunAllTasks'];
	handleApproveAndRun: ResearchAssistantRuntimeApi['handleApproveAndRun'];
	handleRuntimeActionDecision: ResearchAssistantRuntimeApi['handleRuntimeActionDecision'];
	handleCancelPlan: ResearchAssistantRuntimeApi['handleCancelPlan'];
	handleRetryFailedPlan: ResearchAssistantRuntimeApi['handleRetryFailedPlan'];
    setDisplayMode: ResearchAssistantRuntimeApi['setDisplayMode'];
    closeTaskPanel: ResearchAssistantRuntimeApi['closeTaskPanel'];
    resetRuntimeState: ResearchAssistantRuntimeApi['resetRuntimeState'];
    /**
     * Start (or restart) the observation flow for a plan without executing,
     * retrying or resuming it. Optional because older tests and the runtime
     * land independently; callers must use optional chaining.
     */
    observePlan?: (planId: string, options?: ObservePlanOptions) => Promise<void>;
    /** Rebuild an interrupted observation flow; never executes the plan. */
    handleReconnect?: () => Promise<void>;
    /**
     * Resume an already-started but paused plan through the backend resume
     * endpoint. Declared here with an explicit signature (not an indexed
     * runtime type) so the canvas can land before the runtime hook exports it.
     */
    handleResumePlan?: (activePlanId: string) => Promise<void>;
  };
  meta: {
    appendSelectedTaskLog: ResearchAssistantRuntimeApi['appendSelectedTaskLog'];
  };
}

export const ResearchAssistantRuntimeContext = createContext<ResearchAssistantRuntimeContextValue | null>(null);

export function useResearchAssistantRuntimeContext() {
  const context = useContext(ResearchAssistantRuntimeContext);
  if (!context) {
    throw new Error('useResearchAssistantRuntimeContext must be used within ResearchAssistantRuntimeProvider');
  }
  return context;
}
