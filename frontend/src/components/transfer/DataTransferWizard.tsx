// Data Transfer (Tools > Data Transfer): copy tables and views, structure and rows,
// between databases, schemas and connections, including between engines.
import { ArrowLeftRight, ArrowRight, ChevronDown } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { create } from "zustand";
import { DBType, Explorer, Layouts, ObjectKind, Transfer, arr, errorText, type DataTransferRequest, type Endpoint, type TransferOptions } from "../../api";
import { connDialect } from "../../dialect";
import { connById, useApp } from "../../store";
import { openMenu } from "../ContextMenu";
import { alert, prompt } from "../Dialogs";
import { ConnIcon } from "../icons";
import { track, useTransfer } from "./state";
import { EndpointPicker, JobProgress } from "./shared";
import { Modal, ModalFooter } from "../Modal";
import { Check } from "../form";

const useDT = create<{ open: boolean; source?: Endpoint }>(() => ({ open: false }));

/** Open the wizard, optionally starting from a source database (and schema). */
export function openDataTransfer(source?: Partial<Endpoint>) {
  useDT.setState({ open: true, source: source?.connId ? { connId: source.connId, database: source.database ?? "", schema: source.schema ?? "" } : undefined });
}

const SQL_TYPES: string[] = [DBType.MySQL, DBType.MariaDB, DBType.PostgreSQL, DBType.SQLite];
const PROFILES_KEY = "datatransfer-profiles";

type Step = "general" | "objects" | "summary" | "progress";
const STEPS: [Step, string][] = [
  ["general", "General"],
  ["objects", "Database Objects"],
  ["summary", "Summary"],
  ["progress", "Progress"],
];

/** A group of objects: every one at run time, or a fixed pick. */
interface Pick {
  all: boolean;
  names: string[];
}

interface Profile {
  source: Endpoint;
  target: Endpoint;
  tables: Pick;
  views: Pick;
  options: TransferOptions;
}

const emptyEnd = (): Endpoint => ({ connId: "", database: "", schema: "" });

