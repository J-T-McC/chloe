import { useEffect, useMemo, useState } from "react";
import { ExportOptions, Explorer, Format, ObjectKind, ObjectRef, Transfer, arr, errorText } from "../../api";
import { connById } from "../../store";
import { track, useTransfer } from "./state";
import { Modal } from "../Modal";
import { Field, Check } from "../form";
import { WizardSteps } from "./wizard";

const FORMATS: Array<{ f: Format; label: string; desc: string }> = [
  { f: Format.CSV, label: "CSV", desc: "Comma separated values (.csv)" },
  { f: Format.TXT, label: "Text", desc: "Tab separated values (.tsv)" },
  { f: Format.XLSX, label: "Excel", desc: "Excel workbook (.xlsx)" },
  { f: Format.$JSON, label: "JSON", desc: "Array of objects (.json)" },
  { f: Format.JSONL, label: "JSON Lines", desc: "One object per line (.jsonl)" },
  { f: Format.SQL, label: "SQL", desc: "INSERT statements (.sql)" },
  { f: Format.Markdown, label: "Markdown", desc: "Table for docs and notes (.md)" },
];

const ext = (f: Format) => (f === Format.TXT ? "tsv" : f);

type Step = "format" | "tables" | "options";

export function ExportWizard() {
  const target = useTransfer((s) => s.exporting);
  const close = () => useTransfer.setState({ exporting: undefined });
  const [step, setStep] = useState<Step>("format");
  const [format, setFormat] = useState<Format>(Format.CSV);
  const [allTables, setAllTables] = useState<string[]>([]);
  const [picked, setPicked] = useState<string[]>([]);
  const [columns, setColumns] = useState<Record<string, string[]>>({}); // selected columns per table
  const [allCols, setAllCols] = useState<Record<string, string[]>>({});
  const [where, setWhere] = useState("");
  const [opts, setOpts] = useState<ExportOptions>({ format: Format.CSV, includeHeaders: true, fieldDelimiter: "", recordDelimiter: "\n", textQualifier: '"', nullText: "", rowsPerInsert: 100 });
  const [error, setError] = useState("");
  const isQuery = !!target?.sql;

  useEffect(() => {
    if (!target) return;
    setStep("format");
    setError("");
    setWhere("");
    setPicked(target.tables.map((t) => t.name));
    setColumns({});
    if (!isQuery) {
      Explorer.ListObjects(target.connId, target.database, target.schema ?? "", ObjectKind.KindTable)
        .then((o) => setAllTables(arr(o).map((x) => x.name)))
        .catch((e) => setError(errorText(e)));
    }
  }, [target, isQuery]);

  const loadCols = async (name: string) => {
    if (!target || allCols[name]) return;
    const d = await Explorer.TableDetail(target.connId, { database: target.database, schema: target.schema, name, kind: ObjectKind.KindTable });
    setAllCols((c) => ({ ...c, [name]: arr(d.columns).map((x) => x.name) }));
  };

  const options = useMemo<ExportOptions>(() => ({ ...opts, format }), [opts, format]);
  if (!target) return null;
  const conn = connById(target.connId);

  const start = async () => {
    setError("");
    try {
      if (isQuery) {
        const path = await Transfer.SaveFileDialog(`${target.name || "query"}.${ext(format)}`, format.toUpperCase(), `*.${ext(format)}`);
        if (!path) return;
        await track(Transfer.Export(target.connId, { source: { sql: target.sql, database: target.database, schema: target.schema }, options: { ...options, tableName: options.tableName || target.name || "query_result" }, path }));
      } else if (picked.length === 1) {
        const name = picked[0];
        const path = await Transfer.SaveFileDialog(`${name}.${ext(format)}`, format.toUpperCase(), `*.${ext(format)}`);
        if (!path) return;
        await track(Transfer.Export(target.connId, exportReq(name, path)));
      } else {
        const dir = await Transfer.ChooseFolder("Export to folder");
        if (!dir) return;
        for (const name of picked) await track(Transfer.Export(target.connId, exportReq(name, `${dir}/${name}.${ext(format)}`)));
      }
      close();
    } catch (e) {
      setError(errorText(e));
    }
  };

  const exportReq = (name: string, path: string) => {
    const ref: ObjectRef = { database: target.database, schema: target.schema, name, kind: ObjectKind.KindTable };
    return { source: { ref, columns: columns[name] ?? [], where: picked.length === 1 ? where : "" }, options, path };
  };

  const set = (p: Partial<ExportOptions>) => setOpts((o) => ({ ...o, ...p }));
  const delimited = format === Format.CSV || format === Format.TXT;

  return (
    <Modal
      title={
        <>
          Export Wizard <span className="muted">— {isQuery ? "query result" : `${conn?.name} › ${[target.database, target.schema].filter(Boolean).join(".")}`}</span>
        </>
      }
      className="wizard"
      onClose={close}
    >
      <WizardSteps step={step} steps={(isQuery ? (["format", "options"] as Step[]) : (["format", "tables", "options"] as Step[])).map((s) => [s, { format: "Format", tables: "Tables & Columns", options: "Options" }[s]])} />
      <div className="modal-body">
        {step === "format" && (
          <div className="format-list">
            {FORMATS.map((x) => (
              <label key={x.f} className={"format-item" + (format === x.f ? " on" : "")}>
                <input type="radio" checked={format === x.f} onChange={() => setFormat(x.f)} />
                <strong>{x.label}</strong>
                <span className="muted">{x.desc}</span>
              </label>
            ))}
          </div>
        )}

        {step === "tables" && (
          <div className="export-tables">
            <div className="row">
              <button onClick={() => setPicked(allTables)}>Select All</button>
              <button onClick={() => setPicked([])}>Deselect All</button>
              <span className="muted">{picked.length} selected</span>
            </div>
            <div className="table-pick">
              {allTables.map((t) => (
                <div key={t} className="table-pick-row">
                  <label className="check">
                    <input type="checkbox" checked={picked.includes(t)} onChange={(e) => setPicked((p) => (e.target.checked ? [...p, t] : p.filter((x) => x !== t)))} /> {t}
                  </label>
                  {picked.includes(t) && (
                    <details onToggle={(e) => (e.target as HTMLDetailsElement).open && loadCols(t)}>
                      <summary className="muted">{columns[t]?.length ? `${columns[t].length} columns` : "all columns"}</summary>
                      <div className="col-pick">
                        {(allCols[t] ?? []).map((c) => {
                          const sel = columns[t] ?? allCols[t] ?? [];
                          return (
                            <label key={c} className="check">
                              <input
                                type="checkbox"
                                checked={sel.includes(c)}
                                onChange={(e) => setColumns((m) => ({ ...m, [t]: e.target.checked ? [...sel, c] : sel.filter((x) => x !== c) }))}
                              />{" "}
                              {c}
                            </label>
                          );
                        })}
                      </div>
                    </details>
                  )}
                </div>
              ))}
            </div>
            {picked.length === 1 && (
              <div className="row where-row">
                <span className="kw">WHERE</span>
                <input className="mono" placeholder="(optional filter)" value={where} onChange={(e) => setWhere(e.target.value)} />
              </div>
            )}
          </div>
        )}

        {step === "options" && (
          <div className="form">
            {format !== Format.SQL && format !== Format.$JSON && format !== Format.JSONL && format !== Format.Markdown && (
              <Field label="">
                <Check label="Include column titles" checked={opts.includeHeaders} onChange={(v) => set({ includeHeaders: v })} />
              </Field>
            )}
            {delimited && (
              <>
                <Field label="Field Delimiter">
                  <select value={opts.fieldDelimiter || (format === Format.TXT ? "\t" : ",")} onChange={(e) => set({ fieldDelimiter: e.target.value })}>
                    <option value=",">Comma</option>
                    <option value={"\t"}>Tab</option>
                    <option value=";">Semicolon</option>
                    <option value="|">Pipe</option>
                    <option value=" ">Space</option>
                  </select>
                </Field>
                <Field label="Record Delimiter">
                  <select value={opts.recordDelimiter} onChange={(e) => set({ recordDelimiter: e.target.value })}>
                    <option value={"\n"}>LF</option>
                    <option value={"\r\n"}>CRLF</option>
                    <option value={"\r"}>CR</option>
                  </select>
                </Field>
                <Field label="Text Qualifier">
                  <select value={opts.textQualifier} onChange={(e) => set({ textQualifier: e.target.value })}>
                    <option value={'"'}>" (double quote)</option>
                    <option value="'">' (single quote)</option>
                    <option value="none">None</option>
                  </select>
                </Field>
                <Field label="NULL as">
                  <input value={opts.nullText} placeholder="(empty)" onChange={(e) => set({ nullText: e.target.value })} />
                </Field>
              </>
            )}
            {format === Format.SQL && (
              <>
                <Field label="Target Table Name">
                  <input value={opts.tableName ?? ""} placeholder={isQuery ? "query_result" : "(same as source)"} onChange={(e) => set({ tableName: e.target.value })} />
                </Field>
                <Field label="Rows per INSERT">
                  <input className="narrow" type="number" min={1} value={opts.rowsPerInsert} onChange={(e) => set({ rowsPerInsert: Number(e.target.value) })} />
                </Field>
              </>
            )}
            {format === Format.XLSX && <p className="hint">Excel sheets hold up to 1,048,576 rows; numbers keep their type, long decimals stay text to preserve precision.</p>}
            <p className="hint">
              {isQuery ? "The query runs again and streams straight to the file." : picked.length > 1 ? `Each table is written to its own file in the folder you choose.` : "Rows stream straight from the server, so large tables don't use extra memory."}
            </p>
          </div>
        )}
      </div>
      {error && <div className="status-line err">{error}</div>}
      <div className="modal-footer">
        <span className="spacer" />
        <button onClick={close}>Cancel</button>
        {step !== "format" && <button onClick={() => setStep(step === "options" && !isQuery ? "tables" : "format")}>Back</button>}
        {step !== "options" ? (
          <button className="primary" disabled={step === "tables" && !picked.length} onClick={() => setStep(step === "format" && !isQuery ? "tables" : "options")}>
            Next
          </button>
        ) : (
          <button className="primary" onClick={start}>
            Export…
          </button>
        )}
      </div>
    </Modal>
  );
}

