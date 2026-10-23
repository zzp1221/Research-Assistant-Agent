import { Bot, Code2, Database, FileText, FlaskConical, Gauge, TerminalSquare } from 'lucide-react';

const agentLabels: Record<string, string> = {
  librarian_agent: '文献智能体',
  coder_agent: '代码智能体',
  sandbox_agent: '沙箱执行器',
  data_agent: '数据智能体',
  benchmark_agent: '评测智能体',
  research_coding_agent: '科研 Coding 智能体',
  general_agent: '通用智能体',
};

export const getAgentLabel = (agentName: string) => agentLabels[agentName] ?? agentName;

export const getAgentIcon = (agentName: string) => {
  switch (agentName) {
    case 'librarian_agent':
      return <FileText className="h-4 w-4 text-accent" />;
    case 'coder_agent':
      return <Code2 className="h-4 w-4 text-accent" />;
    case 'sandbox_agent':
      return <TerminalSquare className="h-4 w-4 text-accent" />;
    case 'data_agent':
      return <Database className="h-4 w-4 text-accent" />;
    case 'benchmark_agent':
      return <Gauge className="h-4 w-4 text-accent" />;
    case 'research_coding_agent':
      return <FlaskConical className="h-4 w-4 text-accent" />;
    default:
      return <Bot className="h-4 w-4 text-muted" />;
  }
};

const neutral = { borderColor: '#e2dcce', backgroundColor: '#fffdf8', borderLeftColor: '#c9c2b1' };

export const getTaskStyleByStatus = (status?: string) => {
  switch (status) {
    case 'ready':
      return { ...neutral, borderLeftColor: '#3e5c76' };
    case 'in_progress':
      return { ...neutral, borderLeftColor: '#8a5a12' };
    case 'completed':
      return { ...neutral, borderLeftColor: '#2f6b4f' };
    case 'failed':
      return { ...neutral, borderLeftColor: '#a5483a' };
    case 'blocked':
      return { ...neutral, borderLeftColor: '#6e685d' };
    case 'canceled':
    case 'skipped':
      return { ...neutral, borderLeftColor: '#989184' };
    case 'awaiting_approval':
      return { ...neutral, borderLeftColor: '#6b5a7a' };
    default:
      return neutral;
  }
};
