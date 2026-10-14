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
  ReactFlowProvider,
  getBezierPath,
  useNodesState,
  useReactFlow,
  type Edge,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { Download, Eye, KeyRound, LayoutGrid, Link2, RefreshCw, Search } from "lucide-react";
import { createContext, memo, useCallback, useContext, useEffect, useId, useMemo, useRef, useState } from "react";
import { DBType, Explorer, Layouts, ObjectKind, Transfer, errorText } from "../../api";
import { ColumnMode, ErdModel, ErdTable, HEADER_H, Positions, ROW_H, autoLayout, buildModel, svgToPNG, tableSize, toSVG, visibleColumns } from "../../erd/model";
import { tableMenu } from "../../menus";
import { Tab, connById } from "../../store";
import { designTable } from "../../tableActions";
import { openMenu } from "../ContextMenu";
import { alert } from "../Dialogs";

interface TableNodeData extends Record<string, unknown> {
  table: ErdTable;
  mode: ColumnMode;
}

// Selection highlighting is read from context so clicking a table doesn't rebuild
// the nodes (which would also swallow double-clicks).
const Highlight = createContext<{ selected: string | null; related: Set<string> }>({ selected: null, related: new Set() });

type TNode = Node<TableNodeData, "table">;

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

interface Saved {
  positions: Positions;
  mode: ColumnMode;
  views: boolean;
}

export function ERDiagram({ tab }: { tab: Tab }) {
  return (
    <ReactFlowProvider>
      <ERDiagramInner tab={tab} />
    </ReactFlowProvider>
  );
}

