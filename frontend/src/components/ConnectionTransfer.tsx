// Export and import of connection profiles. Saved passwords travel only when a
// passphrase is given, encrypted with it.
import { FolderOpen } from "lucide-react";
import { useEffect, useState } from "react";
import { Connections, Transfer, errorText } from "../api";

const MIN_PASSPHRASE = 8;

export function ExportConnectionsDialog({
  ids,
  count,
  onClose,
  onDone,
}: {
  ids: string[];
  count: number;
  onClose(): void;
  onDone(msg: string): void;
}) {
  const [path, setPath] = useState("");
  const [withSecrets, setWithSecrets] = useState(false);
  const [pass, setPass] = useState("");
  const [again, setAgain] = useState("");
  const [error, setError] = useState("");

  const browse = async () => {
    const p = await Transfer.SaveFileDialog(
      "connections.chloe.json",
      "Chloe connections",
      "*.json",
    ).catch(() => "");
    if (p) setPath(p);
  };

  const start = async () => {
    setError("");
    if (withSecrets) {
      if (pass.length < MIN_PASSPHRASE)
        return setError(
          `The passphrase needs at least ${MIN_PASSPHRASE} characters.`,
        );
      if (pass !== again) return setError("The passphrases don't match.");
    }
    let target = path;
    if (!target)
      target = await Transfer.SaveFileDialog(
        "connections.chloe.json",
        "Chloe connections",
        "*.json",
      ).catch(() => "");
    if (!target) return;
    try {
      await Connections.Export(ids, target, withSecrets ? pass : "");
      onDone(
        `Exported ${count} connection(s) to ${target}${withSecrets ? " with encrypted passwords" : ""}`,
      );
    } catch (e) {
      setError(errorText(e));
    }
  };

  return (
    <div className="modal-backdrop" onMouseDown={onClose}>
      <div
        className="modal"
        onMouseDown={(e) => e.stopPropagation()}
        onKeyDown={(e) => (
          e.stopPropagation(),
          e.key === "Escape" && onClose()
        )}
      >
        <div className="modal-title">Export Connections</div>
        <div className="modal-body form">
          <div className="field">
            <label>File</label>
            <div className="field-input row">
              <input
                value={path}
                placeholder="(choose when exporting)"
                onChange={(e) => setPath(e.target.value)}
              />
              <button onClick={browse}>
                <FolderOpen size={13} />
              </button>
            </div>
          </div>
          <div className="field">
            <label />
            <div className="field-input">
              <label className="check">
                <input
                  type="checkbox"
                  checked={withSecrets}
                  onChange={(e) => setWithSecrets(e.target.checked)}
                />{" "}
                Include passwords
              </label>
            </div>
          </div>
          {withSecrets && (
            <>
              <div className="field">
                <label>Passphrase</label>
                <div className="field-input">
                  <input
                    type="password"
                    autoFocus
                    value={pass}
                    onChange={(e) => setPass(e.target.value)}
                  />
                </div>
              </div>
              <div className="field">
                <label>Confirm Passphrase</label>
                <div className="field-input">
                  <input
                    type="password"
                    value={again}
                    onChange={(e) => setAgain(e.target.value)}
                  />
                </div>
              </div>
              <p className="dialog-message muted">
                Passwords are encrypted with this passphrase. It's needed to
                import them and can't be recovered.
              </p>
            </>
          )}
        </div>
        {error && <div className="status-line err">{error}</div>}
        <div className="modal-footer">
          <span className="spacer" />
          <button onClick={onClose}>Cancel</button>
          <button className="primary" onClick={start}>
            Export
          </button>
        </div>
      </div>
    </div>
  );
}

export function ImportConnectionsDialog({
  onClose,
  onDone,
}: {
  onClose(): void;
  onDone(msg: string): void;
}) {
  const [path, setPath] = useState("");
  const [info, setInfo] = useState<{
    connections: number;
    encrypted: boolean;
  } | null>(null);
  const [pass, setPass] = useState("");
  const [error, setError] = useState("");

  // Read the file's header as the path changes, to know whether it needs a passphrase.
  useEffect(() => {
    setInfo(null);
    if (!path) return;
    const t = setTimeout(() => {
      Connections.InspectExport(path)
        .then((s) => (setInfo(s), setError("")))
        .catch((e) => setError(errorText(e)));
    }, 250);
    return () => clearTimeout(t);
  }, [path]);

  const browse = async () => {
    const p = await Transfer.OpenFileDialog(
      "Import Connections",
      "Chloe connections",
      "*.json",
    ).catch(() => "");
    if (p) setPath(p);
  };

  const start = async () => {
    setError("");
    try {
      const s = info ?? (await Connections.InspectExport(path));
      if (s.encrypted && !info) return setInfo(s); // show the passphrase field first
      const n = await Connections.Import(path, s.encrypted ? pass : "");
      onDone(
        `Imported ${n} connection(s)${s.encrypted && !pass ? " without passwords" : ""}`,
      );
    } catch (e) {
      setError(errorText(e));
    }
  };

  return (
    <div className="modal-backdrop" onMouseDown={onClose}>
      <div
        className="modal"
        onMouseDown={(e) => e.stopPropagation()}
        onKeyDown={(e) => (
          e.stopPropagation(),
          e.key === "Escape" && onClose()
        )}
      >
        <div className="modal-title">Import Connections</div>
        <div className="modal-body form">
          <div className="field">
            <label>File</label>
            <div className="field-input row">
              <input
                value={path}
                placeholder="Choose a Chloe connections file"
                onChange={(e) => setPath(e.target.value)}
              />
              <button onClick={browse}>
                <FolderOpen size={13} />
              </button>
            </div>
          </div>
          {info && (
            <p className="dialog-message muted">
              {info.connections} connection(s)
              {info.encrypted
                ? ", with passwords encrypted by a passphrase."
                : ", without passwords."}
            </p>
          )}
          {info?.encrypted && (
            <div className="field">
              <label>Passphrase</label>
              <div className="field-input">
                <input
                  type="password"
                  autoFocus
                  value={pass}
                  placeholder="Leave empty to import without passwords"
                  onChange={(e) => setPass(e.target.value)}
                />
              </div>
            </div>
          )}
        </div>
        {error && <div className="status-line err">{error}</div>}
        <div className="modal-footer">
          <span className="spacer" />
          <button onClick={onClose}>Cancel</button>
          <button className="primary" disabled={!path} onClick={start}>
            Import
          </button>
        </div>
      </div>
    </div>
  );
}
