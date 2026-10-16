// Structure Synchronization (Tools menu): compare the tables and views of two
// databases/schemas of the same engine and deploy the changes to the target.
import { ArrowLeftRight, ArrowRight, Copy, FilePlus2, RefreshCw } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { create } from "zustand";
import { DBType, Transfer, arr, errorText, type Endpoint, type StructItem, copyText } from "../../api";
import { connDialect } from "../../dialect";
import { connById, useApp } from "../../store";
import { ConnIcon, KindIcon } from "../icons";
import { CodeBlock } from "../ObjectsPane";
import { newQuery } from "../Sidebar";
import { Split } from "../Split";
import { EndpointPicker, JobProgress } from "./shared";
import { track, useTransfer } from "./state";
import { Toolbar } from "../Toolbar";

const useSS = create<{ open: boolean; source?: Endpoint }>(() => ({ open: false }));

export function openStructureSync(source?: Partial<Endpoint>) {
  useSS.setState({ open: true, source: source?.connId ? { connId: source.connId, database: source.database ?? "", schema: source.schema ?? "" } : undefined });
}

// Navicat compares the same engine only (MySQL and MariaDB count as one); SQLite isn't supported.
const TYPES: string[] = [DBType.MySQL, DBType.MariaDB, DBType.PostgreSQL];

type Step = "general" | "results" | "deploy" | "log";
const STEPS: [Step, string][] = [
  ["general", "General"],
  ["results", "Compare Results"],
  ["deploy", "Deployment"],
  ["log", "Message Log"],
];
const OP_LABEL: Record<string, string> = { create: "Create", modify: "Modify", drop: "Drop", same: "Same" };
const key = (it: StructItem) => `${it.kind}:${it.name}`;
const emptyEnd = (): Endpoint => ({ connId: "", database: "", schema: "" });

