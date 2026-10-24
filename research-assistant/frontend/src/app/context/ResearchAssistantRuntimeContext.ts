import { createContext, useContext } from 'react';
import type { useResearchAssistantRuntime } from '../hooks/useResearchAssistantRuntime';

type ResearchAssistantRuntimeApi = ReturnType<typeof useResearchAssistantRuntime>;

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
