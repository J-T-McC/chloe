// A library of files Chloe manages (BI workspaces, models), listed in the Objects tab:
// toolbar, filter, the table, its menus and the info panel beside it.
import { Copy, FolderOpen, Plus, RefreshCw, Trash2, type LucideIcon } from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";
import { errorText, copyText } from "../api";
import { createdByLabel } from "../createdBy";
import { MenuItem, openMenu } from "./ContextMenu";
import { alert, confirm, prompt } from "./Dialogs";
import { Split } from "./Split";
import { Toolbar } from "./Toolbar";

export interface LibraryEntry {
  path: string;
  name: string;
  createdBy?: string;
  modified?: string;
  error?: string;
}

export interface LibraryColumn<E> {
  title: string;
  num?: boolean;
  muted?: boolean;
  cell(e: E): ReactNode;
}

const when = (m?: string) => (m ? new Date(m).toLocaleString() : "");

export function LibraryList<E extends LibraryEntry>(p: {
  /** "Workspace", "Model" */
  noun: string;
  /** the crumb, e.g. "BI Workspaces" */
  title: string;
  icon: LucideIcon;
  splitId: string;
  listClass?: string;
  items: E[];
  error: string;
  loading: boolean;
  /** reloads the list (after every change too) */
  refresh(): void;
  open(path: string): void;
  /** New buttons for the toolbar, and their menu items for the empty area */
  create: { label: string; onClick(): void }[];
  openExternal(): Promise<unknown> | void;
  openExternalTitle: string;
  columns: LibraryColumn<E>[];
  /** each returns the entry's new path */
  rename(e: E, name: string): Promise<string>;
  duplicate(e: E): Promise<string>;
  remove(e: E): Promise<unknown>;
  /** the confirmation message, e.g. what else is deleted with it */
  removeMessage(e: E): string;
  saveExternal(e: E): Promise<unknown>;
  reveal(e: E): Promise<unknown>;
  /** dt/dd rows of the info panel before Created by */
  details?(e: E): ReactNode;
  /** below the info panel's list */
  extra?(e: E): ReactNode;
  empty: string;
}) {
  const [picked, setPicked] = useState("");
  const [filter, setFilter] = useState("");
  const noun = p.noun.toLowerCase();
  const Icon = p.icon;

  const shown = useMemo(() => {
    const f = filter.toLowerCase();
    return p.items.filter((m) => !f || m.name.toLowerCase().includes(f));
  }, [p.items, filter]);
  const sel = p.items.find((m) => m.path === picked) ?? null;

  const run = async (what: string, fn: () => Promise<unknown>) => {
    try {
      await fn();
    } catch (e) {
      alert(what, errorText(e));
    }
    p.refresh();
  };
  const rename = async (m: E) => {
    const name = await prompt(`Rename ${p.noun}`, { message: "New name", defaultValue: m.name });
    if (name === null || name.trim() === m.name) return;
    run(`Couldn't rename the ${noun}`, async () => setPicked(await p.rename(m, name)));
  };
  const remove = async (m: E) => {
    if (!(await confirm(`Delete ${p.noun}`, { message: p.removeMessage(m), okLabel: "Delete" }))) return;
    run(`Couldn't delete the ${noun}`, () => p.remove(m));
  };
  const openExternal = () => run(`Couldn't open the ${noun}`, async () => p.openExternal());
  const menu = (m: E): MenuItem[] => [
    { label: `Open ${p.noun}`, onClick: () => p.open(m.path) },
    { label: "Rename…", onClick: () => rename(m) },
    { label: "Duplicate", onClick: () => run(`Couldn't duplicate the ${noun}`, async () => setPicked(await p.duplicate(m))) },
    { label: "Delete…", onClick: () => remove(m) },
    { separator: true },
    { label: "Save As External File…", onClick: () => run("Couldn't save the file", () => p.saveExternal(m)) },
    { label: "Show in Finder", onClick: () => run("Couldn't show the file", () => p.reveal(m)) },
  ];

  return (
    <div className="objects-pane">
      <Toolbar>
        <button disabled={!sel} onClick={() => sel && p.open(sel.path)}>
          <Icon size={14} /> Open {p.noun}
        </button>
        {p.create.map((c) => (
          <button key={c.label} onClick={c.onClick}>
            <Plus size={14} /> {c.label}
          </button>
        ))}
        <button disabled={!sel} onClick={() => sel && remove(sel)}>
          <Trash2 size={14} /> Delete {p.noun}
        </button>
        <button onClick={openExternal} title={p.openExternalTitle}>
          <FolderOpen size={14} /> Open External…
        </button>
        <button onClick={p.refresh} title="Refresh">
          <RefreshCw size={14} className={p.loading ? "spin" : ""} />
        </button>
        <span className="tb-sep" />
        <span className="crumbs">{p.title}</span>
        <span className="spacer" />
        <input className="search" placeholder="Filter" value={filter} onChange={(e) => setFilter(e.target.value)} />
      </Toolbar>
      <Split
        id={p.splitId}
        className="objects-split"
        secondSize={320}
        minFirst={240}
        minSecond={180}
        maxSecond="70"
        first={
          <div
            className={p.listClass ? `object-list ${p.listClass}` : "object-list"}
            tabIndex={0}
            onMouseDown={(e) => e.target === e.currentTarget && setPicked("")}
            onContextMenu={(e) =>
              e.target === e.currentTarget && openMenu(e, [...p.create, { label: "Open External…", onClick: openExternal }, { separator: true }, { label: "Refresh", onClick: p.refresh }])
            }
            onKeyDown={(e) => {
              if (e.key === "Enter" && sel) p.open(sel.path);
              if ((e.metaKey || e.ctrlKey) && e.key === "o" && sel) {
                e.preventDefault();
                p.open(sel.path);
              }
            }}
          >
            {p.error && <div className="error-banner">{p.error}</div>}
            <table>
              <thead>
                <tr>
                  <th>Name</th>
                  {p.columns.map((c) => (
                    <th key={c.title} className={c.num ? "num" : undefined}>
                      {c.title}
                    </th>
                  ))}
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
                    onDoubleClick={() => p.open(m.path)}
                    onContextMenu={(e) => (setPicked(m.path), openMenu(e, menu(m)))}
                  >
                    <td>
                      <Icon size={13} /> {m.name}
                      {m.error && <span className="err"> (can't be read)</span>}
                    </td>
                    {p.columns.map((c) => (
                      <td key={c.title} className={c.num ? "num" : c.muted ? "muted" : undefined}>
                        {c.cell(m)}
                      </td>
                    ))}
                    <td className="muted">{createdByLabel(m.createdBy)}</td>
                    <td>{when(m.modified)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            {!p.loading && !shown.length && !p.error && <div className="center-hint">{p.items.length ? `No ${noun}s match the filter.` : p.empty}</div>}
          </div>
        }
        second={
          sel ? (
            <div className="info-panel">
              <div className="info-head">
                <Icon size={16} /> {sel.name}
              </div>
              <dl>
                {p.details?.(sel)}
                {sel.createdBy && (
                  <>
                    <dt>Created by</dt>
                    <dd>{createdByLabel(sel.createdBy)}</dd>
                  </>
                )}
                <dt>Modified</dt>
                <dd>{when(sel.modified)}</dd>
                <dt>File</dt>
                <dd>
                  {sel.path}{" "}
                  <button className="link-button" title="Copy path" onClick={() => copyText(sel.path)}>
                    <Copy size={11} />
                  </button>
                </dd>
                {sel.error && (
                  <>
                    <dt>Error</dt>
                    <dd className="err">{sel.error}</dd>
                  </>
                )}
              </dl>
              {p.extra?.(sel)}
            </div>
          ) : (
            <div className="info-panel empty">Select a {noun} to see its details.</div>
          )
        }
      />
    </div>
  );
}
