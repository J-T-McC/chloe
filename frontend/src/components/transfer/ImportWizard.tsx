import { FolderOpen } from "lucide-react";
import { useEffect, useState } from "react";
import { D, Explorer, Format, ImportMode, ImportOptions, ImportPreview, ObjectKind, Transfer, arr, errorText } from "../../api";
import { connById, useApp } from "../../store";
import { refreshFolder } from "../../tableActions";
import { track, useTransfer } from "./state";
import { Modal } from "../Modal";
import { Field, Check } from "../form";

type Step = "source" | "target" | "mapping" | "mode";

const MODES: Array<[ImportMode, string]> = [
  [ImportMode.ImportAppend, "Append: add records to the destination table"],
  [ImportMode.ImportUpsert, "Append/Update: update records that exist in the destination, add the rest"],
  [ImportMode.ImportSkipExisting, "Append without update: skip records that exist in the destination, add the rest"],
  [ImportMode.ImportCopy, "Copy: delete all records in the destination, then add the source records"],
  [ImportMode.ImportUpdate, "Update: update destination records that match source records"],
  [ImportMode.ImportDelete, "Delete: delete destination records that match source records"],
];

/** ImportPreview with Go nil slices normalised. */
type Preview = ImportPreview & { columns: string[]; rows: (string | null)[][]; types: string[]; sheets: string[] };

interface MapRow {
  source: string;
  include: boolean;
  target: string; // existing column, or new column name
  type: string; // new table only
  pk: boolean; // new table only
}

