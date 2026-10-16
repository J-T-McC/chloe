// Asks whether to trust an SSH server Chloe hasn't connected to before. The tunnel waits
// until the user answers here (or two minutes pass, which counts as Cancel).
import { useEffect, useState } from "react";
import { create } from "zustand";
import { Events, HostKeys, arr, type HostKeyPrompt } from "../api";

const useHostKeys = create<{ queue: HostKeyPrompt[] }>(() => ({ queue: [] }));

const drop = (id: string) => useHostKeys.setState((s) => ({ queue: s.queue.filter((p) => p.id !== id) }));

/** ssh-ed25519 → ED25519, ecdsa-sha2-nistp256 → ECDSA, ssh-rsa → RSA. */
const keyName = (t: string) => (t.startsWith("ecdsa-") ? "ECDSA" : t.replace(/^ssh-/, "").toUpperCase());

export function HostKeyHost() {
  const p = useHostKeys((s) => s.queue[0]);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    HostKeys.Pending()
      .then((q) => useHostKeys.setState({ queue: arr(q) }))
      .catch(() => {});
    const off1 = Events.On("ssh:hostkey", (ev) => useHostKeys.setState((s) => (s.queue.some((q) => q.id === ev.data.id) ? s : { queue: [...s.queue, ev.data] })));
    const off2 = Events.On("ssh:hostkey-done", (ev) => drop(ev.data));
    return () => {
      off1();
      off2();
    };
  }, []);

  if (!p) return null;
  const answer = async (trust: boolean) => {
    setBusy(true);
    await HostKeys.Answer(p.id, trust).catch(() => {});
    drop(p.id);
    setBusy(false);
  };

  return (
    <div className="modal-backdrop">
      <div className="modal small host-key" role="dialog" aria-label="Trust SSH server" onKeyDown={(e) => e.key === "Escape" && answer(false)}>
        <div className="modal-title">Trust this SSH server?</div>
        <div className="modal-body">
          <p className="dialog-message">
            Chloe hasn't connected to <strong>{p.host}</strong> before. Its {keyName(p.keyType)} key fingerprint is:
          </p>
          <pre className="host-key-fingerprint">{p.fingerprint}</pre>
          <p className="hint">
            Check it with the server's administrator (on the server, <code>ssh-keygen -lf</code> on its host key prints it). Once trusted, Chloe
            remembers the key and refuses to connect if it changes.
          </p>
        </div>
        <div className="modal-footer">
          <span className="spacer" />
          <button disabled={busy} onClick={() => answer(false)}>
            Cancel
          </button>
          <button className="primary" autoFocus disabled={busy} onClick={() => answer(true)}>
            Trust and Connect
          </button>
        </div>
      </div>
    </div>
  );
}