export function DataTransferWizard() {
  const { open, source: preset } = useDT();
  const connections = useApp((s) => s.connections);
  const [step, setStep] = useState<Step>("general");
  const [source, setSource] = useState<Endpoint>(emptyEnd());
  const [target, setTarget] = useState<Endpoint>(emptyEnd());
  const [tables, setTables] = useState<Pick>({ all: false, names: [] });
  const [views, setViews] = useState<Pick>({ all: false, names: [] });
  const [avail, setAvail] = useState<{ tables: string[]; views: string[] }>({ tables: [], views: [] });
  const [options, setOptions] = useState<TransferOptions | null>(null);
  const [showOptions, setShowOptions] = useState(false);
  const [jobId, setJobId] = useState("");
  const [error, setError] = useState("");
  const job = useTransfer((s) => s.jobs.find((j) => j.id === jobId));
  useEffect(() => setError(""), [tables, views, source, target]);

  useEffect(() => {
    if (!open) return;
    setStep("general");
    setError("");
    setJobId("");
    setSource(preset ?? emptyEnd());
    setTarget(emptyEnd());
    setTables({ all: false, names: [] });
    setViews({ all: false, names: [] });
    Transfer.DefaultTransferOptions().then(setOptions);
  }, [open, preset]);

  // Objects of the source.
  useEffect(() => {
    setAvail({ tables: [], views: [] });
    if (!source.connId || !source.database) return;
    const list = (k: ObjectKind) => Explorer.ListObjects(source.connId, source.database, source.schema ?? "", k).then((o) => arr(o).map((x) => x.name));
    Promise.all([list(ObjectKind.KindTable), list(ObjectKind.KindView)])
      .then(([t, v]) => setAvail({ tables: t, views: v }))
      .catch((e) => setError(errorText(e)));
  }, [source.connId, source.database, source.schema]);

  if (!open || !options) return null;
  const close = () => useDT.setState({ open: false });
  const sqlConns = connections.filter((c) => SQL_TYPES.includes(c.type));
  const sameEngine = source.connId && target.connId && connDialect(source.connId) === connDialect(target.connId);
  const toDialect = target.connId ? connDialect(target.connId) : "";
  const running = job?.status === "running";

  const ready = (e: Endpoint) => !!e.connId && !!e.database && (connDialect(e.connId) !== "postgres" || !!e.schema);
  const sameSide = source.connId === target.connId && source.database === target.database && (source.schema ?? "") === (target.schema ?? "");
  const picked = (p: Pick, all: string[]) => (p.all ? all.length : p.names.length);
  const nothingPicked = picked(tables, avail.tables) + picked(views, avail.views) === 0;

  const request = (): DataTransferRequest => ({
    source,
    target,
    allTables: tables.all,
    tables: tables.all ? [] : tables.names,
    allViews: views.all,
    views: views.all ? [] : views.names,
    options,
  });

  const start = async () => {
    setError("");
    try {
      const j = await track(Transfer.DataTransfer(request()));
      useTransfer.setState({ jobsOpen: false });
      setJobId(j.id);
      setStep("progress");
    } catch (e) {
      setError(errorText(e));
    }
  };

  const next = () => {
    setError("");
    if (step === "general") {
      if (!ready(source) || !ready(target)) return setError("Choose a source and a target connection, database" + (toDialect === "postgres" || connDialect(source.connId) === "postgres" ? " and schema." : "."));
      if (sameSide) return setError("The source and the target are the same.");
      return setStep("objects");
    }
    if (step === "objects") return nothingPicked ? setError("Choose the objects to transfer.") : setStep("summary");
    if (step === "summary") return start();
  };
  const back = () => setStep(step === "summary" ? "objects" : "general");

  // ---- profiles ----
  const profiles = async (): Promise<Record<string, Profile>> => {
    try {
      return JSON.parse((await Layouts.Get(PROFILES_KEY)) || "{}");
    } catch {
      return {};
    }
  };
  const saveProfile = async () => {
    const name = await prompt("Save Profile", { message: "Profile name" });
    if (!name?.trim()) return;
    const all = await profiles();
    all[name.trim()] = { source, target, tables, views, options };
    await Layouts.Set(PROFILES_KEY, JSON.stringify(all)).catch((e) => alert("Couldn't save the profile", errorText(e)));
  };
  const loadProfile = async (e: React.MouseEvent) => {
    const all = await profiles();
    const names = Object.keys(all).sort();
    openMenu(
      e,
      names.length
        ? names.map((n) => ({
            label: n,
            onClick: async () => {
              const p = all[n];
              for (const id of [p.source.connId, p.target.connId]) if (connById(id)) await useApp.getState().connect(id);
              setSource(p.source);
              setTarget(p.target);
              setTables(p.tables);
              setViews(p.views);
              setOptions({ ...options, ...p.options });
              setStep("general");
            },
          }))
        : [{ label: "No saved profiles", disabled: true }],
    );
  };

  const header = (e: Endpoint) => {
    const c = connById(e.connId);
    return c ? (
      <span className="dt-end">
        <ConnIcon type={c.type} preset={c.preset} size={20} />
        <span>
          <strong>{c.name}</strong>
          <span className="muted">{[e.database, e.schema].filter(Boolean).join(".") || "—"}</span>
        </span>
      </span>
    ) : (
      <span className="dt-end muted">Not chosen</span>
    );
  };

  return (
    <Modal title="Data Transfer" className="wizard wide data-transfer" onClose={running || showOptions ? undefined : close}>
      <div className="dt-header">
        {header(source)}
        <ArrowRight size={18} className="muted" />
        {header(target)}
      </div>
      <div className="wizard-steps">
        {STEPS.map(([s, label], i) => (
          <span key={s} className={step === s ? "active" : ""}>
            {i + 1}. {label}
          </span>
        ))}
      </div>
      <div className="modal-body dt-body">
        {step === "general" && (
          <div className="dt-general">
            <EndpointPicker title="Source" value={source} onChange={setSource} conns={sqlConns} />
            <button
              className="dt-swap"
              title="Swap source and target"
              onClick={() => {
                setSource(target);
                setTarget(source);
              }}
            >
              <ArrowLeftRight size={16} />
            </button>
            <EndpointPicker title="Target" value={target} onChange={setTarget} conns={sqlConns} allowNew create={options.createTargetDatabase} />
          </div>
        )}
        {step === "objects" && (
          <div className="dt-objects">
            <ObjectGroup label="Tables" all={avail.tables} pick={tables} onChange={setTables} />
            <ObjectGroup label="Views" all={avail.views} pick={views} onChange={setViews} note={!sameEngine && avail.views.length ? "Views are only copied between databases of the same engine." : undefined} />
          </div>
        )}
        {step === "summary" && (
          <div className="dt-summary">
            <p>
              {[summaryText("table", tables, avail.tables), summaryText("view", views, avail.views)].filter(Boolean).join(", ")} from{" "}
              <strong>{connById(source.connId)?.name}</strong> to <strong>{connById(target.connId)?.name}</strong>.
            </p>
            <div className="dt-summary-grid">
              <div className="dt-summary-row head">
                <span>Source Object</span>
                <span>Target Object</span>
              </div>
              {summaryRows(tables, avail.tables, "tables").concat(summaryRows(views, avail.views, "views")).map(([s, t]) => (
                <div key={s} className="dt-summary-row">
                  <span>{s}</span>
                  <span>{t}</span>
                </div>
              ))}
            </div>
            <Check label="Continue on error" checked={options.continueOnError} onChange={(v) => setOptions({ ...options, continueOnError: v })} />
            <Check label="Drop target objects before create" checked={options.dropTarget} onChange={(v) => setOptions({ ...options, dropTarget: v })} />
          </div>
        )}
        {step === "progress" && job && <JobProgress job={job} />}
      </div>
      {error && <div className="status-line err">{error}</div>}
      <div className="modal-footer">
        <button disabled={running} onClick={saveProfile}>
          Save Profile
        </button>
        <button disabled={running} onClick={loadProfile}>
          Load Profile <ChevronDown size={12} />
        </button>
        <button disabled={running || step === "progress"} onClick={() => setShowOptions(true)}>
          Options…
        </button>
        <span className="spacer" />
        {step === "progress" ? (
          running ? (
            <button onClick={() => Transfer.CancelJob(jobId)}>Stop</button>
          ) : (
            <>
              <button onClick={() => setStep("summary")}>Back</button>
              <button className="primary" onClick={close}>
                Close
              </button>
            </>
          )
        ) : (
          <>
            <button onClick={close}>Cancel</button>
            {step !== "general" && <button onClick={back}>Back</button>}
            <button className="primary" onClick={next}>
              {step === "summary" ? "Start" : "Next"}
            </button>
          </>
        )}
      </div>
      {showOptions && <OptionsDialog value={options} sameEngine={!!sameEngine} to={toDialect} onClose={(o) => (o && setOptions(o), setShowOptions(false))} />}
    </Modal>
  );
}

