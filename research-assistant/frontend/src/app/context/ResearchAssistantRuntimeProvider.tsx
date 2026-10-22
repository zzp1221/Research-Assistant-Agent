import type { PropsWithChildren } from 'react';
import { ResearchAssistantRuntimeContext, type ResearchAssistantRuntimeContextValue } from './ResearchAssistantRuntimeContext';

interface ResearchAssistantRuntimeProviderProps extends PropsWithChildren {
  value: ResearchAssistantRuntimeContextValue;
}

export function ResearchAssistantRuntimeProvider({ value, children }: ResearchAssistantRuntimeProviderProps) {
  return <ResearchAssistantRuntimeContext.Provider value={value}>{children}</ResearchAssistantRuntimeContext.Provider>;
}
