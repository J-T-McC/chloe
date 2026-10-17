// Export a collection to JSON / JSON Lines / CSV, or import such a file into one.
import { FolderOpen } from "lucide-react";
import { useState } from "react";
import { create } from "zustand";
import { Mongo, Transfer, errorText } from "../../api";
import { track } from "../transfer/state";
import { collectionsChanged } from "./actions";
import { Modal, ModalFooter } from "../Modal";
import { Field, Check } from "../form";

interface Request {
  kind: "export" | "import";
  connId: string;
  db: string;
  coll: string;
  /** export: start from the viewer's filter and sort */
  filter?: string;
  sort?: string;
}

const useMongoTransfer = create<{ req: Request | null }>(() => ({ req: null }));

export const exportCollection = (connId: string, db: string, coll: string, filter = "", sort = "") =>
  useMongoTransfer.setState({ req: { kind: "export", connId, db, coll, filter, sort } });
export const importCollection = (connId: string, db: string, coll: string) => useMongoTransfer.setState({ req: { kind: "import", connId, db, coll } });

export function MongoTransferHost() {
  const req = useMongoTransfer((s) => s.req);
  if (!req) return null;
  const close = () => useMongoTransfer.setState({ req: null });
  return req.kind === "export" ? <ExportDialog req={req} onClose={close} /> : <ImportDialog req={req} onClose={close} />;
}

const EXT: Record<string, string> = { json: "json", jsonl: "jsonl", csv: "csv" };

function ExportDialog({ req, onClose }: { req: Request; onClose(): void }) {
  const [format, setFormat] = useState("json");
  const [canonical, setCanonical] = useState(false);
  const [filter, setFilter] = useState(req.filter ?? "");
  const [sort, setSort] = useState(req.sort ?? "");
  const [limit, setLimit] = useState("");
  const [fields, setFields] = useState("");
  const [path, setPath] = useState("");
  const [error, setError] = useState("");

  const browse = async () => {
    const p = await Transfer.SaveFileDialog(`${req.coll}.${EXT[format]}`, format.toUpperCase(), `*.${EXT[format]}`).catch(() => "");
    if (p) setPath(p);
  };
  const start = async () => {
    let target = path;
    if (!target) {
      target = await Transfer.SaveFileDialog(`${req.coll}.${EXT[format]}`, format.toUpperCase(), `*.${EXT[format]}`).catch(() => "");
      if (!target) return;
    }
    try {
      await track(
        Mongo.Export(req.connId, req.db, req.coll, {
          format,
          canonical,
          filter,
          sort,
          projection: "",
          limit: Number(limit) || 0,
          fields: format === "csv" ? fields.split(",").map((f) => f.trim()).filter(Boolean) : [],
        }, target),
      );
      onClose();
    } catch (e) {
      setError(errorText(e));
    }
  };

  return (
    <Modal title={`Export — ${req.coll}`} onClose={onClose}>
      <div className="modal-body form">
        <Field label="Format">
          <select value={format} onChange={(e) => setFormat(e.target.value)}>
            <option value="json">JSON (array)</option>
            <option value="jsonl">JSON Lines (mongoexport)</option>
            <option value="csv">CSV</option>
          </select>
        </Field>
        {format !== "csv" ? (
          <Field label="Extended JSON">
            <select value={canonical ? "canonical" : "relaxed"} onChange={(e) => setCanonical(e.target.value === "canonical")}>
              <option value="relaxed">Relaxed (readable numbers and dates)</option>
              <option value="canonical">Canonical (every type kept)</option>
            </select>
          </Field>
        ) : (
          <Field label="Fields">
            <input value={fields} spellCheck={false} placeholder="(all, from the first 1,000 documents) e.g. _id, email, address.city" onChange={(e) => setFields(e.target.value)} />
          </Field>
        )}
        <Field label="Filter">
          <input value={filter} spellCheck={false} placeholder="{ } (all documents)" onChange={(e) => setFilter(e.target.value)} />
        </Field>
        <Field label="Sort">
          <input value={sort} spellCheck={false} placeholder="{ _id: 1 }" onChange={(e) => setSort(e.target.value)} />
        </Field>
        <Field label="Limit">
          <input className="narrow" value={limit} placeholder="(none)" onChange={(e) => setLimit(e.target.value.replace(/\D/g, ""))} />
        </Field>
        <div className="field">
          <label>File</label>
          <div className="field-input row">
            <input value={path} placeholder="(choose when exporting)" onChange={(e) => setPath(e.target.value)} />
            <button onClick={browse}>
              <FolderOpen size={13} />
            </button>
          </div>
        </div>
      </div>
      {error && <div className="status-line err">{error}</div>}
      <ModalFooter onCancel={onClose} onOk={start} okLabel="Export" />
    </Modal>
  );
}

function ImportDialog({ req, onClose }: { req: Request; onClose(): void }) {
  const [path, setPath] = useState("");
  const [format, setFormat] = useState("json");
  const [mode, setMode] = useState("append");
  const [infer, setInfer] = useState(true);
  const [skipEmpty, setSkipEmpty] = useState(true);
  const [stop, setStop] = useState(false);
  const [error, setError] = useState("");

  const choose = async () => {
    const p = await Transfer.OpenFileDialog("Import into " + req.coll, "JSON or CSV", "*.json;*.jsonl;*.ndjson;*.csv").catch(() => "");
    if (!p) return;
    setPath(p);
    setFormat(/\.csv$/i.test(p) ? "csv" : "json");
  };
  const start = async () => {
    if (!path) return setError("Choose a file to import.");
    try {
      const job = await track(Mongo.Import(req.connId, req.db, req.coll, { format, mode, inferTypes: infer, skipEmpty, stopOnError: stop }, path));
      onClose();
      // Refresh counts once the job has had a moment to finish small files.
      setTimeout(() => collectionsChanged(req.connId, req.db), job.status === "running" ? 1500 : 0);
    } catch (e) {
      setError(errorText(e));
    }
  };

  return (
    <Modal title={`Import — ${req.coll}`} onClose={onClose}>
      <div className="modal-body form">
        <div className="field">
          <label>File</label>
          <div className="field-input row">
            <input value={path} placeholder="Choose a .json, .jsonl or .csv file" onChange={(e) => setPath(e.target.value)} />
            <button onClick={choose}>
              <FolderOpen size={13} />
            </button>
          </div>
        </div>
        <Field label="Format">
          <select value={format} onChange={(e) => setFormat(e.target.value)}>
            <option value="json">JSON (an array or one document per line; Extended JSON)</option>
            <option value="csv">CSV (header row; dotted names nest)</option>
          </select>
        </Field>
        <Field label="Import Mode">
          <select value={mode} onChange={(e) => setMode(e.target.value)}>
            <option value="append">Append: add the documents</option>
            <option value="upsert">Append/Update: replace documents with the same _id</option>
            <option value="copy">Copy: delete every document first</option>
          </select>
        </Field>
        {format === "csv" && (
          <>
            <Field>
              <Check label="Read numbers, booleans and JSON values" checked={infer} onChange={(v) => setInfer(v)} />
            </Field>
            <Field>
              <Check label="Leave out empty values" checked={skipEmpty} onChange={(v) => setSkipEmpty(v)} />
            </Field>
          </>
        )}
        <Field>
          <Check label="Stop at the first error" checked={stop} onChange={(v) => setStop(v)} />
        </Field>
      </div>
      {error && <div className="status-line err">{error}</div>}
      <ModalFooter onCancel={onClose} onOk={start} okLabel="Import" />
    </Modal>
  );
}
