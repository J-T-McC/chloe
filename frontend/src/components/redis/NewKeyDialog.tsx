// New Key: name, type, TTL and the first element (Redis has no empty collections).
import { useState } from "react";
import { R, Redis, errorText } from "../../api";
import { Modal, ModalFooter } from "../Modal";
import { TYPES, TYPE_LABEL, text } from "../../redis/keys";

export function NewKeyDialog({ connId, db, prefix, onClose }: { connId: string; db: number; prefix: string; onClose(created?: R.Key): void }) {
  const [name, setName] = useState(prefix);
  const [type, setType] = useState<string>("string");
  const [field, setField] = useState("");
  const [value, setValue] = useState("");
  const [score, setScore] = useState("0");
  const [ttl, setTtl] = useState("");
  const [error, setError] = useState("");

  const ok = async () => {
    if (!name.trim()) return setError("Enter a key name.");
    if ((type === "hash" || type === "stream") && !field.trim()) return setError("Enter a field name.");
    if (type === "zset" && Number.isNaN(Number(score))) return setError("Enter a numeric score.");
    const nk: R.NewKey = { name: text(name), type, value: text(value), field: text(field.trim()), score: Number(score) || 0, ttl: Number(ttl) > 0 ? Number(ttl) : 0 };
    try {
      await Redis.CreateKey(connId, db, nk);
      onClose({ name: nk.name, type, ttl: nk.ttl > 0 ? nk.ttl : -1 });
    } catch (e) {
      setError(errorText(e));
    }
  };

  const valueLabel = type === "set" || type === "zset" ? "Member" : type === "list" ? "First Item" : "Value";
  return (
    <Modal title={`New Key — db${db}`} onClose={() => onClose()}>
      <div className="modal-body form">
        <div className="field">
          <label>Key Name</label>
          <div className="field-input">
            <input autoFocus value={name} onChange={(e) => setName(e.target.value)} onKeyDown={(e) => e.key === "Enter" && ok()} />
          </div>
        </div>
        <div className="field">
          <label>Type</label>
          <div className="field-input">
            <select value={type} onChange={(e) => setType(e.target.value)}>
              {TYPES.map((t) => (
                <option key={t} value={t}>
                  {TYPE_LABEL[t]}
                </option>
              ))}
            </select>
          </div>
        </div>
        <div className="field">
          <label>TTL (seconds)</label>
          <div className="field-input">
            <input type="number" min={1} placeholder="No limit" value={ttl} onChange={(e) => setTtl(e.target.value)} />
          </div>
        </div>
        {(type === "hash" || type === "stream") && (
          <div className="field">
            <label>Field</label>
            <div className="field-input">
              <input value={field} onChange={(e) => setField(e.target.value)} />
            </div>
          </div>
        )}
        {type === "zset" && (
          <div className="field">
            <label>Score</label>
            <div className="field-input">
              <input value={score} onChange={(e) => setScore(e.target.value)} />
            </div>
          </div>
        )}
        <div className="field">
          <label>{valueLabel}</label>
          <div className="field-input">
            <textarea rows={type === "string" ? 6 : 3} value={value} onChange={(e) => setValue(e.target.value)} />
          </div>
        </div>
      </div>
      {error && <div className="status-line err">{error}</div>}
      <ModalFooter onCancel={() => onClose()} onOk={ok} />
    </Modal>
  );
}
