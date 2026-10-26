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

/**
 * Instrument palette. Status is carried by a glyph plus a colour, never by
 * colour alone: the caller always renders the label next to it.
 */
export const statusTone: Record<string, { color: string; soft: string; border: string; label: string; glyph: string }> = {
  pending: { color: '#5d6975', soft: '#f5f7f8', border: '#d6dde2', label: '等待', glyph: '○' },
  ready: { color: '#1b4d6b', soft: '#e3edf3', border: '#b8c2ca', label: '就绪', glyph: '◔' },
  in_progress: { color: '#8a5a0b', soft: '#f7efdd', border: '#d8c39a', label: '运行中', glyph: '◐' },
  completed: { color: '#2c6a4a', soft: '#e7f0ea', border: '#b6cdbf', label: '已完成', glyph: '●' },
  failed: { color: '#9e2f26', soft: '#f7e9e7', border: '#d9b3ae', label: '失败', glyph: '✕' },
  blocked: { color: '#5d6975', soft: '#f5f7f8', border: '#c4ccd2', label: '已阻塞', glyph: '▲' },
  canceled: { color: '#6b7782', soft: '#f5f7f8', border: '#d6dde2', label: '已取消', glyph: '—' },
  skipped: { color: '#6b7782', soft: '#f5f7f8', border: '#d6dde2', label: '已跳过', glyph: '—' },
  awaiting_approval: { color: '#8a5a0b', soft: '#f7efdd', border: '#d8c39a', label: '待审批', glyph: '⚠' },
};

export const getTaskStyleByStatus = (status?: string) => {
  const tone = statusTone[status ?? 'pending'] ?? statusTone.pending;
  return {
    borderColor: tone.border,
    backgroundColor: '#ffffff',
    borderLeftColor: tone.border,
    borderTopColor: tone.color,
  };
};

export const getStatusTone = (status?: string) => statusTone[status ?? 'pending'] ?? statusTone.pending;
