import { FolderOpen } from "lucide-react";
import { useEffect, useState } from "react";
import { Transfer, arr, errorText } from "../../api";
import { connById, useApp } from "../../store";
import { track, useTransfer } from "./state";

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
    <div className="modal-backdrop" onMouseDown={close}>
      <div className="modal" onMouseDown={(e) => e.stopPropagation()} onKeyDown={(e) => e.key === "Escape" && close()}>
        <div className="modal-title">Execute SQL File</div>
        <div className="modal-body form">
          <div className="field">
            <label>Server</label>
            <div className="field-input">{connById(t.connId)?.name}</div>
          </div>
          <div className="field">
            <label>Database</label>
            <div className="field-input">{[t.database, t.schema].filter(Boolean).join(" › ") || <span className="muted">None (the file chooses with CREATE DATABASE / USE)</span>}</div>
          </div>
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
          <div className="field">
            <label>Encoding</label>
            <div className="field-input">
              <select value={encoding} onChange={(e) => setEncoding(e.target.value)}>
                {encodings.map((x) => (
                  <option key={x}>{x}</option>
                ))}
              </select>
            </div>
          </div>
          <div className="field">
            <label />
            <div className="field-input">
              <label className="check">
                <input type="checkbox" checked={continueOnError} onChange={(e) => setContinueOnError(e.target.checked)} /> Continue on error
              </label>
            </div>
          </div>
        </div>
        {error && <div className="status-line err">{error}</div>}
        <div className="modal-footer">
          <span className="spacer" />
          <button onClick={close}>Cancel</button>
          <button className="primary" disabled={!path} onClick={start}>
            Start
          </button>
        </div>
      </div>
    </div>
  );
}