function summaryText(kind: string, p: Pick, all: string[]) {
  if (p.all) return `All ${kind}s`;
  if (!p.names.length) return "";
  return `${p.names.length} of ${all.length} ${kind}s`;
}

function summaryRows(p: Pick, all: string[], group: string): [string, string][] {
  if (p.all) return [[`All ${group} during execution (*)`, `All ${group} during execution (*)`]];
  return p.names.filter((n) => all.includes(n)).map((n) => [n, n]);
}

// ---- objects step ---------------------------------------------------------------------

function ObjectGroup({ label, all, pick, onChange, note }: { label: string; all: string[]; pick: Pick; onChange(p: Pick): void; note?: string }) {
  const box = useRef<HTMLInputElement>(null);
  const n = pick.all ? all.length : pick.names.filter((x) => all.includes(x)).length;
  const full = n > 0 && n === all.length;
  const partial = !pick.all && n > 0 && !full;
  useEffect(() => {
    if (box.current) box.current.indeterminate = partial;
  }, [partial]);
  const [filter, setFilter] = useState("");
  const shown = useMemo(() => all.filter((x) => x.toLowerCase().includes(filter.toLowerCase())), [all, filter]);
  const tag = pick.all ? "(*)" : n ? `(${n}/${all.length})` : "";
  return (
    <div className="dt-group">
      <div className="dt-group-head">
        <label className="check">
          <input ref={box} type="checkbox" aria-label={`All ${label}`} checked={pick.all || full} onChange={(e) => onChange({ all: e.target.checked && !full, names: [] })} /> {label} <span className="muted">{tag}</span>
        </label>
        <span className="spacer" />
        {all.length > 8 && <input className="search" placeholder="Filter" value={filter} onChange={(e) => setFilter(e.target.value)} />}
      </div>
      {note && <p className="hint">{note}</p>}
      <div className="dt-group-list">
        {shown.map((name) => (
          <label key={name} className="check">
            <input
              type="checkbox"
              checked={pick.all || pick.names.includes(name)}
              onChange={(e) => {
                // Unticking one object of "all" turns it into a fixed list.
                const base = pick.all ? all : pick.names;
                onChange({ all: false, names: e.target.checked ? [...base, name] : base.filter((x) => x !== name) });
              }}
            />{" "}
            {name}
          </label>
        ))}
        {!all.length && <span className="muted">None</span>}
      </div>
    </div>
  );
}

