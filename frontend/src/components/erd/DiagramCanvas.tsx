// The diagram canvas shared by ER diagrams and data models: table nodes, relationship
// edges with crow's-foot markers, selection highlighting and drag-to-move.
import {
  Background,
  BaseEdge,
  Controls,
  EdgeProps,
  Handle,
  MiniMap,
  Node,
  NodeProps,
  Position,
  ReactFlow,
  getBezierPath,
  useNodesState,
  type Edge,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { Eye, KeyRound, Link2 } from "lucide-react";
import { MutableRefObject, createContext, memo, useContext, useEffect, useId, useMemo } from "react";
import { ColumnMode, ErdModel, ErdTable, HEADER_H, Positions, ROW_H, tableSize, visibleColumns } from "../../erd/model";

interface TableNodeData extends Record<string, unknown> {
  table: ErdTable;
  mode: ColumnMode;
}

// Selection highlighting is read from context so clicking a table doesn't rebuild
// the nodes (which would also swallow double-clicks).
const Highlight = createContext<{ selected: string | null; related: Set<string> }>({ selected: null, related: new Set() });

export type TNode = Node<TableNodeData, "table">;

const TableNode = memo(function TableNode({ data }: NodeProps<TNode>) {
  const t = data.table;
  const cols = visibleColumns(t, data.mode);
  const { width } = tableSize(t, data.mode);
  const hl = useContext(Highlight);
  const on = hl.selected !== null && hl.related.has(t.id);
  const dim = hl.selected !== null && !hl.related.has(t.id);
  return (
    <div className={"erd-table" + (t.kind === "view" ? " view" : "") + (on ? " hl" : "") + (dim ? " dim" : "")} style={{ width }} title={t.comment || undefined}>
      <div className="erd-head" style={{ height: HEADER_H }}>
        {t.kind === "view" && <Eye size={11} />}
        <span>{t.name}</span>
        {/* table-level handles for relationships whose column is hidden */}
        <Handle id="t-l" type="target" position={Position.Left} className="erd-handle" />
        <Handle id="t-r" type="source" position={Position.Right} className="erd-handle" />
        <Handle id="t-ls" type="source" position={Position.Left} className="erd-handle" />
        <Handle id="t-rt" type="target" position={Position.Right} className="erd-handle" />
      </div>
      {cols.map((c) => (
        <div key={c.name} className="erd-col" style={{ height: ROW_H }}>
          <span className={"erd-mark" + (c.pk ? " pk" : c.fk ? " fk" : "")}>{c.pk ? <KeyRound size={10} /> : c.fk ? <Link2 size={10} /> : null}</span>
          <span className={"erd-name" + (c.nullable ? "" : " nn")}>{c.name}</span>
          <span className="erd-type">{c.type}</span>
          <Handle id={`${c.name}-l`} type="target" position={Position.Left} className="erd-handle" />
          <Handle id={`${c.name}-r`} type="source" position={Position.Right} className="erd-handle" />
          <Handle id={`${c.name}-ls`} type="source" position={Position.Left} className="erd-handle" />
          <Handle id={`${c.name}-rt`} type="target" position={Position.Right} className="erd-handle" />
        </div>
      ))}
    </div>
  );
});

// Marker ids must be unique per diagram: url(#id) resolves to the first match in the
// document, which may sit in a hidden tab (display:none markers don't render).
const MarkerIds = createContext({ many: "erd-many", one: "erd-one" });

interface RelEdgeData extends Record<string, unknown> {
  optional: boolean;
  oneToOne: boolean;
  highlight: boolean;
}

function RelationEdge(p: EdgeProps<Edge<RelEdgeData>>) {
  const [path] = getBezierPath(p);
  const d = p.data!;
  const ids = useContext(MarkerIds);
  return (
    <BaseEdge
      id={p.id}
      path={path}
      markerStart={d.oneToOne ? `url(#${ids.one})` : `url(#${ids.many})`}
      markerEnd={`url(#${ids.one})`}
      className={"erd-edge" + (d.highlight ? " hl" : "")}
      style={{ strokeDasharray: d.optional ? "5 3" : undefined }}
    />
  );
}

const nodeTypes = { table: TableNode };
const edgeTypes = { relation: RelationEdge };

export interface DiagramCanvasProps {
  model: ErdModel | null;
  mode: ColumnMode;
  /** Table positions by id; updated in place when tables are dragged. */
  positions: MutableRefObject<Positions>;
  /** Called after a drag with the new positions already stored. */
  onMoved(): void;
  selected: string | null;
  onSelect(id: string | null): void;
  onOpen?(t: ErdTable): void;
  onContextMenu?(e: React.MouseEvent, t: ErdTable): void;
  onPaneContextMenu?(e: React.MouseEvent | MouseEvent): void;
  /** Bumped to rebuild nodes from positions (after auto layout). */
  layoutVersion?: number;
}

export function DiagramCanvas(p: DiagramCanvasProps) {
  const { model, mode, positions, selected } = p;
  const [nodes, setNodes, onNodesChange] = useNodesState<TNode>([]);
  const uid = useId().replace(/:/g, "");
  const markerIds = useMemo(() => ({ many: `erd-many-${uid}`, one: `erd-one-${uid}` }), [uid]);

  const related = useMemo(() => {
    const s = new Set<string>();
    if (!selected || !model) return s;
    s.add(selected);
    for (const r of model.relations) if (r.from === selected || r.to === selected) s.add(r.from), s.add(r.to);
    return s;
  }, [selected, model]);

  // Nodes follow the model; positions persist across rebuilds.
  useEffect(() => {
    if (!model) return;
    const next = model.tables.map((t) => ({
      id: t.id,
      type: "table" as const,
      position: positions.current[t.id] ?? { x: 0, y: 0 },
      data: { table: t, mode },
      selected: t.id === selected,
    }));
    setNodes(next);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [model, mode, setNodes, p.layoutVersion]);

  const edges = useMemo<Edge<RelEdgeData>[]>(() => {
    if (!model) return [];
    const byId = new Map(nodes.map((n) => [n.id, n]));
    return model.relations.map((r) => {
      const a = byId.get(r.from);
      const b = byId.get(r.to);
      const childLeft = !a || !b ? true : a.position.x + tableSize(a.data.table, mode).width / 2 <= b.position.x + tableSize(b.data.table, mode).width / 2;
      const fromCol = a && visibleColumns(a.data.table, mode).some((c) => c.name === r.fromColumns[0]) ? r.fromColumns[0] : "t";
      const toCol = b && visibleColumns(b.data.table, mode).some((c) => c.name === r.toColumns[0]) ? r.toColumns[0] : "t";
      return {
        id: r.id,
        source: r.from,
        target: r.to,
        sourceHandle: `${fromCol}-${childLeft ? "r" : "ls"}`,
        targetHandle: `${toCol}-${childLeft ? "l" : "rt"}`,
        type: "relation",
        data: { optional: r.optional, oneToOne: r.oneToOne, highlight: related.has(r.from) && related.has(r.to) && selected !== null },
      };
    });
  }, [model, nodes, mode, related, selected]);

  return (
    <div className="erd-canvas">
      <svg width="0" height="0" style={{ position: "absolute" }}>
        <defs>
          {/* At the start of a path (the child table), +x points back into the table, so
              the prongs fan out at the anchor and the apex sits along the line. */}
          <marker id={markerIds.many} viewBox="0 0 12 12" refX="11" refY="6" markerWidth="12" markerHeight="12" orient="auto-start-reverse">
            <path d="M1,6 L11,1 M1,6 L11,11 M1,6 L11,6" className="erd-marker" fill="none" />
          </marker>
          <marker id={markerIds.one} viewBox="0 0 12 12" refX="11" refY="6" markerWidth="12" markerHeight="12" orient="auto-start-reverse">
            <path d="M8,1 L8,11" className="erd-marker" fill="none" />
          </marker>
        </defs>
      </svg>
      <MarkerIds.Provider value={markerIds}>
        <Highlight.Provider value={{ selected, related }}>
          <ReactFlow
            nodes={nodes}
            edges={edges}
            nodeTypes={nodeTypes}
            edgeTypes={edgeTypes}
            onNodesChange={onNodesChange}
            onNodeDragStop={(_, n, dragged) => {
              for (const d of dragged.length ? dragged : [n]) positions.current[d.id] = d.position;
              p.onMoved();
            }}
            onNodeClick={(_, n) => p.onSelect(n.id)}
            onPaneClick={() => p.onSelect(null)}
            onNodeDoubleClick={(_, n) => p.onOpen?.(n.data.table)}
            onNodeContextMenu={(e, n) => {
              if (!p.onContextMenu) return;
              e.preventDefault();
              p.onSelect(n.id);
              p.onContextMenu(e, n.data.table);
            }}
            onPaneContextMenu={(e) => {
              if (!p.onPaneContextMenu) return;
              e.preventDefault();
              p.onPaneContextMenu(e);
            }}
            onlyRenderVisibleElements
            minZoom={0.05}
            maxZoom={2}
            fitView
            proOptions={{ hideAttribution: true }}
            nodesConnectable={false}
            zoomOnDoubleClick={false}
            deleteKeyCode={null}
          >
            <Background gap={20} size={1} />
            <MiniMap pannable zoomable nodeStrokeWidth={2} className="erd-minimap" />
            <Controls showInteractive={false} />
          </ReactFlow>
        </Highlight.Provider>
      </MarkerIds.Provider>
    </div>
  );
}
