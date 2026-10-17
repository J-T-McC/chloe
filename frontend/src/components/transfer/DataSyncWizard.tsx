// Data Synchronization (Tools menu): compare the rows of two databases of the same
// engine by primary key and make the target's rows match the source's.
import { RefreshCw } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { create } from "zustand";
import { DBType, Transfer, arr, errorText, type DataSyncPage, type DataSyncResult, type DataSyncTable, type Endpoint } from "../../api";
import { connDialect } from "../../dialect";
import { useApp } from "../../store";
import { Split } from "../Split";
import { JobProgress } from "./shared";
import { DeployStep, EndpointPair, EndpointsHeader, WizardFooter, WizardSteps, emptyEnd, presetEnd, useWizardJob } from "./wizard";
import { Toolbar } from "../Toolbar";
import { Modal } from "../Modal";
import { Check } from "../form";

const useDS = create<{ open: boolean; source?: Endpoint }>(() => ({ open: false }));

export function openDataSync(source?: Partial<Endpoint>) {
  useDS.setState({ open: true, source: presetEnd(source) });
}

const TYPES: string[] = [DBType.MySQL, DBType.MariaDB, DBType.PostgreSQL, DBType.SQLite];
type Step = "general" | "mapping" | "results" | "deploy" | "log";
const STEPS: [Step, string][] = [
  ["general", "General"],
  ["mapping", "Mapping"],
  ["results", "Compare Results"],
  ["deploy", "Deployment"],
  ["log", "Message Log"],
];
type Act = "insert" | "update" | "delete";
const ACTS: Act[] = ["insert", "update", "delete"];
const PAGE = 100;

