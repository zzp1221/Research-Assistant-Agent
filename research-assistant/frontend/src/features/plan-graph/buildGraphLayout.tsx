import type { Edge, MarkerType, Node, Position } from '@xyflow/react';

const POSITION_RIGHT = 'right' as Position;
const POSITION_LEFT = 'left' as Position;
const POSITION_TOP = 'top' as Position;
const POSITION_BOTTOM = 'bottom' as Position;
const ARROW_CLOSED = 'arrowclosed' as MarkerType;
import type { GraphTask, PlanGraph, Task } from '../../contracts/api';
import { getTaskStyleByStatus } from '../shared/agentVisuals';
import { createTaskNodeLabel } from './nodeLabelFactory';

const NODE_WIDTH = 214;
const NODE_HEIGHT = 92;
const SEARCH_NODE_WIDTH = 680;
const SEARCH_NODE_HEIGHT = 256;

// The plate is read at print size: three columns keep nodes near 1:1 instead
// of shrinking a two-column snake to 70% and making labels illegible.
const getCompactColumnCount = () => 3;

const pairKey = (from: string, to: string) => `${from}->${to}`;

const selectVisibleEdges = (planGraph: PlanGraph) => {
  const controlEdges = planGraph.edges.filter((edge) => edge.type === 'control');
  const controlPairs = new Set(controlEdges.map((edge) => pairKey(edge.from, edge.to)));
  const adjacency = controlEdges.reduce<Record<string, string[]>>((result, edge) => {
    result[edge.from] = [...(result[edge.from] ?? []), edge.to];
    return result;
  }, {});

  const hasControlPath = (from: string, to: string) => {
    const queue = [...(adjacency[from] ?? [])];
    const visited = new Set<string>();
    while (queue.length > 0) {
      const current = queue.shift();
      if (!current || visited.has(current)) continue;
      if (current === to) return true;
      visited.add(current);
      queue.push(...(adjacency[current] ?? []));
    }
    return false;
  };

  const dataEdges = planGraph.edges.filter(
    (edge) =>
      edge.type === 'data' &&
      !controlPairs.has(pairKey(edge.from, edge.to)) &&
      !hasControlPath(edge.from, edge.to),
  );

  return [...controlEdges, ...dataEdges].filter(
    (edge, index, edges) =>
      edges.findIndex((candidate) => pairKey(candidate.from, candidate.to) === pairKey(edge.from, edge.to)) === index,
  );
};

export const graphTaskToTask = (task: GraphTask): Task => ({
  ID: task.id,
  Name: task.name,
  Type: task.type,
  Description: task.description,
  AssignedTo: task.assigned_to,
  Status: task.status,
  Dependencies: task.dependencies ?? [],
  Inputs: task.inputs,
  RequiredArtifacts: task.required_artifacts,
  OutputArtifacts: task.output_artifacts,
  Result: task.result,
  Code: task.code,
  StructuredData: task.structured_data,
  ImageBase64: task.image_base64 || task.image_base_64,
  Error: task.error,
  Usage: task.usage,
  Verification: task.verification,
});

// Go marshals a nil slice as JSON null, so a plan created before a dependency
// exists can arrive with `dependencies: null`. Normalise every collection here
// instead of crashing the whole workspace on `.map`/`.length`.
export const normalizePlanGraph = (planGraph: PlanGraph): PlanGraph => ({
  ...planGraph,
  nodes: (planGraph.nodes ?? []).map((task) => ({
    ...task,
    dependencies: task.dependencies ?? [],
    required_artifacts: task.required_artifacts ?? [],
    output_artifacts: task.output_artifacts ?? [],
    inputs: task.inputs ?? {},
    contract: {
      ...task.contract,
      input_artifacts: task.contract?.input_artifacts ?? [],
      output_artifacts: task.contract?.output_artifacts ?? [],
      allowed_tools: task.contract?.allowed_tools ?? [],
    },
  })),
  edges: planGraph.edges ?? [],
  artifacts: planGraph.artifacts ?? {},
});

