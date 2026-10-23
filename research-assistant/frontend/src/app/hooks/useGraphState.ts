import { useCallback, useState } from 'react';
import type { Edge, EdgeChange, Node, NodeChange, OnEdgesChange, OnNodesChange } from '@xyflow/react';

/**
 * Minimal local graph state. React Flow's renderer lives in the lazy GraphCanvas
 * chunk; keeping its state helpers out of the entry chunk keeps React Flow out
 * of the initial bundle as well.
 */
function applyNodeChanges(changes: NodeChange<Node>[], nodes: Node[]): Node[] {
  const updated: Node[] = [];
  for (const node of nodes) {
    const nodeChanges = changes.filter((change) => 'id' in change && change.id === node.id);
    if (nodeChanges.some((change) => change.type === 'remove')) continue;
    const next = { ...node };
    for (const change of nodeChanges) {
      switch (change.type) {
        case 'select':
          next.selected = change.selected;
          break;
        case 'position':
          if (change.position !== undefined) next.position = change.position;
          if (change.dragging !== undefined) next.dragging = change.dragging;
          break;
        case 'dimensions':
          if (change.dimensions !== undefined) {
            next.measured = { ...change.dimensions };
            if (change.setAttributes === true || change.setAttributes === 'width') {
              next.width = change.dimensions.width;
            }
            if (change.setAttributes === true || change.setAttributes === 'height') {
              next.height = change.dimensions.height;
            }
          }
          if (change.resizing !== undefined) next.resizing = change.resizing;
          break;
      }
    }
    updated.push(next);
  }
  for (const change of changes) {
    if (change.type === 'add') updated.push({ ...change.item });
    if (change.type === 'replace') {
      const index = updated.findIndex((node) => node.id === change.id);
      if (index >= 0) updated[index] = { ...change.item };
    }
  }
  return updated;
}

function applyEdgeChanges(changes: EdgeChange<Edge>[], edges: Edge[]): Edge[] {
  const updated: Edge[] = [];
  for (const edge of edges) {
    const edgeChanges = changes.filter((change) => 'id' in change && change.id === edge.id);
    if (edgeChanges.some((change) => change.type === 'remove')) continue;
    const next = { ...edge };
    for (const change of edgeChanges) {
      if (change.type === 'select') next.selected = change.selected;
    }
    updated.push(next);
  }
  for (const change of changes) {
    if (change.type === 'add') updated.push({ ...change.item });
    if (change.type === 'replace') {
      const index = updated.findIndex((edge) => edge.id === change.id);
      if (index >= 0) updated[index] = { ...change.item };
    }
  }
  return updated;
}

export function useGraphState() {
  const [nodes, setNodes] = useState<Node[]>([]);
  const [edges, setEdges] = useState<Edge[]>([]);

  const onNodesChange = useCallback<OnNodesChange<Node>>(
    (changes) => setNodes((current) => applyNodeChanges(changes, current)),
    [],
  );
  const onEdgesChange = useCallback<OnEdgesChange<Edge>>(
    (changes) => setEdges((current) => applyEdgeChanges(changes, current)),
    [],
  );

  return { nodes, setNodes, onNodesChange, edges, setEdges, onEdgesChange };
}