function ERDiagramInner({ tab }: { tab: Tab }) {
  const connId = tab.connId!;
  const database = tab.database!;
  const schema = tab.schema;
  const pg = connById(connId)?.type === DBType.PostgreSQL;
  const layoutKey = `erd:${connId}:${database}:${schema ?? ""}`;
  const [model, setModel] = useState<ErdModel | null>(null);
  const [mode, setMode] = useState<ColumnMode>("all");
  const [views, setViews] = useState(false);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [selected, setSelected] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [nodes, setNodes, onNodesChange] = useNodesState<TNode>([]);
  const flow = useReactFlow();
  const positions = useRef<Positions>({});
  const first = useRef(true);
  const uid = useId().replace(/:/g, "");
  const markerIds = useMemo(() => ({ many: `erd-many-${uid}`, one: `erd-one-${uid}` }), [uid]);
  const catalog = useRef<Awaited<ReturnType<typeof Explorer.Catalog>> | null>(null);

  const save = useCallback(() => {
    const doc: Saved = { positions: positions.current, mode, views };
    Layouts.Set(layoutKey, JSON.stringify(doc)).catch(() => {});
  }, [layoutKey, mode, views]);

  const load = useCallback(
    async (relayout: boolean) => {
      setLoading(true);
      setError("");
      try {
        catalog.current = await Explorer.Catalog(connId, database);
        const saved: Saved | null = relayout ? null : JSON.parse((await Layouts.Get(layoutKey)) || "null");
        const m = saved?.mode ?? mode;
        const v = saved?.views ?? views;
        const model = buildModel(catalog.current as never, pg ? (schema ?? "public") : undefined, v, pg ? undefined : database);
        let pos = saved?.positions ?? {};
        // Lay out from scratch, or just place tables that are new since the last save.
        if (relayout || model.tables.some((t) => !pos[t.id])) {
          const auto = await autoLayout(model, m);
          if (relayout || !Object.keys(pos).length) pos = auto;
          else {
            const maxY = Math.max(0, ...Object.values(pos).map((p) => p.y)) + 300;
            let x = 0;
            for (const t of model.tables) if (!pos[t.id]) (pos[t.id] = { x, y: maxY }), (x += tableSize(t, m).width + 40);
          }
        }
        positions.current = pos;
        setMode(m);
        setViews(v);
        setModel(model);
        // Fit once the nodes exist (the fitView prop only covers the first, empty render).
        if (relayout || first.current) setTimeout(() => flow.fitView({ padding: 0.1, duration: relayout ? 300 : 0, maxZoom: 1 }), 60);
        first.current = false;
      } catch (e) {
        setError(errorText(e));
      } finally {
        setLoading(false);
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [connId, database, schema, layoutKey, pg],
  );

  useEffect(() => {
    load(false);
  }, [load]);

  // Rebuild when "show views" changes (the catalog is already loaded).
  useEffect(() => {
    if (!catalog.current) return;
    const m = buildModel(catalog.current as never, pg ? (schema ?? "public") : undefined, views, pg ? undefined : database);
    setModel(m);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [views]);

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
    setNodes(
      model.tables.map((t) => ({
        id: t.id,
        type: "table",
        position: positions.current[t.id] ?? { x: 0, y: 0 },
        data: { table: t, mode },
      })),
    );
  }, [model, mode, setNodes]);

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

  const exportAs = async (kind: "svg" | "png") => {
    if (!model) return;
    const css = getComputedStyle(document.documentElement);
    const v = (n: string) => css.getPropertyValue(n).trim();
    const svg = toSVG(model, positions.current, mode, {
      background: v("--bg"), header: v("--bg-3"), headerText: v("--text"), body: v("--bg"), text: v("--text"), muted: v("--text-3"), border: v("--border-strong"), line: v("--text-2"), key: "#c49a2c",
    });
    const name = `${schema ?? database}-erd.${kind}`;
    const path = await Transfer.SaveFileDialog(name, kind.toUpperCase(), `*.${kind}`);
    if (!path) return;
    try {
      if (kind === "svg") await Transfer.WriteFile(path, svg, false);
      else await Transfer.WriteFile(path, (await svgToPNG(svg)).split(",")[1], true);
    } catch (e) {
      alert("Export failed", errorText(e));
    }
  };

  const find = (q: string) => {
    const n = nodes.find((x) => x.data.table.name.toLowerCase().includes(q.toLowerCase()));
    if (!n) return;
    setSelected(n.id);
    const { width, height } = tableSize(n.data.table, mode);
    flow.setCenter(n.position.x + width / 2, n.position.y + height / 2, { zoom: 1, duration: 300 });
  };

  return (
    <div className="erd">
      <div className="toolbar">
        <button onClick={() => load(true)} title="Arrange tables automatically">
          <LayoutGrid size={14} /> Auto Layout
        </button>
        <button onClick={() => load(false)} title="Reload from the database">
          <RefreshCw size={14} className={loading ? "spin" : ""} />
        </button>
        <span className="tb-sep" />
        <div className="seg">
          {(["all", "keys", "none"] as ColumnMode[]).map((m) => (
            <button key={m} className={mode === m ? "active" : ""} onClick={() => (setMode(m), setTimeout(save, 0))}>
              {m === "all" ? "All Columns" : m === "keys" ? "Keys Only" : "Names Only"}
            </button>
          ))}
        </div>
        <label className="check">
          <input type="checkbox" checked={views} onChange={(e) => (setViews(e.target.checked), setTimeout(save, 0))} /> Views
        </label>
        <span className="tb-sep" />
        <button onClick={() => exportAs("png")}>
          <Download size={14} /> PNG
        </button>
        <button onClick={() => exportAs("svg")}>
          <Download size={14} /> SVG
        </button>
        <span className="spacer" />
        <span className="muted">
          {model ? `${model.tables.length} tables · ${model.relations.length} relationships` : ""}
        </span>
        <div className="erd-search">
          <Search size={13} />
          <input placeholder="Find table" value={search} onChange={(e) => setSearch(e.target.value)} onKeyDown={(e) => e.key === "Enter" && find(search)} />
        </div>
      </div>
      {error && <div className="error-banner">{error}</div>}
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
            save();
          }}
          onNodeClick={(_, n) => setSelected(n.id)}
          onPaneClick={() => setSelected(null)}
          onNodeDoubleClick={(_, n) => {
            const t = n.data.table;
            if (t.kind === "table") designTable(connId, { database, schema: t.schema, name: t.name, kind: ObjectKind.KindTable });
          }}
          onNodeContextMenu={(e, n) => {
            const t = n.data.table;
            if (t.kind === "table") openMenu(e, tableMenu(connId, [{ database, schema: t.schema, name: t.name, kind: ObjectKind.KindTable }]));
          }}
          onlyRenderVisibleElements
          minZoom={0.05}
          maxZoom={2}
          fitView
          proOptions={{ hideAttribution: true }}
          nodesConnectable={false}
          zoomOnDoubleClick={false}
        >
          <Background gap={20} size={1} />
          <MiniMap pannable zoomable nodeStrokeWidth={2} className="erd-minimap" />
          <Controls showInteractive={false} />
        </ReactFlow>
        </Highlight.Provider>
        </MarkerIds.Provider>
      </div>
    </div>
  );
}
