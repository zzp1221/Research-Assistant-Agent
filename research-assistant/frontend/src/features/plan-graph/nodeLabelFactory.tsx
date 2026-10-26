import { CheckCircle2, Circle, Clock3, LoaderCircle, ShieldAlert, XCircle } from 'lucide-react';
import type { Task } from '../../contracts/api';
import { getAgentIcon, getAgentLabel, getStatusTone } from '../shared/agentVisuals';
import { ExperimentSearchNodeLabel } from './ExperimentSearchNodeLabel';

interface CreateTaskNodeLabelOptions {
  assignedTo: string;
  taskName: string;
  status: string;
  step?: number;
  task?: Task;
}

const statusIcon = (status: string, spinning: boolean) => {
  const className = spinning ? 'h-2.5 w-2.5 animate-spin' : 'h-2.5 w-2.5';
  switch (status) {
    case 'ready':
      return <Circle className={className} />;
    case 'in_progress':
      return <LoaderCircle className={className} />;
    case 'completed':
      return <CheckCircle2 className={className} />;
    case 'failed':
      return <XCircle className={className} />;
    case 'blocked':
      return <ShieldAlert className={className} />;
    case 'awaiting_approval':
      return <ShieldAlert className={className} />;
    case 'canceled':
    case 'skipped':
      return <XCircle className={className} />;
    default:
      return <Clock3 className={className} />;
  }
};

const getPrimaryTaskName = (taskName: string) => taskName.split(/\s+\/\s+/)[0]?.trim() || taskName;

export const createTaskNodeLabel = (options: CreateTaskNodeLabelOptions) => {
  const { assignedTo, taskName, status, step, task } = options;
  if (task?.Type === 'experiment_run') {
    return <ExperimentSearchNodeLabel task={task} status={status} step={step} />;
  }
  const tone = getStatusTone(status);

  return (
    <div className="flex min-h-[76px] w-full flex-col px-2.5 py-2 text-left">
      <div className="flex items-center justify-between gap-2 font-mono text-[11px]">
        <div className="flex min-w-0 items-center gap-1 text-faint">
          {typeof step === 'number' && <span className="font-semibold text-faint">{String(step).padStart(2, '0')}</span>}
          {getAgentIcon(assignedTo, 'h-3 w-3 text-faint')}
          <span className="truncate">{getAgentLabel(assignedTo)}</span>
        </div>
        <div className="flex shrink-0 items-center gap-1 font-semibold" style={{ color: tone.color }}>
          {statusIcon(status, status === 'in_progress')}
          <span>{tone.label}</span>
        </div>
      </div>
      <div className="mt-1.5 line-clamp-2 text-[12.5px] font-semibold leading-[1.4] text-ink" title={taskName}>
        {getPrimaryTaskName(taskName)}
      </div>
    </div>
  );
};
