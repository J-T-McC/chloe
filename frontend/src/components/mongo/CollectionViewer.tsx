// A collection's (or view's) documents: grid / tree / JSON, with filter, sort,
// projection and paging. Edits are saved as they're made.
import { Braces, ChevronLeft, ChevronRight, ChevronsLeft, ChevronsRight, Filter, ListTree, Minus, PencilLine, PencilRuler, Plus, RefreshCw, Table2 } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { Mongo, ObjectKind, arr, errorText, type MG } from "../../api";
import { Val, field } from "../../mongo/docs";
import type { Tab } from "../../store";
import { confirm } from "../Dialogs";
import { editDocument } from "./DocEditor";
import { DocsMode, DocsView } from "./DocsView";
import { designCollection } from "./actions";

const PAGE_SIZES = [100, 500, 1000, 5000];

export function readMode(): DocsMode {
  try {
    const m = localStorage.getItem("chloe.mongoView");
    if (m === "grid" || m === "tree" || m === "json") return m;
  } catch {
    /* storage unavailable */
  }
  return "grid";
}

export function writeMode(m: DocsMode) {
  try {
    localStorage.setItem("chloe.mongoView", m);
  } catch {
    /* storage unavailable */
  }
}

export function ModeSwitch({ mode, onChange }: { mode: DocsMode; onChange(m: DocsMode): void }) {
  const b = (m: DocsMode, icon: React.ReactNode, title: string) => (
    <button title={title} className={mode === m ? "on" : ""} onClick={() => onChange(m)}>
      {icon}
    </button>
  );
  return (
    <div className="tb-group mode-switch">
      {b("grid", <Table2 size={14} />, "Grid View")}
      {b("tree", <ListTree size={14} />, "Tree View")}
      {b("json", <Braces size={14} />, "JSON View")}
    </div>
  );
}

