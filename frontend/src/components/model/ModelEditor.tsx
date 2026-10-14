// Data model editor: a diagram of tables that live in a .chloemodel file. Tables are
// added and designed offline, then turned into SQL or synchronized to a database.
import { ReactFlowProvider, useReactFlow } from "@xyflow/react";
import { Database, Download, FileCode2, FolderOpen, LayoutGrid, Plus, Save, Search } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { D, DM, Models, arr, errorText } from "../../api";
import { Dialect, Table, newTable, normalize, toWire } from "../../designer/model";
import { ColumnMode, ErdTable, Positions, autoLayout, fromTables, modelKey, tableSize } from "../../erd/model";
import { Tab, useApp } from "../../store";
import { MenuItem, openMenu } from "../ContextMenu";
import { alert, confirm } from "../Dialogs";
import { CodeBlock } from "../ObjectsPane";
import { DiagramCanvas } from "../erd/DiagramCanvas";
import { exportDiagram } from "../erd/export";
import { openModel, pendingModels } from "./actions";
import { ModelTableDialog } from "./ModelTableDialog";
import { SyncDialog } from "./SyncDialog";

interface ModelState {
  model: DM.Model;
  tables: Table[];
}

/** Serialise the editor state to a model document. */
function toModel(s: ModelState, positions: Positions): DM.Model {
  const keys = new Set(s.tables.map((t) => modelKey(t, s.model.dialect, s.model.schema)));
  return {
    ...s.model,
    tables: s.tables.map((t) => toWire(t)),
    positions: Object.fromEntries(Object.entries(positions).filter(([k]) => keys.has(k))),
  };
}

export function ModelEditor({ tab }: { tab: Tab }) {
  return (
    <ReactFlowProvider>
      <ModelEditorInner tab={tab} />
    </ReactFlowProvider>
  );
}

