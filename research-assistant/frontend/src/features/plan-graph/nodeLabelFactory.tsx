import { CheckCircle2, Circle, CircleAlert, Clock3, LoaderCircle, ShieldAlert, XCircle } from 'lucide-react';
import type { Task } from '../../contracts/api';
import { getAgentIcon, getAgentLabel } from '../shared/agentVisuals';
import { ExperimentSearchNodeLabel } from './ExperimentSearchNodeLabel';

interface CreateTaskNodeLabelOptions {
  assignedTo: string;
  taskName: string;
  status: string;
  step?: number;
  task?: Task;
}

const statusMeta: Record<string, { label: string; className: string; icon: typeof Circle }> = {
  pending: { label: '等待', className: 'text-faint', icon: Clock3 },
  ready: { label: '就绪', className: 'text-[#3e5c76]', icon: Circle },
  in_progress: { label: '运行中', className: 'text-warn', icon: LoaderCircle },
  completed: { label: '已完成', className: 'text-ok', icon: CheckCircle2 },
  failed: { label: '失败', className: 'text-flag', icon: CircleAlert },
  blocked: { label: '已阻塞', className: 'text-muted', icon: ShieldAlert },
  canceled: { label: '已取消', className: 'text-faint', icon: XCircle },
  skipped: { label: '已跳过', className: 'text-faint', icon: XCircle },
  awaiting_approval: { label: '待审批', className: 'text-plum', icon: ShieldAlert },
};

const getPrimaryTaskName = (taskName: string) => taskName.split(/\s+\/\s+/)[0]?.trim() || taskName;

export const createTaskNodeLabel = (options: CreateTaskNodeLabelOptions) => {
  const { assignedTo, taskName, status, step, task } = options;
  if (task?.Type === 'experiment_run') {
    return <ExperimentSearchNodeLabel task={task} status={status} step={step} />;
  }
  const statusState = statusMeta[status] ?? statusMeta.pending;
  const StatusIcon = statusState.icon;

  return (
    <div className="flex min-h-[86px] w-full flex-col px-3.5 py-3 text-left">
      <div className="flex items-center justify-between gap-2 text-[10.5px]">
        <div className="flex min-w-0 items-center gap-1.5 text-muted">
          {getAgentIcon(assignedTo)}
          <span className="truncate font-medium">{getAgentLabel(assignedTo)}</span>
        </div>
        <div className={`flex shrink-0 items-center gap-1 font-medium ${statusState.className}`}>
          <StatusIcon className={`h-3 w-3 ${status === 'in_progress' ? 'animate-spin' : ''}`} />
          <span>{statusState.label}</span>
        </div>
      </div>
      <div className="mt-2.5 flex min-w-0 gap-2">
        {typeof step === 'number' && (
          <span className="shrink-0 font-mono text-[10.5px] font-semibold leading-[1.5] text-faint">{String(step).padStart(2, '0')}</span>
        )}
        <div className="line-clamp-2 text-[13px] font-semibold leading-[1.45] text-ink" title={taskName}>
          {getPrimaryTaskName(taskName)}
        </div>
      </div>
    </div>
  );
};
