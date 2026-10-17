// Promise-based prompt/confirm dialogs. The system webview's window.prompt and
// window.confirm aren't reliable, so the app renders its own.
import { useEffect, useRef, useState } from "react";
import { create } from "zustand";
import { Modal } from "./Modal";

interface Request {
  kind: "prompt" | "confirm" | "alert" | "choose";
  /** choose: button labels, the last one the default */
  buttons?: string[];
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

/** Ask with several buttons; resolves to the chosen label, or null when dismissed. */
export const choose = (title: string, opts: { message?: string; buttons: string[] }) =>
  push({ kind: "choose", title, ...opts }) as Promise<string | null>;

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
  const buttons = req.buttons ?? [];
  const ok = () => done(req.kind === "prompt" ? value : req.kind === "choose" ? buttons[buttons.length - 1] : true);
  const cancel = () => done(req.kind === "prompt" || req.kind === "choose" ? null : false);

  return (
    <Modal title={req.title} className="small" onClose={cancel} onKeyDown={(e) => e.key === "Enter" && ok()}>
      <div className="modal-body">
        {req.message && <p className="dialog-message">{req.message}</p>}
        {req.kind === "prompt" && (
          <input ref={input} autoFocus type={req.password ? "password" : "text"} value={value} onChange={(e) => setValue(e.target.value)} />
        )}
      </div>
      <div className="modal-footer">
        {req.kind === "choose" ? (
          [buttons.length <= 2 && <span key="" className="spacer" />, ...buttons.map((b, i) => (
            <span key={b} style={{ display: "contents" }}>
              <button className={i === buttons.length - 1 ? "primary" : ""} autoFocus={i === buttons.length - 1} onClick={() => done(b)}>
                {b}
              </button>
              {i === 0 && buttons.length > 2 && <span className="spacer" />}
            </span>
          ))]
        ) : (
          <>
            <span className="spacer" />
            {req.kind !== "alert" && <button onClick={cancel}>Cancel</button>}
            <button className={req.danger ? "danger" : "primary"} onClick={ok} autoFocus={req.kind !== "prompt"}>
              {req.okLabel ?? "OK"}
            </button>
          </>
        )}
      </div>
    </Modal>
  );
}
