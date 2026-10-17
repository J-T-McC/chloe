import { FolderOpen } from "lucide-react";
import { useEffect, useState } from "react";
import { Transfer, arr, errorText } from "../../api";
import { connById, useApp } from "../../store";
import { Modal, ModalFooter } from "../Modal";
import { track, useTransfer } from "./state";
import { Field, Check } from "../form";

/** Execute SQL File: runs every statement of a script on a database. */
export function ExecuteFileDialog() {
  const t = useTransfer((s) => s.executing);
  const close = () => useTransfer.setState({ executing: undefined });
  const [path, setPath] = useState("");
  const [encoding, setEncoding] = useState("utf-8");
  const [encodings, setEncodings] = useState<string[]>([]);
  const [continueOnError, setContinueOnError] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!t) return;
    setPath("");
    setError("");
    Transfer.Encodings().then((e) => setEncodings(arr(e)));
  }, [t]);
  if (!t) return null;

  const start = async () => {
    try {
      await track(Transfer.ExecuteFile(t.connId, { path, database: t.database, schema: t.schema, encoding, continueOnError }));
      useApp.setState((s) => ({ objectsVersion: s.objectsVersion + 1 }));
      close();
    } catch (e) {
      setError(errorText(e));
    }
  };

  return (
    <Modal title="Execute SQL File" onClose={close}>
      <div className="modal-body form">
        <Field label="Server">{connById(t.connId)?.name}</Field>
        <Field label="Database">{[t.database, t.schema].filter(Boolean).join(" › ") || <span className="muted">None (the file chooses with CREATE DATABASE / USE)</span>}</Field>
        <div className="field">
          <label>File</label>
          <div className="field-input row">
            <input value={path} readOnly placeholder="Choose a .sql file" />
            <button
              onClick={async () => {
                const p = await Transfer.OpenFileDialog("Execute SQL File", "SQL files", "*.sql");
                if (p) setPath(p);
              }}
            >
              <FolderOpen size={13} />
            </button>
          </div>
        </div>
        <Field label="Encoding">
          <select value={encoding} onChange={(e) => setEncoding(e.target.value)}>
            {encodings.map((x) => (
              <option key={x}>{x}</option>
            ))}
          </select>
        </Field>
        <Field>
          <Check label="Continue on error" checked={continueOnError} onChange={(v) => setContinueOnError(v)} />
        </Field>
      </div>
      {error && <div className="status-line err">{error}</div>}
      <ModalFooter onCancel={close} onOk={start} okLabel="Start" okDisabled={!path} />
    </Modal>
  );
}