export function DataSyncWizard() {
  const { open, source: preset } = useDS();
  const connections = useApp((s) => s.connections);
  const [step, setStep] = useState<Step>("general");
  const [source, setSource] = useState<Endpoint>(emptyEnd());
  const [target, setTarget] = useState<Endpoint>(emptyEnd());
  const [acts, setActs] = useState<Record<Act, boolean>>({ insert: true, update: true, delete: true });
  const [mapping, setMapping] = useState<DataSyncTable[]>([]);
  const [mapped, setMapped] = useState<Set<string>>(new Set());
  const [targets, setTargets] = useState<string[]>([]);
  const [cmp, setCmp] = useState<{ id: string; tables: DataSyncResult[] } | null>(null);
  const [chosen, setChosen] = useState<Record<string, Record<Act, boolean>>>({});
  const [sel, setSel] = useState("");
  const [showSame, setShowSame] = useState(true);
  const [script, setScript] = useState<string[]>([]);
  const [continueOnError, setContinueOnError] = useState(false);
  const [useTx, setUseTx] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const { job, running, reset, start: startJob, stop } = useWizardJob();

  useEffect(() => {
    if (!open) return;
    setStep("general");
    setError("");
    setCmp(null);
    reset();
    setSource(preset ?? emptyEnd());
    setTarget(emptyEnd());
  }, [open, preset, reset]);
  useEffect(() => setError(""), [source, target]);
  // Free the comparison on the server when it's replaced or the wizard closes.
  useEffect(() => () => void (cmp && Transfer.DiscardDataSync(cmp.id)), [cmp]);

  if (!open) return null;
  const close = () => {
    if (cmp) Transfer.DiscardDataSync(cmp.id);
    setCmp(null);
    useDS.setState({ open: false });
  };
  const conns = connections.filter((c) => TYPES.includes(c.type));

  const loadMapping = async () => {
    setError("");
    if (!source.connId || !source.database || !target.connId || !target.database) return setError("Choose a source and a target.");
    if (connDialect(source.connId) !== connDialect(target.connId)) return setError("Data synchronization compares two databases of the same engine. Use Data Transfer between engines.");
    if (source.connId === target.connId && source.database === target.database && (source.schema ?? "") === (target.schema ?? "")) return setError("The source and the target are the same.");
    setBusy(true);
    try {
      const list = arr(await Transfer.DataSyncTables(source, target));
      setMapping(list);
      // Same-name tables with a key are mapped by default.
      setMapped(new Set(list.filter((t) => !t.message).map((t) => t.source)));
      setTargets([...new Set(list.map((t) => t.target).filter(Boolean))].sort());
      setStep("mapping");
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  const compare = async () => {
    setError("");
    const tables = mapping.filter((t) => mapped.has(t.source) && t.target);
    if (!tables.length) return setError("Map at least one table.");
    setBusy(true);
    try {
      const res = await Transfer.CompareData({ source, target, tables });
      const list = arr(res.tables);
      setCmp({ id: res.id, tables: list });
      // Tables with differences, and their actions allowed by the options, start checked.
      setChosen(Object.fromEntries(list.map((t) => [t.source, { insert: acts.insert && t.insert > 0, update: acts.update && t.update > 0, delete: acts.delete && t.delete > 0 }])));
      setSel(list.find((t) => t.insert + t.update + t.delete > 0)?.source ?? list[0]?.source ?? "");
      setStep("results");
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  const selections = () =>
    Object.entries(chosen)
      .filter(([, a]) => a.insert || a.update || a.delete)
      .map(([table, a]) => ({ table, ...a }));

  const toDeploy = async () => {
    setError("");
    try {
      const s = arr(await Transfer.DataSyncScript(cmp!.id, selections()));
      if (!s.length) return setError("Nothing is selected to deploy.");
      setScript(s);
      setStep("deploy");
    } catch (e) {
      setError(errorText(e));
    }
  };

  const start = async () => {
    setError("");
    try {
      await startJob(Transfer.DeployDataSync({ id: cmp!.id, tables: selections(), continueOnError, useTransaction: useTx }));
      setStep("log");
    } catch (e) {
      setError(errorText(e));
    }
  };

  const scriptText = script.join(";\n") + (script.length ? ";" : "");
  const PREVIEW = 2000;

  return (
    <Modal title="Data Synchronization" className="wizard wide data-transfer data-sync" onClose={running || busy ? undefined : close}>
      <EndpointsHeader source={source} target={target} />
      <WizardSteps steps={STEPS} step={step} />
      <div className="modal-body dt-body">
        {step === "general" && (
          <>
            <EndpointPair source={source} target={target} setSource={setSource} setTarget={setTarget} conns={conns} />
            <fieldset className="ss-options">
              <legend>Options</legend>
              {ACTS.map((a) => (
                <label key={a} className="check">
                  <input type="checkbox" checked={acts[a]} onChange={(e) => setActs({ ...acts, [a]: e.target.checked })} /> {a[0].toUpperCase() + a.slice(1)}
                </label>
              ))}
            </fieldset>
          </>
        )}
        {step === "mapping" && (
          <div className="dsync-mapping">
            <Toolbar>
              <button onClick={() => setMapped(new Set(mapping.filter((t) => t.target && t.key?.length).map((t) => t.source)))}>Select All</button>
              <button onClick={() => setMapped(new Set())}>Deselect All</button>
              <span className="spacer" />
              <span className="muted">{mapped.size} of {mapping.length} table(s) mapped</span>
            </Toolbar>
            <div className="dsync-grid">
              <div className="dsync-row head">
                <span />
                <span>Source Table</span>
                <span>Target Table</span>
                <span>Key</span>
                <span>Condition</span>
              </div>
              {mapping.map((t, i) => (
                <div key={t.source} className={"dsync-row" + (t.message && !t.target ? " same" : "")}>
                  <input
                    type="checkbox"
                    aria-label={`Map ${t.source}`}
                    disabled={!t.target || !t.key?.length}
                    checked={mapped.has(t.source)}
                    onChange={(e) => {
                      const n = new Set(mapped);
                      e.target.checked ? n.add(t.source) : n.delete(t.source);
                      setMapped(n);
                    }}
                  />
                  <span>{t.source}</span>
                  <span>
                    <select
                      value={t.target}
                      onChange={(e) => setMapping(mapping.map((x, j) => (j === i ? { ...x, target: e.target.value, message: e.target.value ? (x.key?.length ? "" : x.message) : "Unmapped" } : x)))}
                    >
                      <option value="">(not mapped)</option>
                      {targets.concat(t.target && !targets.includes(t.target) ? [t.target] : []).map((n) => (
                        <option key={n}>{n}</option>
                      ))}
                    </select>
                  </span>
                  <span className={t.target && t.key?.length ? "" : "muted"}>{!t.target ? t.message || "Not mapped" : t.key?.length ? t.key.join(", ") : t.message}</span>
                  <span>
                    <input className="mono" placeholder="(all rows)" disabled={!t.target} value={t.condition ?? ""} onChange={(e) => setMapping(mapping.map((x, j) => (j === i ? { ...x, condition: e.target.value } : x)))} />
                  </span>
                </div>
              ))}
            </div>
          </div>
        )}
        {step === "results" && cmp && (
          <Results cmp={cmp} chosen={chosen} setChosen={setChosen} sel={sel} setSel={setSel} showSame={showSame} setShowSame={setShowSame} onRecompare={compare} busy={busy} />
        )}
        {step === "deploy" && (
          <DeployStep
            script={scriptText}
            shown={script.length > PREVIEW ? script.slice(0, PREVIEW).join(";\n") + `;\n-- … ${script.length - PREVIEW} more statement(s)` : undefined}
            count={script.length}
            target={target}
            onClose={close}
          >
            <Check label="Use transaction" checked={useTx} onChange={setUseTx} />
            <Check label="Continue on error" checked={continueOnError} onChange={setContinueOnError} />
          </DeployStep>
        )}
        {step === "log" && job && <JobProgress job={job} />}
      </div>
      {error && <div className="status-line err">{error}</div>}
      <WizardFooter
        last={step === "log"}
        running={running}
        onStop={stop}
        onClose={close}
        after={<button onClick={compare}>Recompare</button>}
        onBack={step !== "general" ? () => setStep(step === "deploy" ? "results" : step === "results" ? "mapping" : "general") : undefined}
        next={
          step === "general" ? (
            <button className="primary" disabled={busy} onClick={loadMapping}>
              Next
            </button>
          ) : step === "mapping" ? (
            <button className="primary" disabled={busy} onClick={compare}>
              {busy ? "Comparing…" : "Compare & Preview"}
            </button>
          ) : step === "results" ? (
            <button className="primary" onClick={toDeploy}>
              Next
            </button>
          ) : (
            <button className="primary" onClick={start}>
              Start
            </button>
          )
        }
      />
    </Modal>
  );
}

function Results(p: {
  cmp: { id: string; tables: DataSyncResult[] };
  chosen: Record<string, Record<Act, boolean>>;
  setChosen(c: Record<string, Record<Act, boolean>>): void;
  sel: string;
  setSel(s: string): void;
  showSame: boolean;
  setShowSame(v: boolean): void;
  onRecompare(): void;
  busy: boolean;
}) {
  const shown = p.cmp.tables.filter((t) => p.showSame || t.insert + t.update + t.delete > 0 || t.message);
  const set = (table: string, a: Act, v: boolean) => p.setChosen({ ...p.chosen, [table]: { ...p.chosen[table], [a]: v } });
  return (
    <div className="ss-results">
      <Toolbar>
        <button onClick={p.onRecompare} disabled={p.busy}>
          <RefreshCw size={14} className={p.busy ? "spin" : ""} /> Recompare
        </button>
        <span className="spacer" />
        <Check label="Show identical tables and others" checked={p.showSame} onChange={(v) => p.setShowSame(v)} />
      </Toolbar>
      <Split
        id="data-sync"
        vertical
        firstSize="45"
        minFirst="20"
        minSecond="20"
        first={
          <div className="ss-tree">
            <div className="dsr-row head">
              <span>Source Table</span>
              <span>Target Table</span>
              <span>Insert</span>
              <span>Update</span>
              <span>Delete</span>
              <span>Same</span>
              <span>Message</span>
            </div>
            {shown.map((t) => (
              <div key={t.source} className={"dsr-row" + (p.sel === t.source ? " selected" : "") + (t.message ? " same" : "")} onClick={() => !t.message && p.setSel(t.source)}>
                <span>{t.source}</span>
                <span>{t.target}</span>
                {ACTS.map((a) => (
                  <span key={a}>
                    <label className="check">
                      <input type="checkbox" aria-label={`${a} ${t.source}`} disabled={!t[a]} checked={!!p.chosen[t.source]?.[a]} onChange={(e) => set(t.source, a, e.target.checked)} onClick={(e) => e.stopPropagation()} /> {t[a].toLocaleString()}
                    </label>
                  </span>
                ))}
                <span>{t.same.toLocaleString()}</span>
                <span className="muted">{t.message}</span>
              </div>
            ))}
          </div>
        }
        second={p.sel ? <Records id={p.cmp.id} table={p.sel} /> : <div className="center-hint">Select a table.</div>}
      />
    </div>
  );
}

/** A table's differing records: source and target values side by side, changed cells marked. */
function Records({ id, table }: { id: string; table: string }) {
  const [op, setOp] = useState("");
  const [offset, setOffset] = useState(0);
  const [page, setPage] = useState<DataSyncPage | null>(null);
  const [err, setErr] = useState("");
  useEffect(() => setOffset(0), [table, op]);
  useEffect(() => {
    Transfer.DataSyncRecords(id, table, op, offset, PAGE)
      .then(setPage)
      .catch((e) => setErr(errorText(e)));
  }, [id, table, op, offset]);
  const cols = useMemo(() => arr(page?.columns), [page]);
  const cell = (v?: string | null) => (v === null || v === undefined ? <span className="null">(Null)</span> : v);
  return (
    <div className="dsync-records">
      <Toolbar>
        <strong>{table}</strong>
        <select aria-label="Show records" value={op} onChange={(e) => setOp(e.target.value)}>
          <option value="">Difference</option>
          <option value="insert">Insert</option>
          <option value="update">Update</option>
          <option value="delete">Delete</option>
        </select>
        <span className="spacer" />
        {page && page.total > PAGE && (
          <>
            <button disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - PAGE))}>
              Previous
            </button>
            <span className="muted">
              {offset + 1}–{Math.min(page.total, offset + PAGE)} of {page.total.toLocaleString()}
            </span>
            <button disabled={offset + PAGE >= page.total} onClick={() => setOffset(offset + PAGE)}>
              Next
            </button>
          </>
        )}
        {page && page.total <= PAGE && <span className="muted">{page.total.toLocaleString()} record(s)</span>}
        {page && !!page.more && <span className="muted">and {page.more.toLocaleString()} more, not listed</span>}
      </Toolbar>
      {err && <div className="err small">{err}</div>}
      <div className="dsync-table">
        <table>
          <thead>
            <tr>
              <th />
              {cols.map((c, i) => (
                <th key={c} className={i < (page?.key ?? 0) ? "key" : ""}>
                  {c}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {arr(page?.records).map((r, i) => {
              const changed = new Set(arr(r.changed));
              return [
                r.source && (
                  <tr key={i + "s"} className={"src op-" + r.op}>
                    <td className="muted">{r.op === "update" ? "Source" : "Insert"}</td>
                    {arr(r.source).map((v, j) => (
                      <td key={j} className={changed.has(j) ? "changed" : ""}>
                        {cell(v)}
                      </td>
                    ))}
                  </tr>
                ),
                r.target && (
                  <tr key={i + "t"} className={"dst op-" + r.op}>
                    <td className="muted">{r.op === "update" ? "Target" : "Delete"}</td>
                    {arr(r.target).map((v, j) => (
                      <td key={j} className={changed.has(j) ? "changed" : ""}>
                        {cell(v)}
                      </td>
                    ))}
                  </tr>
                ),
              ];
            })}
          </tbody>
        </table>
        {page && !page.total && <div className="center-hint">No differing records.</div>}
      </div>
    </div>
  );
}
