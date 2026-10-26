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

/** Proof marks, not traffic-light colours: the glyph is the primary signal. */
const proofMarks = {
  pending: '○',
  ready: '◔',
  in_progress: '◐',
  completed: '✓',
  failed: '✗',
  blocked: '▲',
  canceled: '—',
  skipped: '—',
  awaiting_approval: '⚠',
} as const;

export const getAgentLabel = (agentName: string) => agentLabels[agentName] ?? agentName;

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
    default:
      return <Bot className={className} />;
  }
};

export const getProofMark = (status?: string) => proofMarks[(status ?? 'pending') as keyof typeof proofMarks] ?? '○';

export const statusTone: Record<string, { color: string; soft: string; border: string; label: string; glyph: string }> = {
  pending: { color: '#6b6156', soft: '#f4f1ea', border: '#d8d0c2', label: '等待', glyph: '○' },
  ready: { color: '#3b342c', soft: '#f4f1ea', border: '#bdb2a0', label: '就绪', glyph: '◔' },
  in_progress: { color: '#7a4a12', soft: '#f6efe2', border: '#d4b98d', label: '进行中', glyph: '◐' },
  completed: { color: '#14603f', soft: '#e9f0ea', border: '#b3c8b9', label: '已完成', glyph: '✓' },
  failed: { color: '#8c2c2c', soft: '#f3e7e4', border: '#d5b3ae', label: '失败', glyph: '✗' },
  blocked: { color: '#6b6156', soft: '#f4f1ea', border: '#c6bdb0', label: '已阻塞', glyph: '▲' },
  canceled: { color: '#6b6156', soft: '#f4f1ea', border: '#d8d0c2', label: '已取消', glyph: '—' },
  skipped: { color: '#6b6156', soft: '#f4f1ea', border: '#d8d0c2', label: '已跳过', glyph: '—' },
  awaiting_approval: { color: '#8c2c2c', soft: '#f3e7e4', border: '#d5b3ae', label: '待批', glyph: '⚠' },
};

export const getTaskStyleByStatus = (status?: string) => {
  const tone = statusTone[status ?? 'pending'] ?? statusTone.pending;
  return {
    borderColor: '#bdb2a0',
    backgroundColor: '#fbfaf7',
    borderLeftColor: tone.border,
    borderTopColor: tone.color,
  };
};

export const getStatusTone = (status?: string) => statusTone[status ?? 'pending'] ?? statusTone.pending;