export function ImportWizard() {
  const target = useTransfer((s) => s.importing);
  const close = () => useTransfer.setState({ importing: undefined });
  const [step, setStep] = useState<Step>("source");
  const [path, setPath] = useState("");
  const [opts, setOpts] = useState<ImportOptions>({ format: Format.CSV, headerRow: 1, emptyAsNull: false, encoding: "utf-8" });
  const [preview, setPreview] = useState<Preview | null>(null);
  const [encodings, setEncodings] = useState<string[]>([]);
  const [tables, setTables] = useState<string[]>([]);
  const [table, setTable] = useState("");
  const [isNew, setIsNew] = useState(false);
  const [targetCols, setTargetCols] = useState<string[]>([]);
  const [mapping, setMapping] = useState<MapRow[]>([]);
  const [mode, setMode] = useState<ImportMode>(ImportMode.ImportAppend);
  const [transaction, setTransaction] = useState(true);
  const [continueOnError, setContinueOnError] = useState(false);
  const [noFK, setNoFK] = useState(false);
  const [batch, setBatch] = useState(500);
  const [error, setError] = useState("");

  const pg = connById(target?.connId)?.type === "postgres";

  useEffect(() => {
    if (!target) return;
    setStep("source");
    setPath("");
    setPreview(null);
    setError("");
    setTable(target.table?.name ?? "");
    setIsNew(false);
    Transfer.Encodings().then((e) => setEncodings(arr(e)));
    Explorer.ListObjects(target.connId, target.database, target.schema ?? "", ObjectKind.KindTable).then((o) => setTables(arr(o).map((x) => x.name)));
  }, [target]);

  // Re-read the preview whenever the file or reading options change.
  useEffect(() => {
    if (!target || !path) return;
    let live = true;
    const t = setTimeout(() => {
      Transfer.PreviewImport(target.connId, path, opts)
        .then((p) => {
          if (!live) return;
          setPreview({ ...p, columns: arr(p.columns), rows: arr(p.rows).map((r) => arr(r)), types: arr(p.types), sheets: arr(p.sheets) });
          setError("");
        })
        .catch((e) => {
          if (!live) return;
          setPreview(null);
          setError(errorText(e));
        });
    }, 150);
    return () => {
      live = false;
      clearTimeout(t);
    };
  }, [target, path, opts]);

  // Target table columns for mapping.
  useEffect(() => {
    if (!target || isNew || !table) return setTargetCols([]);
    Explorer.TableDetail(target.connId, { database: target.database, schema: target.schema, name: table, kind: ObjectKind.KindTable })
      .then((d) => setTargetCols(arr(d.columns).map((c) => c.name)))
      .catch(() => setTargetCols([]));
  }, [target, table, isNew]);

  // Build the default mapping when entering the mapping step.
  useEffect(() => {
    if (step !== "mapping" || !preview) return;
    setMapping(
      preview.columns.map((c, i) => {
        const match = targetCols.find((t) => t.toLowerCase() === c.toLowerCase());
        return { source: c, include: isNew || !!match, target: isNew ? sanitize(c) : (match ?? ""), type: preview.types[i] ?? "text", pk: isNew && i === 0 && /id$/i.test(c) };
      }),
    );
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [step]);

  if (!target) return null;

  const choose = async () => {
    const p = await Transfer.OpenFileDialog("Import from file", "Data files", "*.csv;*.tsv;*.txt;*.json;*.jsonl;*.ndjson;*.xlsx");
    if (!p) return;
    setPath(p);
    const lower = p.toLowerCase();
    const f = lower.endsWith(".xlsx") ? Format.XLSX : lower.endsWith(".jsonl") || lower.endsWith(".ndjson") ? Format.JSONL : lower.endsWith(".json") ? Format.$JSON : lower.endsWith(".tsv") || lower.endsWith(".txt") ? Format.TXT : Format.CSV;
    setOpts((o) => ({ ...o, format: f, sheet: undefined }));
    if (!table) setTable(sanitize(p.split(/[\\/]/).pop()!.replace(/\.[^.]+$/, "")));
  };

  const setMap = (i: number, patch: Partial<MapRow>) => setMapping((m) => m.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  const included = mapping.filter((m) => m.include && m.target);

  const start = async () => {
    setError("");
    const ref = { database: target.database, schema: target.schema, name: table, kind: ObjectKind.KindTable };
    let createTable: D.Table | undefined;
    if (isNew) {
      const pk = included.filter((m) => m.pk).map((m) => m.target);
      createTable = {
        database: target.database,
        schema: target.schema,
        name: table,
        columns: included.map((m) => {
          const [, type, len] = /^([^(]+)(?:\((.*)\))?$/.exec(m.type) ?? [m.type, m.type, ""];
          const [length, scale] = (len ?? "").split(",");
          return { name: m.target, type: type.trim(), length: length || undefined, scale: scale || undefined, nullable: !m.pk };
        }),
        primaryKey: pk.length ? { columns: pk } : undefined,
        options: {},
      } as D.Table;
    }
    try {
      await track(
        Transfer.Import(target.connId, {
          path,
          options: opts,
          target: ref,
          createTable,
          mapping: included.map((m) => ({ source: m.source, target: m.target })),
          mode: isNew ? ImportMode.ImportAppend : mode,
          batchSize: batch,
          continueOnError,
          transaction,
          disableFKChecks: noFK,
        }),
      );
      if (isNew) refreshFolder(target.connId, target.database, target.schema, ObjectKind.KindTable);
      useApp.setState((s) => ({ objectsVersion: s.objectsVersion + 1 }));
      close();
    } catch (e) {
      setError(errorText(e));
    }
  };

  const set = (p: Partial<ImportOptions>) => setOpts((o) => ({ ...o, ...p }));
  const steps: Step[] = ["source", "target", "mapping", "mode"];
  const next = steps[steps.indexOf(step) + 1];
  const prev = steps[steps.indexOf(step) - 1];
  const canNext = step === "source" ? !!preview?.columns.length : step === "target" ? !!table.trim() : step === "mapping" ? included.length > 0 : true;

  return (
    <Modal
      title={
        <>
          Import Wizard <span className="muted">— {connById(target.connId)?.name} › {[target.database, target.schema].filter(Boolean).join(".")}</span>
        </>
      }
      className="wizard wide"
      onClose={close}
    >
      <div className="wizard-steps">
        {steps.map((s, i) => (
          <span key={s} className={step === s ? "active" : ""}>
            {i + 1}. {{ source: "Source File", target: "Target Table", mapping: "Field Mappings", mode: "Import Mode" }[s]}
          </span>
        ))}
      </div>
      <div className="modal-body">
        {step === "source" && (
          <>
            <div className="row">
              <button onClick={choose}>
                <FolderOpen size={14} /> Choose File…
              </button>
              <span className="mono small">{path || "No file chosen"}</span>
            </div>
            {path && (
              <div className="source-opts">
                <label>
                  Format
                  <select value={opts.format} onChange={(e) => set({ format: e.target.value as Format })}>
                    <option value={Format.CSV}>CSV</option>
                    <option value={Format.TXT}>Text (tab)</option>
                    <option value={Format.$JSON}>JSON</option>
                    <option value={Format.JSONL}>JSON Lines</option>
                    <option value={Format.XLSX}>Excel</option>
                  </select>
                </label>
                {(opts.format === Format.CSV || opts.format === Format.TXT) && (
                  <>
                    <label>
                      Encoding
                      <select value={opts.encoding} onChange={(e) => set({ encoding: e.target.value })}>
                        {encodings.map((x) => (
                          <option key={x}>{x}</option>
                        ))}
                      </select>
                    </label>
                    <label>
                      Delimiter
                      <select value={opts.fieldDelimiter || (opts.format === Format.TXT ? "\t" : ",")} onChange={(e) => set({ fieldDelimiter: e.target.value })}>
                        <option value=",">Comma</option>
                        <option value={"\t"}>Tab</option>
                        <option value=";">Semicolon</option>
                        <option value="|">Pipe</option>
                      </select>
                    </label>
                  </>
                )}
                {opts.format === Format.XLSX && !!preview?.sheets?.length && (
                  <label>
                    Sheet
                    <select value={opts.sheet ?? preview.sheets[0]} onChange={(e) => set({ sheet: e.target.value })}>
                      {preview.sheets.map((x) => (
                        <option key={x}>{x}</option>
                      ))}
                    </select>
                  </label>
                )}
                {opts.format !== Format.$JSON && opts.format !== Format.JSONL && (
                  <>
                    <label>
                      Field name row
                      <input className="narrow" type="number" min={0} value={opts.headerRow} onChange={(e) => set({ headerRow: Number(e.target.value) })} />
                    </label>
                    <label>
                      First data row
                      <input className="narrow" type="number" min={1} value={opts.firstDataRow || opts.headerRow + 1} onChange={(e) => set({ firstDataRow: Number(e.target.value) })} />
                    </label>
                  </>
                )}
                <Check label="Empty string as NULL" checked={opts.emptyAsNull} onChange={(v) => set({ emptyAsNull: v })} />
              </div>
            )}
            {preview && <PreviewTable preview={preview} />}
          </>
        )}

        {step === "target" && (
          <div className="form">
            <Field>
              <label className="check">
                <input type="radio" checked={!isNew} onChange={() => (setIsNew(false), setTable(tables.includes(table) ? table : ""))} /> Existing table
              </label>
              <label className="check">
                <input type="radio" checked={isNew} onChange={() => setIsNew(true)} /> New table
              </label>
            </Field>
            <Field label="Target Table">
              {isNew ? (
                <input value={table} onChange={(e) => setTable(e.target.value)} />
              ) : (
                <select value={table} onChange={(e) => setTable(e.target.value)}>
                  <option value="">Choose a table…</option>
                  {tables.map((t) => (
                    <option key={t}>{t}</option>
                  ))}
                </select>
              )}
            </Field>
          </div>
        )}

        {step === "mapping" && (
          <>
            <div className="row">
              {!isNew && (
                <>
                  <button onClick={() => setMapping((m) => m.map((r) => ({ ...r, target: targetCols.find((t) => t.toLowerCase() === r.source.toLowerCase()) ?? "", include: targetCols.some((t) => t.toLowerCase() === r.source.toLowerCase()) })))}>
                    Match All By Name
                  </button>
                  <button onClick={() => setMapping((m) => m.map((r, i) => ({ ...r, target: targetCols[i] ?? "", include: i < targetCols.length })))}>Match All By Position</button>
                  <button onClick={() => setMapping((m) => m.map((r) => ({ ...r, target: "", include: false })))}>Clear All Matches</button>
                </>
              )}
              <span className="muted">{included.length} of {mapping.length} fields imported</span>
            </div>
            <table className="mapping">
              <thead>
                <tr>
                  <th />
                  <th>Source Field</th>
                  <th>Target Field</th>
                  {isNew && <th>Type</th>}
                  {isNew && <th>Primary Key</th>}
                </tr>
              </thead>
              <tbody>
                {mapping.map((r, i) => (
                  <tr key={i}>
                    <td>
                      <input type="checkbox" checked={r.include} onChange={(e) => setMap(i, { include: e.target.checked })} />
                    </td>
                    <td>{r.source}</td>
                    <td>
                      {isNew ? (
                        <input value={r.target} onChange={(e) => setMap(i, { target: e.target.value })} />
                      ) : (
                        <select value={r.target} onChange={(e) => setMap(i, { target: e.target.value, include: !!e.target.value })}>
                          <option value="">(skip)</option>
                          {targetCols.map((c) => (
                            <option key={c}>{c}</option>
                          ))}
                        </select>
                      )}
                    </td>
                    {isNew && (
                      <td>
                        <input className="mono" value={r.type} onChange={(e) => setMap(i, { type: e.target.value })} />
                      </td>
                    )}
                    {isNew && (
                      <td className="center">
                        <input type="checkbox" checked={r.pk} onChange={(e) => setMap(i, { pk: e.target.checked })} />
                      </td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          </>
        )}

        {step === "mode" && (
          <div className="mode-step">
            {isNew ? (
              <p className="hint">The new table is created, then every record is added.</p>
            ) : (
              <div className="modes">
                {MODES.map(([m, label]) => (
                  <label key={m} className="check">
                    <input type="radio" checked={mode === m} onChange={() => setMode(m)} /> {label}
                  </label>
                ))}
                {mode !== ImportMode.ImportAppend && mode !== ImportMode.ImportCopy && <p className="hint">Records are matched on the table's primary key, which must be mapped.</p>}
              </div>
            )}
            <hr />
            <Check label="Run in a single transaction (all or nothing)" checked={transaction} onChange={(v) => setTransaction(v)} />
            <Check label="Continue on error (skip bad records)" checked={continueOnError} disabled={transaction} onChange={(v) => setContinueOnError(v)} />
            {!pg && (
              <Check label="Ignore foreign key constraints" checked={noFK} onChange={(v) => setNoFK(v)} />
            )}
            <label className="row">
              Records per statement
              <input className="narrow" type="number" min={1} value={batch} onChange={(e) => setBatch(Math.max(1, Number(e.target.value)))} />
            </label>
          </div>
        )}
      </div>
      {error && <div className="status-line err">{error}</div>}
      <div className="modal-footer">
        <span className="spacer" />
        <button onClick={close}>Cancel</button>
        {prev && <button onClick={() => setStep(prev)}>Back</button>}
        {next ? (
          <button className="primary" disabled={!canNext} onClick={() => setStep(next)}>
            Next
          </button>
        ) : (
          <button className="primary" onClick={start}>
            Start
          </button>
        )}
      </div>
    </Modal>
  );
}

function PreviewTable({ preview }: { preview: Preview }) {
  return (
    <div className="import-preview">
      <table>
        <thead>
          <tr>
            {preview.columns.map((c, i) => (
              <th key={i}>
                {c}
                <span className="muted"> {preview.types[i]}</span>
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {arr(preview.rows)
            .slice(0, 50)
            .map((r, i) => (
              <tr key={i}>
                {preview.columns.map((_, j) => (
                  <td key={j}>{r?.[j] ?? <span className="null">NULL</span>}</td>
                ))}
              </tr>
            ))}
        </tbody>
      </table>
      <div className="muted small">Showing the first {Math.min(50, arr(preview.rows).length)} rows</div>
    </div>
  );
}

function sanitize(s: string) {
  return s.trim().replace(/[^A-Za-z0-9_]+/g, "_").replace(/^_+|_+$/g, "").toLowerCase() || "column";
}
