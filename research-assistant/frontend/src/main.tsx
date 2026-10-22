import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import { AppErrorBoundary } from './app/AppErrorBoundary.tsx'
import { AppProviders } from './app/AppProviders.tsx'
import ResearchAssistantApp from './app/ResearchAssistantApp.tsx'

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <AppErrorBoundary>
      <AppProviders>
        <ResearchAssistantApp />
      </AppProviders>
    </AppErrorBoundary>
  </StrictMode>,
)
