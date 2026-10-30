import { Bot, Code2, Database, FileText, FlaskConical, Gauge, TerminalSquare } from 'lucide-react';

const agentLabels: Record<string, string> = {
  librarian_agent: '文献智能体',
  coder_agent: '代码智能体',
  sandbox_agent: '沙箱执行器',
  data_agent: '数据智能体',
  benchmark_agent: '评测智能体',
  research_coding_agent: '科研 Coding 智能体',
  general_agent: '通用智能体',
  answer_agent: '回答智能体',
  tool_executor: '工具执行器',
};

export const getAgentLabel = (agentName: string) => {
  if (agentName.startsWith('profile:')) {
    return `自定义 Agent · ${agentName.slice(8, 16)}`;
  }
  return agentLabels[agentName] ?? agentName;
};

export const getAgentIcon = (agentName: string, className = 'h-3.5 w-3.5 text-muted') => {
  switch (agentName) {
    case 'librarian_agent':
      return <FileText className={className} />;
    case 'coder_agent':
      return <Code2 className={className} />;
    case 'sandbox_agent':
      return <TerminalSquare className={className} />;
    case 'data_agent':
      return <Database className={className} />;
    case 'benchmark_agent':
      return <Gauge className={className} />;
    case 'research_coding_agent':
      return <FlaskConical className={className} />;
    case 'tool_executor':
      return <TerminalSquare className={className} />;
    default:
      return <Bot className={className} />;
  }
};

/** Status is shown as a small dot + word, never as a traffic-light fill. */
export const statusTone: Record<string, { color: string; soft: string; border: string; label: string }> = {
  pending: { color: '#657368', soft: '#f4f7f1', border: '#d6e0d4', label: '等待' },
  ready: { color: '#35443a', soft: '#f4f7f1', border: '#d6e0d4', label: '就绪' },
  in_progress: { color: '#1f6b4f', soft: '#e8f2e9', border: '#c7dfcf', label: '运行中' },
  completed: { color: '#2f7b58', soft: '#eef7ef', border: '#c7dfcf', label: '已完成' },
  failed: { color: '#b14a3a', soft: '#f8ebe7', border: '#e3c6bf', label: '失败' },
  blocked: { color: '#8a5a16', soft: '#f8efe1', border: '#e7d4b6', label: '已阻塞' },
  canceled: { color: '#657368', soft: '#f4f7f1', border: '#d6e0d4', label: '已取消' },
  skipped: { color: '#657368', soft: '#f4f7f1', border: '#d6e0d4', label: '已跳过' },
  awaiting_approval: { color: '#b14a3a', soft: '#f8ebe7', border: '#e3c6bf', label: '待批准' },
  awaiting_review: { color: '#8a5a16', soft: '#f8efe1', border: '#e7d4b6', label: '待审阅' },
  awaiting_input: { color: '#4b5f8a', soft: '#eef1f8', border: '#cdd5e6', label: '待回答' },
};

export const getTaskStyleByStatus = (status?: string) => {
  const tone = statusTone[status ?? 'pending'] ?? statusTone.pending;
  return {
    borderColor: '#e2e9df',
    backgroundColor: '#ffffff',
    borderLeftColor: '#e2e9df',
    borderTopColor: tone.border,
  };
};

export const getStatusTone = (status?: string) => statusTone[status ?? 'pending'] ?? statusTone.pending;