function ModelEditorInner({ tab }: { tab: Tab }) {
  const [state, setState] = useState<ModelState | null>(null);
  const [path, setPath] = useState(tab.modelPath ?? "");
  const [saved, setSaved] = useState("");
  const [meta, setMeta] = useState<D.Meta | null>(null);
  const [selected, setSelected] = useState<string | null>(null);
  const [layoutVersion, setLayoutVersion] = useState(0);
  const [editing, setEditing] = useState<{ table: Table; isNew: boolean; at?: { x: number; y: number } } | null>(null);
  const [sql, setSql] = useState<string[] | null>(null);
  const [syncing, setSyncing] = useState(false);
  const [search, setSearch] = useState("");
  const [error, setError] = useState("");
  const positions = useRef<Positions>({});
  const flow = useReactFlow();
  const host = useRef<HTMLDivElement>(null);

  const dialect = (state?.model.dialect === "postgres" ? "postgres" : "mysql") as Dialect;
  const mode = (state?.model.columnMode || "all") as ColumnMode;
  const erd = useMemo(() => (state ? fromTables(state.tables, dialect, state.model.schema) : null), [state, dialect]);
  const keyOf = useCallback((t: { schema?: string; name: string }) => modelKey(t, dialect, state?.model.schema), [dialect, state?.model.schema]);

  // Load the model handed over by whoever opened the tab (or read its file).
  useEffect(() => {
    (async () => {
      try {
        let m = pendingModels.get(tab.id);
        pendingModels.delete(tab.id);
        if (!m && tab.modelPath) m = (await Models.Open(tab.modelPath)).model;
        if (!m) throw new Error("model not found");
        const tables = arr(m.tables).map(normalize);
        positions.current = { ...(m.positions ?? {}) } as Positions;
        const st = { model: m, tables };
        // Lay out reverse-engineered models (no positions yet).
        if (tables.length && tables.some((t) => !positions.current[modelKey(t, m!.dialect, m!.schema)])) {
          const placed = await autoLayout(fromTables(tables, m.dialect, m.schema), (m.columnMode || "all") as ColumnMode);
          positions.current = { ...placed, ...positions.current };
        }
        setState(st);
        setSaved(tab.modelPath ? JSON.stringify(toModel(st, positions.current)) : "");
        setLayoutVersion((v) => v + 1);
        setMeta(await Models.Meta(m.dialect));
        setTimeout(() => flow.fitView({ padding: 0.1, maxZoom: 1 }), 60);
      } catch (e) {
        setError(errorText(e));
      }
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab.id]);

  // Positions live in a ref (dragging shouldn't re-render); bump this to recheck dirtiness.
  const [moved, setMoved] = useState(0);
  const dirty = useMemo(() => !!state && JSON.stringify(toModel(state, positions.current)) !== saved, [state, saved, moved]);

  useEffect(() => {
    if (!state) return;
    useApp.getState().updateTab(tab.id, { title: `${state.model.name || "Untitled"}${dirty ? " •" : ""} (model)`, modelPath: path || undefined });
  }, [state?.model.name, dirty, path, tab.id, state]);

  const save = async (as = false) => {
    if (!state) return;
    const doc = toModel(state, positions.current);
    try {
      const p = await Models.Save(as ? "" : path, doc);
      if (!p) return;
      setPath(p);
      setSaved(JSON.stringify(doc));
    } catch (e) {
      alert("Save failed", errorText(e));
    }
  };

  const setTables = (fn: (ts: Table[]) => Table[]) => setState((s) => (s ? { ...s, tables: fn(s.tables) } : s));
  const setModel = (patch: Partial<DM.Model>) => setState((s) => (s ? { ...s, model: { ...s.model, ...patch } } : s));

  const viewCenter = () => {
    const r = host.current?.getBoundingClientRect();
    return r ? flow.screenToFlowPosition({ x: r.left + r.width / 2 - 100, y: r.top + r.height / 2 - 80 }) : { x: 0, y: 0 };
  };

  const addTable = (at?: { x: number; y: number }) => {
    if (!state) return;
    const t = newTable(dialect, "", dialect === "postgres" ? state.model.schema : undefined, meta ?? undefined);
    setEditing({ table: t, isNew: true, at: at ?? viewCenter() });
  };

  const findTable = (id: string) => state?.tables.find((t) => keyOf(t) === id);

  const commitTable = (t: Table) => {
    if (!editing || !state) return;
    const old = editing.isNew ? null : editing.table;
    const newKey = keyOf(t);
    if (old) {
      const oldKey = keyOf(old);
      if (oldKey !== newKey) {
        positions.current[newKey] = positions.current[oldKey];
        delete positions.current[oldKey];
      }
      setTables((ts) =>
        ts.map((x) => {
          if (x === old) return t;
          // Follow renames in other tables' foreign keys.
          if (old.name === t.name && (old.schema ?? "") === (t.schema ?? "")) return x;
          const fks = x.foreignKeys.map((fk) => (fk.refTable === old.name && (fk.refSchema || x.schema || "") === (old.schema || "") ? { ...fk, refTable: t.name, refSchema: fk.refSchema ? t.schema : fk.refSchema } : fk));
          return { ...x, foreignKeys: fks };
        }),
      );
    } else {
      positions.current[newKey] = editing.at ?? { x: 0, y: 0 };
      setTables((ts) => [...ts, t]);
      setSelected(newKey);
    }
    setLayoutVersion((v) => v + 1);
    setEditing(null);
  };

  const deleteTables = async (ids: string[]) => {
    const victims = ids.map(findTable).filter(Boolean) as Table[];
    if (!victims.length) return;
    const names = victims.map((t) => t.name).join(", ");
    if (!(await confirm(`Delete ${victims.length > 1 ? `${victims.length} tables` : `table ${names}`} from the model?`, { message: "Foreign keys that reference it are removed too.", okLabel: "Delete" }))) return;
    const gone = new Set(victims.map((t) => keyOf(t)));
    for (const k of gone) delete positions.current[k];
    setTables((ts) =>
      ts
        .filter((t) => !gone.has(keyOf(t)))
        .map((t) => ({ ...t, foreignKeys: t.foreignKeys.filter((fk) => !gone.has(keyOf({ schema: fk.refSchema || t.schema, name: fk.refTable }))) })),
    );
    setSelected(null);
  };

  const duplicate = (id: string) => {
    const t = findTable(id);
    if (!t || !state) return;
    let name = `${t.name}_copy`;
    for (let i = 2; state.tables.some((x) => x.name === name && x.schema === t.schema); i++) name = `${t.name}_copy${i}`;
    const copy: Table = { ...structuredClone(t), name, origName: "" };
    const p = positions.current[id] ?? { x: 0, y: 0 };
    positions.current[keyOf(copy)] = { x: p.x + 40, y: p.y + 40 };
    setTables((ts) => [...ts, copy]);
    setLayoutVersion((v) => v + 1);
  };

  const relayout = async () => {
    if (!erd) return;
    positions.current = await autoLayout(erd, mode);
    setLayoutVersion((v) => v + 1);
    setMoved((m) => m + 1);
    setTimeout(() => flow.fitView({ padding: 0.1, duration: 300, maxZoom: 1 }), 60);
  };

  const generateSQL = async () => {
    if (!state) return;
    try {
      setSql(arr(await Models.GenerateSQL(toModel(state, positions.current), { database: "" })));
    } catch (e) {
      alert("Can't generate SQL", errorText(e));
    }
  };

  const find = (q: string) => {
    const t = erd?.tables.find((x) => x.name.toLowerCase().includes(q.toLowerCase()));
    const at = t && positions.current[t.id];
    if (!t || !at) return;
    setSelected(t.id);
    const { width, height } = tableSize(t, mode);
    flow.setCenter(at.x + width / 2, at.y + height / 2, { zoom: 1, duration: 300 });
  };

  const tableMenu = (t: ErdTable): MenuItem[] => [
    { label: "Design Table", onClick: () => findTable(t.id) && setEditing({ table: findTable(t.id)!, isNew: false }) },
    { label: "Duplicate Table", onClick: () => duplicate(t.id) },
    { separator: true },
    { label: "Delete Table", onClick: () => deleteTables([t.id]) },
  ];

  if (error) return <div className="center-hint err">{error}</div>;
  if (!state || !erd) return <div className="center-hint">Loading…</div>;

  return (
    <div
      className="erd model-editor"
      ref={host}
      tabIndex={-1}
      onKeyDown={(e) => {
        const typing = (e.target as HTMLElement).closest("input, textarea, select, .modal");
        if ((e.metaKey || e.ctrlKey) && e.key === "s") {
          e.preventDefault();
          save(e.shiftKey);
        } else if (!typing && selected && (e.key === "Delete" || e.key === "Backspace")) {
          e.preventDefault();
          deleteTables([selected]);
        } else if (!typing && selected && e.key === "Enter") {
          const t = findTable(selected);
          if (t) setEditing({ table: t, isNew: false });
        }
      }}
    >
      <div className="toolbar">
        <button className={dirty ? "accent" : ""} onClick={() => save()} title="Save (⌘S)">
          <Save size={14} /> Save
        </button>
        <button onClick={() => save(true)} title="Save As (⇧⌘S)">
          Save As
        </button>
        <button onClick={() => openModel()} title="Open a model file">
          <FolderOpen size={14} />
        </button>
        <span className="tb-sep" />
        <button onClick={() => addTable()} title="Add a table">
          <Plus size={14} /> Table
        </button>
        <button onClick={relayout} title="Arrange tables automatically">
          <LayoutGrid size={14} /> Auto Layout
        </button>
        <select value={mode} onChange={(e) => (setModel({ columnMode: e.target.value }), setLayoutVersion((v) => v + 1))} title="Columns shown in tables">
          <option value="all">All Columns</option>
          <option value="keys">Keys Only</option>
          <option value="none">Names Only</option>
        </select>
        <span className="tb-sep" />
        <button onClick={generateSQL} title="Generate CREATE statements for the model">
          <FileCode2 size={14} /> Export SQL
        </button>
        <button onClick={() => setSyncing(true)} title="Synchronize the model to a database">
          <Database size={14} /> Synchronize
        </button>
        <button onClick={() => exportDiagram(erd, positions.current, mode, state.model.name || "model", "png")}>
          <Download size={14} /> PNG
        </button>
        <button onClick={() => exportDiagram(erd, positions.current, mode, state.model.name || "model", "svg")}>
          <Download size={14} /> SVG
        </button>
        <span className="spacer" />
        <span className="crumbs">
          <span className="badge">{dialect === "postgres" ? "PostgreSQL" : "MySQL"}</span>
          <input className="name-input" value={state.model.name} placeholder="model name" onChange={(e) => setModel({ name: e.target.value })} title="Model name" />
        </span>
        <div className="erd-search">
          <Search size={13} />
          <input placeholder="Find table" value={search} onChange={(e) => setSearch(e.target.value)} onKeyDown={(e) => e.key === "Enter" && find(search)} />
        </div>
      </div>
      <DiagramCanvas
        model={erd}
        mode={mode}
        positions={positions}
        onMoved={() => setMoved((m) => m + 1)}
        selected={selected}
        onSelect={setSelected}
        onOpen={(t) => findTable(t.id) && setEditing({ table: findTable(t.id)!, isNew: false })}
        onContextMenu={(e, t) => openMenu(e, tableMenu(t))}
        onPaneContextMenu={(e) => {
          const at = flow.screenToFlowPosition({ x: e.clientX, y: e.clientY });
          openMenu(e as React.MouseEvent, [
            { label: "New Table", onClick: () => addTable(at) },
            { label: "Auto Layout", onClick: relayout },
          ]);
        }}
        layoutVersion={layoutVersion}
      />
      {!state.tables.length && (
        <div className="model-empty">
          <p>This model has no tables yet.</p>
          <button className="primary" onClick={() => addTable()}>
            <Plus size={14} /> New Table
          </button>
        </div>
      )}
      <div className="statusbar">
        <span>
          {state.tables.length} tables · {erd.relations.length} relationships
        </span>
        <span className="spacer" />
        <span className="muted" title={path}>
          {path || "Not saved"}
        </span>
      </div>
      {editing && meta && (
        <ModelTableDialog
          model={toModel(state, positions.current)}
          tables={state.tables}
          table={editing.table}
          isNew={editing.isNew}
          dialect={dialect}
          meta={meta}
          onOK={commitTable}
          onCancel={() => setEditing(null)}
        />
      )}
      {sql && <SqlDialog sql={sql} name={state.model.name} onClose={() => setSql(null)} />}
      {syncing && <SyncDialog model={toModel(state, positions.current)} onClose={() => setSyncing(false)} />}
    </div>
  );
}

function SqlDialog({ sql, name, onClose }: { sql: string[]; name: string; onClose(): void }) {
  const text = sql.join(";\n\n") + (sql.length ? ";\n" : "");
  const saveFile = async () => {
    const { Transfer } = await import("../../api");
    const p = await Transfer.SaveFileDialog(`${name || "model"}.sql`, "SQL", "*.sql");
    if (!p) return;
    try {
      await Transfer.WriteFile(p, text, false);
      onClose();
    } catch (e) {
      alert("Save failed", errorText(e));
    }
  };
  return (
    <div className="modal-backdrop" onMouseDown={onClose}>
      <div className="modal wizard wide" onMouseDown={(e) => e.stopPropagation()} onKeyDown={(e) => e.key === "Escape" && onClose()}>
        <div className="modal-title">Export SQL</div>
        <div className="modal-body model-sql">
          <CodeBlock code={text} />
        </div>
        <div className="modal-footer">
          <button onClick={() => navigator.clipboard.writeText(text)}>Copy</button>
          <span className="spacer" />
          <button onClick={onClose}>Close</button>
          <button className="primary" onClick={saveFile}>
            Save…
          </button>
        </div>
      </div>
    </div>
  );
}
