// The frame Data Transfer, Structure Synchronization and Data Synchronization share:
// the source → target header, the step strip, the source/target pickers with the swap
// button, the job a wizard starts, its footer and the deployment step.
import { ArrowLeftRight, ArrowRight, Copy, FilePlus2 } from "lucide-react";
import { useState, type ReactNode } from "react";
import { Transfer, copyText, type Endpoint, type Job } from "../../api";
import { connById, useApp } from "../../store";
import { ConnIcon } from "../icons";
import { CodeBlock } from "../CodeBlock";
import { newQuery } from "../../actions/tabs";
import { Toolbar } from "../Toolbar";
import { EndpointPicker } from "./shared";
import { track, useTransfer } from "./state";

export const emptyEnd = (): Endpoint => ({ connId: "", database: "", schema: "" });

/** The endpoint a wizard opens with, from a tree selection. */
export const presetEnd = (e?: Partial<Endpoint>): Endpoint | undefined => (e?.connId ? { connId: e.connId, database: e.database ?? "", schema: e.schema ?? "" } : undefined);

export function WizardSteps<S extends string>({ steps, step }: { steps: [S, string][]; step: S }) {
  return (
    <div className="wizard-steps">
      {steps.map(([s, label], i) => (
        <span key={s} className={step === s ? "active" : ""}>
          {i + 1}. {label}
        </span>
      ))}
    </div>
  );
}

function EndLabel({ e }: { e: Endpoint }) {
  const c = connById(e.connId);
  return c ? (
    <span className="dt-end">
      <ConnIcon type={c.type} preset={c.preset} size={20} />
      <span>
        <strong>{c.name}</strong>
        <span className="muted">{[e.database, e.schema].filter(Boolean).join(".") || "—"}</span>
      </span>
    </span>
  ) : (
    <span className="dt-end muted">Not chosen</span>
  );
}

export function EndpointsHeader({ source, target }: { source: Endpoint; target: Endpoint }) {
  return (
    <div className="dt-header">
      <EndLabel e={source} />
      <ArrowRight size={18} className="muted" />
      <EndLabel e={target} />
    </div>
  );
}

/** Source and target pickers side by side, with the swap button between them. */
export function EndpointPair(p: {
  source: Endpoint;
  target: Endpoint;
  setSource(e: Endpoint): void;
  setTarget(e: Endpoint): void;
  conns: ReturnType<typeof useApp.getState>["connections"];
  /** the target may name a database or schema that doesn't exist yet */
  allowNew?: boolean;
  create?: boolean;
}) {
  return (
    <div className="dt-general">
      <EndpointPicker title="Source" value={p.source} onChange={p.setSource} conns={p.conns} />
      <button
        className="dt-swap"
        title="Swap source and target"
        onClick={() => {
          p.setSource(p.target);
          p.setTarget(p.source);
        }}
      >
        <ArrowLeftRight size={16} />
      </button>
      <EndpointPicker title="Target" value={p.target} onChange={p.setTarget} conns={p.conns} allowNew={p.allowNew} create={p.create} />
    </div>
  );
}

/** The job a wizard starts and follows on its last step. */
export function useWizardJob() {
  const [id, setId] = useState("");
  const job = useTransfer((s) => s.jobs.find((j) => j.id === id));
  return {
    job,
    running: job?.status === "running",
    reset: () => setId(""),
    /** Starts the job here rather than in the Jobs panel; throws when it can't start. */
    async start(p: Promise<Job>) {
      const j = await track(p);
      useTransfer.setState({ jobsOpen: false });
      setId(j.id);
    },
    stop: () => Transfer.CancelJob(id),
  };
}

/**
 * Cancel, Back and the step's primary button; on the last step Stop while the job runs,
 * then `after` (Back, Recompare) and Close. `children` go on the left.
 */
export function WizardFooter(p: {
  children?: ReactNode;
  last: boolean;
  running: boolean;
  onStop(): void;
  onClose(): void;
  after?: ReactNode;
  onBack?(): void;
  next?: ReactNode;
}) {
  return (
    <div className="modal-footer">
      {p.children}
      <span className="spacer" />
      {p.last ? (
        p.running ? (
          <button onClick={p.onStop}>Stop</button>
        ) : (
          <>
            {p.after}
            <button className="primary" onClick={p.onClose}>
              Close
            </button>
          </>
        )
      ) : (
        <>
          <button onClick={p.onClose}>Cancel</button>
          {p.onBack && <button onClick={p.onBack}>Back</button>}
          {p.next}
        </>
      )}
    </div>
  );
}

/** The deployment step: the script, Copy, Open in Query Editor and the run options (`children`). */
export function DeployStep(p: { script: string; shown?: string; count: number; target: Endpoint; onClose(): void; children?: ReactNode }) {
  return (
    <div className="ss-deploy">
      <Toolbar>
        <button onClick={() => copyText(p.script)}>
          <Copy size={14} /> Copy Script
        </button>
        <button
          onClick={() => {
            newQuery(p.target.connId, p.target.database, p.target.schema || undefined, p.script);
            p.onClose();
          }}
        >
          <FilePlus2 size={14} /> Open in Query Editor
        </button>
        <span className="spacer" />
        {p.children}
        <span className="muted">{p.count.toLocaleString()} statement(s)</span>
      </Toolbar>
      <div className="ss-script">
        <CodeBlock code={p.shown ?? p.script} />
      </div>
    </div>
  );
}
