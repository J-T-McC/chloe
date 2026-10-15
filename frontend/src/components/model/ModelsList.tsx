// Chloe's model library: every model it manages, listed in the Objects tab when the
// Model toolbar button is pressed (like Navicat's model object list). Files kept
// elsewhere open with Open External and can be saved into the library.
import { Copy, FolderOpen, Plus, RefreshCw, Shapes, Trash2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { DM, Models, arr, copyText, errorText } from "../../api";
import { connById } from "../../store";
import { MenuItem, openMenu } from "../ContextMenu";
import { alert, confirm, prompt } from "../Dialogs";
import { Split } from "../Split";
import { libraryChanged, listenModelLibrary, newModel, openModel, useModelLibrary } from "./actions";

const DIALECT: Record<string, string> = { mysql: "MySQL", mariadb: "MariaDB", postgres: "PostgreSQL" };

/** Where a model came from or was last synchronized to, for display. */
export function linkLabel(link?: DM.Link | null) {
  if (!link) return "";
  const conn = connById(link.connId);
  return [conn ? conn.name : "(deleted connection)", link.database, link.schema].filter(Boolean).join(" › ");
}

/** "mcp:Claude Code" → "AI (Claude Code)". */
export function createdByLabel(by?: string) {
  return by?.startsWith("mcp:") ? `AI (${by.slice(4)})` : "";
}

export function ModelsList() {
  const version = useModelLibrary((s) => s.version);
  const [models, setModels] = useState<DM.Entry[]>([]);
  const [picked, setPicked] = useState("");
  const [filter, setFilter] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);

  useEffect(() => listenModelLibrary(), []);
  useEffect(() => {
    setLoading(true);
    Models.List()
      .then((ms) => (setModels(arr(ms)), setError("")))
      .catch((e) => setError(errorText(e)))
      .finally(() => setLoading(false));
  }, [version]);

  const shown = useMemo(() => {
    const f = filter.toLowerCase();
    return models.filter((m) => !f || m.name.toLowerCase().includes(f));
  }, [models, filter]);
  const sel = models.find((m) => m.path === picked) ?? null;

  const run = async (what: string, fn: () => Promise<unknown>) => {
    try {
      await fn();
    } catch (e) {
      alert(what, errorText(e));
    }
    libraryChanged();
  };
  const rename = async (m: DM.Entry) => {
    const name = await prompt("Rename Model", { message: "New name", defaultValue: m.name });
    if (name === null || name.trim() === m.name) return;
    run("Couldn't rename the model", async () => setPicked(await Models.Rename(m.path, name)));
  };
  const remove = async (m: DM.Entry) => {
    if (!(await confirm("Delete Model", { message: `Delete the model "${m.name}"? This can't be undone.`, okLabel: "Delete" }))) return;
    run("Couldn't delete the model", () => Models.Delete(m.path));
  };
  const exportFile = (m: DM.Entry) =>
    run("Couldn't save the file", async () => {
      const f = await Models.Open(m.path);
      await Models.SaveExternal(f.model);
    });
  const menu = (m: DM.Entry): MenuItem[] => [
    { label: "Open Model", onClick: () => openModel(m.path) },
    { label: "Rename…", onClick: () => rename(m) },
    { label: "Duplicate", onClick: () => run("Couldn't duplicate the model", async () => setPicked(await Models.Duplicate(m.path))) },
    { label: "Delete…", onClick: () => remove(m) },
    { separator: true },
    { label: "Save As External File…", onClick: () => exportFile(m) },
    { label: "Show in Finder", onClick: () => run("Couldn't show the file", () => Models.Reveal(m.path)) },
  ];

  return (
    <div className="objects-pane">
      <div className="toolbar">
        <button disabled={!sel} onClick={() => sel && openModel(sel.path)}>
          <Shapes size={14} /> Open Model
        </button>
        <button onClick={() => newModel("mysql")}>
          <Plus size={14} /> New MySQL Model
        </button>
        <button onClick={() => newModel("postgres")}>
          <Plus size={14} /> New PostgreSQL Model
        </button>
        <button disabled={!sel} onClick={() => sel && remove(sel)}>
          <Trash2 size={14} /> Delete Model
        </button>
        <button onClick={() => openModel()} title="Open a model file kept outside Chloe">
          <FolderOpen size={14} /> Open External…
        </button>
        <button onClick={libraryChanged} title="Refresh">
          <RefreshCw size={14} className={loading ? "spin" : ""} />
        </button>
        <span className="tb-sep" />
        <span className="crumbs">Models</span>
        <span className="spacer" />
        <input className="search" placeholder="Filter" value={filter} onChange={(e) => setFilter(e.target.value)} />
      </div>
      <Split
        id="models-info"
        className="objects-split"
        secondSize={320}
        minFirst={240}
        minSecond={180}
        maxSecond="70"
        first={
          <div
            className="object-list models-list"
            tabIndex={0}
            onMouseDown={(e) => e.target === e.currentTarget && setPicked("")}
            onContextMenu={(e) =>
              e.target === e.currentTarget &&
              openMenu(e, [
                { label: "New MySQL Model", onClick: () => newModel("mysql") },
                { label: "New PostgreSQL Model", onClick: () => newModel("postgres") },
                { label: "Open External…", onClick: () => openModel() },
                { separator: true },
                { label: "Refresh", onClick: libraryChanged },
              ])
            }
            onKeyDown={(e) => {
              if (e.key === "Enter" && sel) openModel(sel.path);
              if ((e.metaKey || e.ctrlKey) && e.key === "o" && sel) {
                e.preventDefault();
                openModel(sel.path);
              }
            }}
          >
            {error && <div className="error-banner">{error}</div>}
            <table>
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Type</th>
                  <th className="num">Tables</th>
                  <th>Database</th>
                  <th>Created By</th>
                  <th>Modified</th>
                </tr>
              </thead>
              <tbody>
                {shown.map((m) => (
                  <tr
                    key={m.path}
                    className={m.path === picked ? "selected" : ""}
                    onClick={() => setPicked(m.path)}
                    onDoubleClick={() => openModel(m.path)}
                    onContextMenu={(e) => (setPicked(m.path), openMenu(e, menu(m)))}
                  >
                    <td>
                      <Shapes size={13} /> {m.name}
                      {m.error && <span className="err"> (can't be read)</span>}
                    </td>
                    <td>{DIALECT[m.dialect ?? ""] ?? m.dialect}</td>
                    <td className="num">{m.tables}</td>
                    <td className="muted">{linkLabel(m.link)}</td>
                    <td className="muted">{createdByLabel(m.createdBy)}</td>
                    <td>{m.modified ? new Date(m.modified).toLocaleString() : ""}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            {!loading && !shown.length && !error && (
              <div className="center-hint">{models.length ? "No models match the filter." : "No models yet. Create one, or reverse engineer a database from its menu."}</div>
            )}
          </div>
        }
        second={sel ? <ModelInfo m={sel} /> : <div className="info-panel empty">Select a model to see its details.</div>}
      />
    </div>
  );
}

function ModelInfo({ m }: { m: DM.Entry }) {
  const [tables, setTables] = useState<string[]>([]);
  useEffect(() => {
    Models.Open(m.path)
      .then((f) => setTables(arr(f.model.tables).map((t) => (t.schema ? `${t.schema}.${t.name}` : t.name))))
      .catch(() => setTables([]));
  }, [m.path, m.modified]);
  return (
    <div className="info-panel">
      <div className="info-head">
        <Shapes size={16} /> {m.name}
      </div>
      <dl>
        <dt>Type</dt>
        <dd>{DIALECT[m.dialect ?? ""] ?? m.dialect}</dd>
        {m.link && (
          <>
            <dt>Database</dt>
            <dd>{linkLabel(m.link)}</dd>
          </>
        )}
        {m.createdBy && (
          <>
            <dt>Created by</dt>
            <dd>{createdByLabel(m.createdBy)}</dd>
          </>
        )}
        <dt>Modified</dt>
        <dd>{m.modified ? new Date(m.modified).toLocaleString() : ""}</dd>
        <dt>File</dt>
        <dd>
          {m.path}{" "}
          <button className="link-button" title="Copy path" onClick={() => copyText(m.path)}>
            <Copy size={11} />
          </button>
        </dd>
        {m.error && (
          <>
            <dt>Error</dt>
            <dd className="err">{m.error}</dd>
          </>
        )}
      </dl>
      {tables.length > 0 && (
        <>
          <div className="info-ddl-head">Tables ({tables.length})</div>
          <ul className="model-tables">
            {tables.map((t) => (
              <li key={t}>{t}</li>
            ))}
          </ul>
        </>
      )}
    </div>
  );
}