export function CollectionViewer({ tab }: { tab: Tab }) {
  const ref = tab.ref!;
  const connId = tab.connId!;
  const db = ref.database;
  const coll = ref.name;
  const editable = ref.kind !== ObjectKind.KindView;

  const [mode, setMode] = useState<DocsMode>(readMode);
  const [page, setPageData] = useState<MG.Page | null>(null);
  const [docs, setDocs] = useState<Val[]>([]);
  const [total, setTotal] = useState<number | null>(null);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [loading, setLoading] = useState(false);
  const [showFilter, setShowFilter] = useState(false);
  const [draft, setDraft] = useState({ filter: "", sort: "", projection: "" });
  const [query, setQuery] = useState({ filter: "", sort: "", projection: "" });
  const [pageNo, setPageNo] = useState(0);
  const [pageSize, setPageSize] = useState(1000);
  const [revision, setRevision] = useState(0);
  const [dataKey, setDataKey] = useState("0");
  const [active, setActive] = useState<number | null>(null);
  const seq = useRef(0);

  const load = useCallback(async () => {
    const n = ++seq.current;
    setLoading(true);
    setError("");
    try {
      const p = await Mongo.Find(connId, { db, coll, ...query, skip: pageNo * pageSize, limit: pageSize }, true);
      if (n !== seq.current) return;
      setPageData(p);
      setDocs(arr(p.docs));
      setTotal(p.total);
      setDataKey(String(n));
      setActive(null);
    } catch (e) {
      if (n === seq.current) setError(errorText(e));
    } finally {
      if (n === seq.current) setLoading(false);
    }
  }, [connId, db, coll, query, pageNo, pageSize]);

  useEffect(() => {
    load();
  }, [load]);

  const flash = (m: string) => {
    setMessage(m);
    setTimeout(() => setMessage((x) => (x === m ? "" : x)), 3000);
  };

  const idOf = (i: number) => field(docs[i], "_id");

  const setField = async (i: number, path: string, value: Val | null) => {
    const id = idOf(i);
    if (!id) throw new Error("the document has no _id");
    await Mongo.EditField(connId, db, coll, { id, path, unset: value === null, value: value ?? { t: "null" } });
    const fresh = await Mongo.GetDoc(connId, db, coll, id);
    setDocs((d) => d.map((x, j) => (j === i ? fresh : x)));
    setRevision((r) => r + 1);
    flash(value === null ? `Removed ${path}` : `Saved ${path}`);
  };

  const remove = async (rows: number[]) => {
    const ids = rows.map(idOf).filter((x): x is Val => !!x);
    if (!ids.length) return;
    if (!(await confirm(rows.length === 1 ? "Delete this document?" : `Delete ${rows.length} documents?`, { okLabel: "Delete" }))) return;
    try {
      const n = await Mongo.DeleteDocs(connId, db, coll, ids);
      flash(`Deleted ${n} document${n === 1 ? "" : "s"}`);
      load();
    } catch (e) {
      setError(errorText(e));
    }
  };

  const editDoc = async (i: number) => {
    if (await editDocument({ connId, db, coll, doc: docs[i] })) {
      const id = idOf(i);
      if (id) {
        const fresh = await Mongo.GetDoc(connId, db, coll, id).catch(() => null);
        if (fresh) {
          setDocs((d) => d.map((x, j) => (j === i ? fresh : x)));
          setRevision((r) => r + 1);
        }
      }
      flash("Document saved");
    }
  };

  const add = async () => {
    if (await editDocument({ connId, db, coll })) {
      flash("Document added");
      load();
    }
  };

  const apply = () => {
    setPageNo(0);
    setQuery({ ...draft });
  };
  const pages = total !== null && total >= 0 ? Math.max(1, Math.ceil(total / pageSize)) : null;
  const sortSpec = parseSort(query.sort);
  const changeMode = (m: DocsMode) => {
    setMode(m);
    writeMode(m);
  };

  return (
    <div className="viewer mongo-viewer">
      <div className="toolbar">
        <button title="Filter, sort and projection" className={showFilter || query.filter || query.sort || query.projection ? "on" : ""} onClick={() => setShowFilter((x) => !x)}>
          <Filter size={14} /> Filter
        </button>
        <span className="tb-sep" />
        {editable && (
          <>
            <button title="Add Document" onClick={add}>
              <Plus size={14} /> Add
            </button>
            <button title="Edit Document" disabled={active === null} onClick={() => active !== null && editDoc(active)}>
              <PencilLine size={14} /> Edit
            </button>
          </>
        )}
        <button title={editable ? "Design Collection" : "Design View"} onClick={() => designCollection(connId, db, coll, ref.kind)}>
          <PencilRuler size={14} /> Design
        </button>
        <span className="spacer" />
        {!editable && <span className="badge">read-only</span>}
      </div>

      {showFilter && (
        <div className="filter-bar mongo-filter">
          {(["filter", "sort", "projection"] as const).map((k) => (
            <label key={k}>
              <span className="kw">{k[0].toUpperCase() + k.slice(1)}</span>
              <input
                autoFocus={k === "filter"}
                spellCheck={false}
                placeholder={k === "filter" ? "{ status: 'paid' }" : k === "sort" ? "{ placed_at: -1 }" : "{ items: 0 }"}
                value={draft[k]}
                onChange={(e) => setDraft({ ...draft, [k]: e.target.value })}
                onKeyDown={(e) => e.key === "Enter" && apply()}
              />
            </label>
          ))}
          <button onClick={apply}>Apply</button>
          {(query.filter || query.sort || query.projection) && (
            <button
              onClick={() => {
                const empty = { filter: "", sort: "", projection: "" };
                setDraft(empty);
                setQuery(empty);
                setPageNo(0);
              }}
            >
              Clear
            </button>
          )}
        </div>
      )}

      <div
        className="viewer-body"
        onKeyDown={(e) => {
          if ((e.metaKey || e.ctrlKey) && e.key === "r") {
            e.preventDefault();
            load();
          }
        }}
      >
        {error && (
          <div className="error-banner" onClick={() => setError("")}>
            {error}
          </div>
        )}
        {page && (
          <DocsView
            mode={mode}
            docs={docs}
            fields={arr(page.fields)}
            dataKey={dataKey}
            revision={revision}
            rowOffset={pageNo * pageSize}
            onSetField={editable ? setField : undefined}
            onDelete={editable ? remove : undefined}
            onEditDoc={editable ? editDoc : undefined}
            onAdd={editable ? add : undefined}
            sort={sortSpec}
            onSort={(column, desc) => {
              const s = desc === null ? "" : `{ ${JSON.stringify(column)}: ${desc ? -1 : 1} }`;
              setDraft((d) => ({ ...d, sort: s }));
              setQuery((q) => ({ ...q, sort: s }));
              setPageNo(0);
            }}
            onActive={(i) => setActive(i)}
          />
        )}
      </div>

      <div className="statusbar">
        {editable && (
          <div className="tb-group">
            <button title="Add Document" onClick={add}>
              <Plus size={14} />
            </button>
            <button title="Delete Document" disabled={active === null} onClick={() => active !== null && remove([active])}>
              <Minus size={14} />
            </button>
          </div>
        )}
        <div className="tb-group">
          <button title="Refresh (⌘R)" onClick={load}>
            <RefreshCw size={14} className={loading ? "spin" : ""} />
          </button>
        </div>
        <span className="sql-preview">{message || `db.getCollection(${JSON.stringify(coll)}).find(${query.filter || "{}"}${query.projection ? ", " + query.projection : ""})${query.sort ? `.sort(${query.sort})` : ""}`}</span>
        <span>{total === null ? "…" : total.toLocaleString()} documents</span>
        <select
          value={pageSize}
          onChange={(e) => {
            const size = Number(e.target.value);
            setPageNo(0);
            setPageSize(size);
          }}
        >
          {PAGE_SIZES.map((n) => (
            <option key={n} value={n}>
              {n.toLocaleString()} / page
            </option>
          ))}
        </select>
        <div className="pager">
          <button disabled={pageNo === 0} onClick={() => setPageNo(0)}>
            <ChevronsLeft size={13} />
          </button>
          <button disabled={pageNo === 0} onClick={() => setPageNo(pageNo - 1)}>
            <ChevronLeft size={13} />
          </button>
          <span>
            {pageNo + 1}
            {pages ? ` / ${pages}` : ""}
          </span>
          <button disabled={pages !== null && pageNo >= pages - 1} onClick={() => setPageNo(pageNo + 1)}>
            <ChevronRight size={13} />
          </button>
          <button disabled={!pages || pageNo >= pages - 1} onClick={() => pages && setPageNo(pages - 1)}>
            <ChevronsRight size={13} />
          </button>
        </div>
        <ModeSwitch mode={mode} onChange={changeMode} />
      </div>
    </div>
  );
}

/** "{ a: 1, "b": -1 }" → grid sort markers (best effort, for the column headers). */
function parseSort(s: string): { column: string; desc: boolean }[] {
  const out: { column: string; desc: boolean }[] = [];
  for (const m of s.matchAll(/["']?([\w.$-]+)["']?\s*:\s*(-?1)/g)) out.push({ column: m[1], desc: m[2] === "-1" });
  return out;
}
