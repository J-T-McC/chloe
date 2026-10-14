// Promise-based prompt/confirm dialogs. The system webview's window.prompt and
// window.confirm aren't reliable, so the app renders its own.
import { useEffect, useRef, useState } from "react";
import { create } from "zustand";

interface Request {
  kind: "prompt" | "confirm" | "alert";
  title: string;
  message?: string;
  defaultValue?: string;
  password?: boolean;
  danger?: boolean;
  okLabel?: string;
  resolve(value: string | boolean | null): void;
}

const useDialogs = create<{ queue: Request[] }>(() => ({ queue: [] }));

function push(r: Omit<Request, "resolve">) {
  return new Promise<string | boolean | null>((resolve) => {
    useDialogs.setState((s) => ({ queue: [...s.queue, { ...r, resolve }] }));
  });
}

export const prompt = (title: string, opts: { message?: string; defaultValue?: string; password?: boolean } = {}) =>
  push({ kind: "prompt", title, ...opts }) as Promise<string | null>;

export const confirm = (title: string, opts: { message?: string; danger?: boolean; okLabel?: string } = {}) =>
  push({ kind: "confirm", title, ...opts }) as Promise<boolean>;

export const alert = (title: string, message?: string) => push({ kind: "alert", title, message }) as Promise<boolean>;

export function DialogHost() {
  const req = useDialogs((s) => s.queue[0]);
  const [value, setValue] = useState("");
  const input = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (req) {
      setValue(req.defaultValue ?? "");
      setTimeout(() => input.current?.select(), 0);
    }
  }, [req]);

  if (!req) return null;
  const done = (v: string | boolean | null) => {
    useDialogs.setState((s) => ({ queue: s.queue.slice(1) }));
    req.resolve(v);
  };
  const ok = () => done(req.kind === "prompt" ? value : true);
  const cancel = () => done(req.kind === "prompt" ? null : false);

  return (
    <div className="modal-backdrop" onMouseDown={cancel}>
      <div
        className="modal small"
        onMouseDown={(e) => e.stopPropagation()}
        onKeyDown={(e) => {
          if (e.key === "Escape") cancel();
          if (e.key === "Enter") ok();
        }}
      >
        <div className="modal-title">{req.title}</div>
        <div className="modal-body">
          {req.message && <p className="dialog-message">{req.message}</p>}
          {req.kind === "prompt" && (
            <input ref={input} autoFocus type={req.password ? "password" : "text"} value={value} onChange={(e) => setValue(e.target.value)} />
          )}
        </div>
        <div className="modal-footer">
          <span className="spacer" />
          {req.kind !== "alert" && <button onClick={cancel}>Cancel</button>}
          <button className={req.danger ? "danger" : "primary"} onClick={ok} autoFocus={req.kind !== "prompt"}>
            {req.okLabel ?? "OK"}
          </button>
        </div>
      </div>
    </div>
  );
}
