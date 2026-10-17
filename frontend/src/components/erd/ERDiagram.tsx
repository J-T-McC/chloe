import { ReactFlowProvider, useReactFlow } from "@xyflow/react";
import { Download, LayoutGrid, RefreshCw, Search } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { Explorer, Layouts, ObjectKind, errorText } from "../../api";
import { ColumnMode, ErdModel, Positions, autoLayout, buildModel, tableSize } from "../../erd/model";
import { tableMenu } from "../../menus";
import { type TabOf } from "../../store";
import { designTable } from "../../tableActions";
import { openMenu } from "../ContextMenu";
import { DiagramCanvas } from "./DiagramCanvas";
import { exportDiagram } from "./export";
import { Toolbar } from "../Toolbar";
import { Check } from "../form";
import { isPG } from "../../dialect";

interface Saved {
  positions: Positions;
  mode: ColumnMode;
  views: boolean;
}

export function ERDiagram({ tab }: { tab: TabOf<"erd"> }) {
  return (
    <ReactFlowProvider>
      <ERDiagramInner tab={tab} />
    </ReactFlowProvider>
  );
}

function ERDiagramInner({ tab }: { tab: TabOf<"erd"> }) {
  const connId = tab.connId;
  const database = tab.database;
  const schema = tab.schema;
  const pg = isPG(connId);
  const layoutKey = `erd:${connId}:${database}:${schema ?? ""}`;
  const [model, setModel] = useState<ErdModel | null>(null);
  const [mode, setMode] = useState<ColumnMode>("all");
  const [views, setViews] = useState(false);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [selected, setSelected] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [layoutVersion, setLayoutVersion] = useState(0);
  const flow = useReactFlow();
  const positions = useRef<Positions>({});
  const first = useRef(true);
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
        setLayoutVersion((v) => v + 1);
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

  const exportAs = (kind: "svg" | "png") => model && exportDiagram(model, positions.current, mode, `${schema ?? database}-erd`, kind);

  const find = (q: string) => {
    const t = model?.tables.find((x) => x.name.toLowerCase().includes(q.toLowerCase()));
    const at = t && positions.current[t.id];
    if (!t || !at) return;
    setSelected(t.id);
    const { width, height } = tableSize(t, mode);
    flow.setCenter(at.x + width / 2, at.y + height / 2, { zoom: 1, duration: 300 });
  };

  return (
    <div className="erd">
      <Toolbar>
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
        <Check label="Views" checked={views} onChange={(v) => (setViews(v), setTimeout(save, 0))} />
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
      </Toolbar>
      {error && <div className="error-banner">{error}</div>}
      <DiagramCanvas
        model={model}
        mode={mode}
        positions={positions}
        onMoved={save}
        selected={selected}
        onSelect={setSelected}
        onOpen={(t) => t.kind === "table" && designTable(connId, { database, schema: t.schema, name: t.name, kind: ObjectKind.KindTable })}
        onContextMenu={(e, t) => t.kind === "table" && openMenu(e, tableMenu(connId, [{ database, schema: t.schema, name: t.name, kind: ObjectKind.KindTable }]))}
        layoutVersion={layoutVersion}
      />
    </div>
  );
}
