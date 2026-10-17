// Create Chart from a data grid: pick a workspace (or name a new one); Chloe adds a
// data source for the table or query and an empty chart on it, and opens the chart.
import { useEffect, useState } from "react";
import { B, BI, arr, errorText } from "../../api";
import { Modal, ModalFooter } from "../Modal";
import { biChanged, openObject, useCreateChart } from "./actions";
import { Field } from "../form";

const NEW = "\u0000new";

export function CreateChartDialog() {
  const src = useCreateChart((s) => s.src);
  const [items, setItems] = useState<B.Entry[]>([]);
  const [target, setTarget] = useState(NEW);
  const [name, setName] = useState("My Reports");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!src) return;
    setError("");
    BI.List().then((xs) => {
      const list = arr(xs);
      setItems(list);
      setTarget(list[0]?.path ?? NEW);
    });
  }, [src]);
  if (!src) return null;
  const close = () => useCreateChart.setState({ src: null });
  const ok = async () => {
    setBusy(true);
    setError("");
    try {
      const r = await BI.CreateChart(target === NEW ? "" : target, target === NEW ? name.trim() : "", src);
      biChanged(r.path);
      close();
      openObject(r.path, "chart", r.chartId, "");
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal title="Create Chart" className="small" onClose={close} onKeyDown={(e) => e.key === "Enter" && !busy && ok()}>
      <div className="modal-body form">
        <p className="dialog-message">A data source for {src.table ? `“${src.table}”` : "this query"} and a new chart on it will be added to a BI workspace.</p>
        <Field label="Workspace">
          <select aria-label="Workspace" value={target} onChange={(e) => setTarget(e.target.value)} autoFocus>
            {items.map((w) => (
              <option key={w.path} value={w.path}>
                {w.name}
              </option>
            ))}
            <option value={NEW}>New workspace…</option>
          </select>
        </Field>
        {target === NEW && (
          <Field label="Name">
            <input aria-label="New workspace name" value={name} onChange={(e) => setName(e.target.value)} />
          </Field>
        )}
        {error && <div className="error-banner">{error}</div>}
      </div>
      <ModalFooter onCancel={close} onOk={ok} okLabel={busy ? "Creating…" : "Create Chart"} okDisabled={busy || (target === NEW && !name.trim())} />
    </Modal>
  );
}
