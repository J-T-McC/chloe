// Approval dialog for an AI agent's write. The agent's tool call waits until
// the user answers here (or two minutes pass, which counts as Deny).
import { useEffect, useState } from "react";
import { create } from "zustand";
import { Events, MCP, arr, type MCPApproval } from "../../api";

const useApprovals = create<{ queue: MCPApproval[] }>(() => ({ queue: [] }));

const drop = (id: string) => useApprovals.setState((s) => ({ queue: s.queue.filter((r) => r.id !== id) }));

export function ApprovalHost() {
  const req = useApprovals((s) => s.queue[0]);
  const waiting = useApprovals((s) => s.queue.length);
  const [busy, setBusy] = useState(false);
  const [password, setPassword] = useState("");
  useEffect(() => setPassword(""), [req?.id]);

  useEffect(() => {
    MCP.Pending()
      .then((p) => useApprovals.setState({ queue: arr(p) }))
      .catch(() => {});
    const off1 = Events.On("mcp:approval", (ev) => useApprovals.setState((s) => (s.queue.some((r) => r.id === ev.data.id) ? s : { queue: [...s.queue, ev.data] })));
    const off2 = Events.On("mcp:approval-done", (ev) => drop(ev.data));
    return () => {
      off1();
      off2();
    };
  }, []);

  if (!req) return null;
  const answer = async (approved: boolean) => {
    setBusy(true);
    await MCP.Resolve(req.id, approved, approved ? password : "").catch(() => {});
    drop(req.id);
    setBusy(false);
  };

  return (
    <div className="modal-backdrop">
      <div className="modal mcp-approval" role="dialog" aria-label="Approve AI change" onKeyDown={(e) => e.key === "Escape" && answer(false)}>
        <div className="modal-title">{req.kind === "connection" ? "Add this connection?" : "Allow this change?"}</div>
        <div className="modal-body">
          <p className="dialog-message">
            <strong>{req.endpoint}</strong> wants to <strong>{req.summary.charAt(0).toLowerCase() + req.summary.slice(1)}</strong>
          </p>
          {req.kind !== "connection" && (
            <div className="mcp-approval-target">
              {req.connection}
              {req.database ? ` › ${req.database}` : ""}
            </div>
          )}
          <pre className="mcp-approval-sql">{req.sql}</pre>
          {req.askPassword && (
            <label className="mcp-approval-password">
              Password
              <input
                type="password"
                aria-label="Connection password"
                autoComplete="off"
                value={password}
                placeholder={req.hasPassword ? "Leave empty to use the one the agent sent" : ""}
                onChange={(e) => setPassword(e.target.value)}
              />
            </label>
          )}
          {req.warning && <p className="mcp-approval-warning">{req.warning}</p>}
          <p className="hint">The agent waits for your answer. With no answer in two minutes the change is declined.{waiting > 1 ? ` ${waiting - 1} more waiting.` : ""}</p>
        </div>
        <div className="modal-footer">
          <span className="spacer" />
          <button disabled={busy} onClick={() => answer(false)}>
            Deny
          </button>
          <button className="primary" disabled={busy} onClick={() => answer(true)}>
            Approve
          </button>
        </div>
      </div>
    </div>
  );
}