export const buildGraphLayout = (inputPlanGraph: PlanGraph): { nodes: Node[]; edges: Edge[] } => {
  const planGraph = normalizePlanGraph(inputPlanGraph);
  const newNodes: Node[] = [];
  const newEdges: Edge[] = [];

  const levelMap: Record<string, number> = {};
  const laneOrder = ['librarian_agent', 'benchmark_agent', 'coder_agent', 'research_coding_agent', 'sandbox_agent', 'data_agent', 'general_agent'];
  const tasksById = Object.fromEntries(planGraph.nodes.map((task) => [task.id, task]));

  const resolveLevel = (task: GraphTask): number => {
    if (typeof levelMap[task.id] === 'number') return levelMap[task.id];
    if (!task.dependencies.length) {
      levelMap[task.id] = 0;
      return 0;
    }

    const level = Math.max(
      ...task.dependencies.map((depId) => {
        const dep = tasksById[depId];
        return dep ? resolveLevel(dep) + 1 : 1;
      }),
    );
    levelMap[task.id] = level;
    return level;
  };

  const sortedTasks = [...planGraph.nodes].sort((a, b) => {
    const levelDiff = resolveLevel(a) - resolveLevel(b);
    if (levelDiff !== 0) return levelDiff;
    return laneOrder.indexOf(a.assigned_to) - laneOrder.indexOf(b.assigned_to);
  });

  const maxLevel = sortedTasks.reduce((max, task) => Math.max(max, resolveLevel(task)), 0);
  const tasksPerLevel = sortedTasks.reduce<Record<number, number>>((counts, task) => {
    const level = resolveLevel(task);
    counts[level] = (counts[level] || 0) + 1;
    return counts;
  }, {});
  const maxTasksInLevel = Math.max(...Object.values(tasksPerLevel), 1);
  const hasExperimentSearch = sortedTasks.some((task) => task.type === 'experiment_run');
  const useCompactLongChain = maxLevel >= 5 && maxTasksInLevel <= 2;
  const compactColumns = getCompactColumnCount();
  const compactStackGap = 96;
  const compactRowHeight = maxTasksInLevel > 1 ? 150 : 130;

  const levelCounts: Record<number, number> = {};
  sortedTasks.forEach((task, taskIndex) => {
    const level = resolveLevel(task);
    const stackIndex = levelCounts[level] || 0;
    levelCounts[level] = stackIndex + 1;
    const legacyTask = graphTaskToTask(task);
    const styleState = getTaskStyleByStatus(task.status);

    let position = {
      x: 48 + level * 256,
      y: 48 + (stackIndex + (maxTasksInLevel - tasksPerLevel[level]) / 2) * 124,
    };
    let sourcePosition = POSITION_RIGHT;
    let targetPosition = POSITION_LEFT;

    if (hasExperimentSearch) {
      const scientificPositions: Record<string, { x: number; y: number }> = {
        experiment_dataset_prepare: { x: 32, y: 90 },
        experiment_spec: { x: 272, y: 90 },
        ablation_design: { x: 512, y: 28 },
        prepare_runtime: { x: 512, y: 154 },
        install_dependencies: { x: 752, y: 154 },
        experiment_run: { x: 272, y: 292 },
        experiment_validate: { x: 332, y: 602 },
        verify_result: { x: 592, y: 602 },
      };
      position = scientificPositions[task.type] ?? position;
      if (task.type === 'ablation_design' || task.type === 'install_dependencies') sourcePosition = POSITION_BOTTOM;
      if (task.type === 'experiment_run') {
        sourcePosition = POSITION_BOTTOM;
        targetPosition = POSITION_TOP;
      }
      if (task.type === 'experiment_validate') targetPosition = POSITION_TOP;
    }

    if (useCompactLongChain && !hasExperimentSearch) {
      const row = Math.floor(level / compactColumns);
      const naturalColumn = level % compactColumns;
      const column = row % 2 === 0 ? naturalColumn : compactColumns - naturalColumn - 1;
      const startsNewRow = naturalColumn === 0 && row > 0;
      const endsRow = naturalColumn === compactColumns - 1 && level < maxLevel;
      const levelStackOffset = ((maxTasksInLevel - tasksPerLevel[level]) * compactStackGap) / 2;

      position = {
        x: 40 + column * 248,
        y: 28 + row * compactRowHeight + levelStackOffset + stackIndex * compactStackGap,
      };
      targetPosition = startsNewRow ? POSITION_TOP : row % 2 === 0 ? POSITION_LEFT : POSITION_RIGHT;
      sourcePosition = endsRow ? POSITION_BOTTOM : row % 2 === 0 ? POSITION_RIGHT : POSITION_LEFT;
    }

    newNodes.push({
      id: task.id,
      position,
      sourcePosition,
      targetPosition,
      className: 'research-assistant-task-node',
      data: {
        task: legacyTask,
        status: task.status,
        step: taskIndex + 1,
        label: createTaskNodeLabel({
          assignedTo: task.assigned_to,
          taskName: task.name,
          status: task.status,
          step: taskIndex + 1,
          task: legacyTask,
        }),
      },
      style: {
        borderRadius: '16px',
        backgroundColor: '#ffffff',
        border: '1px solid #e1e9df',
        borderTopWidth: '1px',
        borderTopColor: styleState.borderTopColor,
        boxShadow: '0 18px 34px -30px rgba(24, 55, 36, 0.5)',
        cursor: 'pointer',
        overflow: 'hidden',
        padding: 0,
        width: task.type === 'experiment_run' ? SEARCH_NODE_WIDTH : NODE_WIDTH,
        minHeight: task.type === 'experiment_run' ? SEARCH_NODE_HEIGHT : NODE_HEIGHT,
      },
    });
  });

  selectVisibleEdges(planGraph).forEach((edge) => {
    const targetStatus = tasksById[edge.to]?.status;
    const isDataEdge = edge.type === 'data';
    newEdges.push({
      id: edge.id,
      source: edge.from,
      target: edge.to,
      type: 'smoothstep',
      animated: targetStatus === 'in_progress',
      style: {
        stroke: isDataEdge ? '#9aab9d' : '#a9bfa9',
        strokeWidth: isDataEdge ? 1.2 : 1.5,
        strokeDasharray: isDataEdge ? '3 4' : undefined,
      },
      markerEnd: {
        type: ARROW_CLOSED,
        color: isDataEdge ? '#9aab9d' : '#a9bfa9',
        width: 14,
        height: 14,
      },
    });
  });

  return { nodes: newNodes, edges: newEdges };
};