export function StructureSyncWizard() {
  const { open, source: preset } = useSS();
  const connections = useApp((s) => s.connections);
  const [step, setStep] = useState<Step>("general");
  const [source, setSource] = useState<Endpoint>(emptyEnd());
  const [target, setTarget] = useState<Endpoint>(emptyEnd());
  const [opts, setOpts] = useState({ compareTables: true, compareViews: true, dropCascade: false });
  const [items, setItems] = useState<StructItem[]>([]);
  const [checked, setChecked] = useState<Set<string>>(new Set());
  const [showSame, setShowSame] = useState(true);
  const [sel, setSel] = useState<string>("");
  const [script, setScript] = useState<string[]>([]);
  const [continueOnError, setContinueOnError] = useState(false);
  const [busy, setBusy] = useState(false);
  const [jobId, setJobId] = useState("");
  const [error, setError] = useState("");
  const job = useTransfer((s) => s.jobs.find((j) => j.id === jobId));

  useEffect(() => {
    if (!open) return;
    setStep("general");
    setError("");
    setItems([]);
    setJobId("");
    setSource(preset ?? emptyEnd());
    setTarget(emptyEnd());
  }, [open, preset]);
  useEffect(() => setError(""), [source, target]);

  const chosen = useMemo(() => items.filter((it) => checked.has(key(it))), [items, checked]);
  if (!open) return null;
  const close = () => useSS.setState({ open: false });
  const conns = connections.filter((c) => TYPES.includes(c.type));
  const running = job?.status === "running";
  const pg = target.connId && connDialect(target.connId) === "postgres";

  const compare = async () => {
    setError("");
    if (!source.connId || !source.database || !target.connId || !target.database) return setError("Choose a source and a target.");
    if (connDialect(source.connId) !== connDialect(target.connId)) return setError("Structure synchronization compares two databases of the same engine. Use Data Transfer between engines.");
    setBusy(true);
    try {
      const res = await Transfer.CompareStructure({ source, target, options: opts });
      const list = arr(res.items);
      setItems(list);
      // Differences start checked; identical objects can't be.
      setChecked(new Set(list.filter((it) => it.op !== "same").map(key)));
      setSel(list.find((it) => it.op !== "same") ? key(list.find((it) => it.op !== "same")!) : "");
      setStep("results");
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  const toDeploy = async () => {
    const s = arr(await Transfer.StructureScript(chosen));
    if (!s.length) return setError("Nothing is selected to deploy.");
    setScript(s);
    setStep("deploy");
  };

  const start = async () => {
    setError("");
    try {
      const j = await track(Transfer.Deploy({ target, statements: script, continueOnError, title: "Structure Synchronization" }));
      useTransfer.setState({ jobsOpen: false });
      setJobId(j.id);
      setStep("log");
    } catch (e) {
      setError(errorText(e));
    }
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
  const scriptText = script.join(";\n\n") + (script.length ? ";" : "");

  return (
    <div className="modal-backdrop" onMouseDown={() => !running && !busy && close()}>
      <div className="modal wizard wide data-transfer structure-sync" onMouseDown={(e) => e.stopPropagation()} onKeyDown={(e) => e.key === "Escape" && !running && close()}>
        <div className="modal-title">Structure Synchronization</div>
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
            <>
              <div className="dt-general">
                <EndpointPicker title="Source" value={source} onChange={setSource} conns={conns} />
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
                <EndpointPicker title="Target" value={target} onChange={setTarget} conns={conns} />
              </div>
              <fieldset className="ss-options">
                <legend>Compare Options</legend>
                <label className="check">
                  <input type="checkbox" checked={opts.compareTables} onChange={(e) => setOpts({ ...opts, compareTables: e.target.checked })} /> Compare tables
                </label>
                <label className="check">
                  <input type="checkbox" checked={opts.compareViews} onChange={(e) => setOpts({ ...opts, compareViews: e.target.checked })} /> Compare views
                </label>
                {pg && (
                  <label className="check">
                    <input type="checkbox" checked={opts.dropCascade} onChange={(e) => setOpts({ ...opts, dropCascade: e.target.checked })} /> Drop with CASCADE
                  </label>
                )}
              </fieldset>
            </>
          )}
          {step === "results" && (
            <Results
              items={items}
              checked={checked}
              setChecked={setChecked}
              showSame={showSame}
              setShowSame={setShowSame}
              sel={sel}
              setSel={setSel}
              source={source}
              target={target}
              onRecompare={compare}
              busy={busy}
            />
          )}
          {step === "deploy" && (
            <div className="ss-deploy">
              <Toolbar>
                <button onClick={() => copyText(scriptText)}>
                  <Copy size={14} /> Copy Script
                </button>
                <button
                  onClick={() => {
                    newQuery(target.connId, target.database, target.schema || undefined, scriptText);
                    close();
                  }}
                >
                  <FilePlus2 size={14} /> Open in Query Editor
                </button>
                <span className="spacer" />
                <label className="check">
                  <input type="checkbox" checked={continueOnError} onChange={(e) => setContinueOnError(e.target.checked)} /> Continue on error
                </label>
                <span className="muted">{script.length} statement(s)</span>
              </Toolbar>
              <div className="ss-script">
                <CodeBlock code={scriptText} />
              </div>
            </div>
          )}
          {step === "log" && job && <JobProgress job={job} />}
        </div>
        {error && <div className="status-line err">{error}</div>}
        <div className="modal-footer">
          <span className="spacer" />
          {step === "log" ? (
            running ? (
              <button onClick={() => Transfer.CancelJob(jobId)}>Stop</button>
            ) : (
              <>
                <button onClick={compare}>Recompare</button>
                <button className="primary" onClick={close}>
                  Close
                </button>
              </>
            )
          ) : (
            <>
              <button onClick={close}>Cancel</button>
              {step !== "general" && <button onClick={() => setStep(step === "deploy" ? "results" : "general")}>Back</button>}
              {step === "general" && (
                <button className="primary" disabled={busy || (!opts.compareTables && !opts.compareViews)} onClick={compare}>
                  {busy ? "Comparing…" : "Compare"}
                </button>
              )}
              {step === "results" && (
                <button className="primary" disabled={!chosen.length} onClick={toDeploy}>
                  Next
                </button>
              )}
              {step === "deploy" && (
                <button className="primary" onClick={start}>
                  Start
                </button>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  );
}

function Results(p: {
  items: StructItem[];
  checked: Set<string>;
  setChecked(s: Set<string>): void;
  showSame: boolean;
  setShowSame(v: boolean): void;
  sel: string;
  setSel(k: string): void;
  source: Endpoint;
  target: Endpoint;
  onRecompare(): void;
  busy: boolean;
}) {
  const [tab, setTab] = useState<"ddl" | "script">("ddl");
  const shown = p.items.filter((it) => p.showSame || it.op !== "same");
  const groups: [string, string][] = [
    ["table", "Tables"],
    ["view", "Views"],
  ];
  const picked = p.items.find((it) => key(it) === p.sel);
  const toggle = (ks: string[], on: boolean) => {
    const next = new Set(p.checked);
    for (const k of ks) on ? next.add(k) : next.delete(k);
    p.setChecked(next);
  };
  const differing = p.items.filter((it) => it.op !== "same");
  const counts = ["create", "modify", "drop", "same"].map((op) => [op, p.items.filter((it) => it.op === op).length] as const).filter(([, n]) => n);
  return (
    <div className="ss-results">
      <Toolbar>
        <button onClick={() => toggle(differing.map(key), true)}>Select All</button>
        <button onClick={() => p.setChecked(new Set())}>Deselect All</button>
        <button onClick={p.onRecompare} disabled={p.busy}>
          <RefreshCw size={14} className={p.busy ? "spin" : ""} /> Recompare
        </button>
        <span className="spacer" />
        <span className="muted">{counts.map(([op, n]) => `${n} ${OP_LABEL[op].toLowerCase()}`).join(" · ") || "Nothing to compare"}</span>
        <label className="check">
          <input type="checkbox" checked={p.showSame} onChange={(e) => p.setShowSame(e.target.checked)} /> Show identical objects
        </label>
      </Toolbar>
      <Split
        id="structure-sync"
        vertical
        firstSize="55"
        minFirst="20"
        minSecond="15"
        first={
          <div className="ss-tree">
            <div className="ss-row head">
              <span />
              <span>Source Object</span>
              <span>Operation</span>
              <span>Target Object</span>
            </div>
            {groups.map(([kind, label]) => {
              const rows = shown.filter((it) => it.kind === kind);
              if (!rows.length) return null;
              const selectable = rows.filter((it) => it.op !== "same").map(key);
              const on = selectable.filter((k) => p.checked.has(k)).length;
              return (
                <div key={kind}>
                  <div className="ss-row group">
                    <GroupBox all={selectable.length} on={on} onChange={(v) => toggle(selectable, v)} />
                    <span>
                      {label} <span className="muted">({rows.length})</span>
                    </span>
                    <span />
                    <span />
                  </div>
                  {rows.map((it) => (
                    <div key={key(it)} className={"ss-row" + (p.sel === key(it) ? " selected" : "") + (it.op === "same" ? " same" : "")} onClick={() => p.setSel(key(it))}>
                      <input type="checkbox" aria-label={`Include ${it.name}`} disabled={it.op === "same"} checked={p.checked.has(key(it))} onChange={(e) => toggle([key(it)], e.target.checked)} onClick={(e) => e.stopPropagation()} />
                      <span>{it.op !== "drop" && (<><KindIcon kind={it.kind} /> {it.name}</>)}</span>
                      <span className={"ss-op op-" + it.op}>{OP_LABEL[it.op] ?? it.op}</span>
                      <span>{it.op !== "create" && (<><KindIcon kind={it.kind} /> {it.name}</>)}</span>
                    </div>
                  ))}
                </div>
              );
            })}
            {!shown.length && <div className="center-hint">{p.items.length ? "The source and the target are identical." : "Nothing to compare."}</div>}
          </div>
        }
        second={
          <div className="ss-detail">
            <div className="result-tabs">
              <button className={tab === "ddl" ? "active" : ""} onClick={() => setTab("ddl")}>
                DDL Comparison
              </button>
              <button className={tab === "script" ? "active" : ""} onClick={() => setTab("script")}>
                Deployment Script
              </button>
            </div>
            {!picked ? (
              <div className="center-hint">Select an object.</div>
            ) : tab === "ddl" ? (
              <DDLCompare
                source={picked.sourceDdl ?? ""}
                target={picked.targetDdl ?? ""}
                sourceLabel={[p.source.database, p.source.schema].filter(Boolean).join(".")}
                targetLabel={[p.target.database, p.target.schema].filter(Boolean).join(".")}
                sourceQualifier={p.source.schema || p.source.database}
                targetQualifier={p.target.schema || p.target.database}
              />
            ) : (
              <div className="ss-script">
                {picked.warnings?.map((w) => (
                  <div key={w} className="hint">
                    {w}
                  </div>
                ))}
                <CodeBlock code={[...arr(picked.pre), ...arr(picked.statements), ...arr(picked.post)].join(";\n\n") || "-- no changes"} />
              </div>
            )}
          </div>
        }
      />
    </div>
  );
}

function GroupBox({ all, on, onChange }: { all: number; on: number; onChange(v: boolean): void }) {
  const ref = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (ref.current) ref.current.indeterminate = on > 0 && on < all;
  }, [on, all]);
  return <input ref={ref} type="checkbox" disabled={!all} checked={all > 0 && on === all} onChange={(e) => onChange(e.target.checked)} />;
}

/** Source and target DDL side by side; lines found on only one side are marked. */
function DDLCompare(p: { source: string; target: string; sourceLabel: string; targetLabel: string; sourceQualifier: string; targetQualifier: string }) {
  const { source, target, sourceLabel, targetLabel } = p;
  // Each side's own database/schema name doesn't count as a difference.
  const strip = (l: string, q: string) => (q ? l.split(`"${q}".`).join("").split(`\`${q}\`.`).join("").split(`${q}.`).join("") : l);
  const norm = (l: string, q: string) => strip(l, q).trim().replace(/,$/, "").toLowerCase();
  const sl = source ? source.split("\n") : [];
  const tl = target ? target.split("\n") : [];
  const sSet = new Set(sl.map((l) => norm(l, p.sourceQualifier)));
  const tSet = new Set(tl.map((l) => norm(l, p.targetQualifier)));
  const side = (lines: string[], other: Set<string>, q: string, cls: string, empty: string) => (
    <pre className="ddl-side">
      {lines.length ? lines.map((l, i) => (
        <div key={i} className={other.has(norm(l, q)) || !l.trim() ? "" : cls}>
          {l || " "}
        </div>
      )) : <span className="muted">{empty}</span>}
    </pre>
  );
  return (
    <div className="ddl-compare">
      <div>
        <div className="ddl-label">Source: {sourceLabel}</div>
        {side(sl, tSet, p.sourceQualifier, "only-source", "(not in the source)")}
      </div>
      <div>
        <div className="ddl-label">Target: {targetLabel}</div>
        {side(tl, sSet, p.targetQualifier, "only-target", "(not in the target)")}
      </div>
    </div>
  );
}