// ---- options ----------------------------------------------------------------------------

function OptionsDialog({ value, sameEngine, to, onClose }: { value: TransferOptions; sameEngine: boolean; to: string; onClose(o?: TransferOptions): void }) {
  const [o, setO] = useState(value);
  const set = (p: Partial<TransferOptions>) => setO((x) => ({ ...x, ...p }));
  const box = (key: keyof TransferOptions, label: string, disabled = false, indent = false) => (
    <label className={"check" + (indent ? " indent" : "")}>
      <input type="checkbox" disabled={disabled} checked={!!o[key]} onChange={(e) => set({ [key]: e.target.checked } as Partial<TransferOptions>)} /> {label}
    </label>
  );
  return (
    <Modal title="Options" className="dt-options" onClose={() => onClose()} onKeyDown={(e) => e.stopPropagation()}>
      <div className="modal-body dt-options-body">
        <fieldset>
          <legend>Table Options</legend>
          {box("createTables", "Create tables")}
          {box("includeIndexes", "Include indexes", !o.createTables, true)}
          {box("includeForeignKeys", "Include foreign key constraints", !o.createTables, true)}
          {box("includeChecks", "Include checks", !o.createTables, true)}
          {box("includeUniques", "Include uniques", !o.createTables, true)}
          {box("includeAutoIncrement", "Include auto increment", !o.createTables, true)}
          {sameEngine && box("includeTriggers", "Include triggers", !o.createTables, true)}
          {sameEngine && to !== "sqlite" && box("includeCharset", "Include character set", !o.createTables, true)}
          {sameEngine && to === "mysql" && box("includeEngine", "Include engine/table type", !o.createTables, true)}
          {sameEngine && box("includeOtherOptions", "Include other table options", !o.createTables, true)}
          <Check label="Convert object name to" checked={!!o.convertNames} onChange={(v) => set({ convertNames: v ? "lower" : "" })} />
          <div className="indent row">
            <label className="check">
              <input type="radio" disabled={!o.convertNames} checked={o.convertNames !== "upper"} onChange={() => set({ convertNames: "lower" })} /> Lower case
            </label>
            <label className="check">
              <input type="radio" disabled={!o.convertNames} checked={o.convertNames === "upper"} onChange={() => set({ convertNames: "upper" })} /> Upper case
            </label>
          </div>
        </fieldset>
        <fieldset>
          <legend>Record Options</legend>
          {box("createRecords", "Create records")}
          {box("emptyTarget", "Empty target tables before creating records", !o.createRecords, true)}
          {box("useTransaction", "Use transaction", !o.createRecords, true)}
          {box("extendedInserts", "Use extended insert statements", !o.createRecords, true)}
          {to !== "" && (
            <>
              <label className="check indent">
                <input type="checkbox" disabled={!o.createRecords} checked={o.insertMode === "ignore"} onChange={(e) => set({ insertMode: e.target.checked ? "ignore" : "" })} /> Use ignore insert statements
              </label>
              {to !== "postgres" && (
                <label className="check indent">
                  <input type="checkbox" disabled={!o.createRecords} checked={o.insertMode === "replace"} onChange={(e) => set({ insertMode: e.target.checked ? "replace" : "" })} /> Use replace insert statements
                </label>
              )}
            </>
          )}
        </fieldset>
        <fieldset>
          <legend>Other Options</legend>
          {box("continueOnError", "Continue on error")}
          {box("dropTarget", "Drop target objects before create")}
          {to === "postgres" && box("dropCascade", "Drop with CASCADE", !o.dropTarget, true)}
          {to !== "sqlite" && box("createTargetDatabase", to === "postgres" ? "Create target database/schema if not exist" : "Create target database if not exist")}
        </fieldset>
      </div>
      <ModalFooter onCancel={() => onClose()} onOk={() => onClose(o)} />
    </Modal>
  );
}
