import type { Task } from '../../contracts/api';
import { getAgentLabel, getProofMark, getStatusTone } from '../shared/agentVisuals';
import { ExperimentSearchNodeLabel } from './ExperimentSearchNodeLabel';

interface CreateTaskNodeLabelOptions {
  assignedTo: string;
  taskName: string;
  status: string;
  step?: number;
  task?: Task;
}

const getPrimaryTaskName = (taskName: string) => taskName.split(/\s+\/\s+/)[0]?.trim() || taskName;

/**
 * A node is a figure entry: plate number, proof mark, title, and the running
 * head that names the responsible agent.
 */
export const createTaskNodeLabel = (options: CreateTaskNodeLabelOptions) => {
  const { assignedTo, taskName, status, step, task } = options;
  if (task?.Type === 'experiment_run') {
    return <ExperimentSearchNodeLabel task={task} status={status} step={step} />;
  }
  const tone = getStatusTone(status);

  return (
    <div className="flex min-h-[76px] w-full flex-col px-3 py-2.5 text-left">
      <div className="flex items-center justify-between gap-2">
        <span className="journal-num text-[11px] text-muted">
          {typeof step === 'number' ? `图 ${String(step).padStart(2, '0')}` : '图'}
        </span>
        <span className="flex items-center gap-1 journal-caps text-[10px]" style={{ color: tone.color }}>
          <span aria-hidden="true" className={status === 'in_progress' ? 'proof-pulse' : ''}>{getProofMark(status)}</span>
          {tone.label}
        </span>
      </div>
      <div className="mt-1 line-clamp-2 font-serif text-[13px] font-semibold leading-[1.4] text-ink" title={taskName}>
        {getPrimaryTaskName(taskName)}
      </div>
      <div className="mt-auto pt-1.5 truncate border-t border-line/70 journal-caps text-[10px] text-muted">
        {getAgentLabel(assignedTo)}
      </div>
    </div>
  );
};