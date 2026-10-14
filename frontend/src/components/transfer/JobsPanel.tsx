import { CheckCircle2, ChevronDown, ChevronRight, CircleX, Loader2, Square, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { Job, JobStatus, Transfer } from "../../api";
import { listenJobs, useTransfer } from "./state";

/** Status-bar button plus a popover listing background jobs. */
export function JobsButton() {
  const jobs = useTransfer((s) => s.jobs);
  const open = useTransfer((s) => s.jobsOpen);
  useEffect(() => listenJobs(), []);
  const running = jobs.filter((j) => j.status === JobStatus.JobRunning).length;
  if (!jobs.length) return null;
  return (
    <>
      <button className={"jobs-button" + (running ? " busy" : "")} onClick={() => useTransfer.getState().setJobsOpen(!open)} title="Background jobs">
        {running ? <Loader2 size={12} className="spin" /> : <CheckCircle2 size={12} />}
        {running ? `${running} running` : "Jobs"}
      </button>
      {open && <JobsPanel jobs={jobs} />}
    </>
  );
}

function JobsPanel({ jobs }: { jobs: Job[] }) {
  return (
    <div className="jobs-panel" onMouseDown={(e) => e.stopPropagation()}>
      <div className="jobs-head">
        <strong>Jobs</strong>
        <span className="spacer" />
        <button
          className="icon"
          title="Clear finished"
          onClick={async () => {
            await Transfer.ClearJobs();
            useTransfer.setState((s) => ({ jobs: s.jobs.filter((j) => j.status === JobStatus.JobRunning) }));
          }}
        >
          <Trash2 size={13} />
        </button>
        <button className="icon" onClick={() => useTransfer.getState().setJobsOpen(false)}>
          ×
        </button>
      </div>
      <div className="jobs-list">
        {jobs.map((j) => (
          <JobRow key={j.id} job={j} />
        ))}
      </div>
    </div>
  );
}

function JobRow({ job: j }: { job: Job }) {
  const [showLog, setShowLog] = useState(j.status === JobStatus.JobFailed);
  const pct = j.total > 0 ? Math.min(100, (j.done / j.total) * 100) : undefined;
  const icon =
    j.status === JobStatus.JobRunning ? <Loader2 size={13} className="spin" /> : j.status === JobStatus.JobDone && !j.errors ? <CheckCircle2 size={13} className="ok" /> : <CircleX size={13} className="err" />;
  return (
    <div className="job">
      <div className="job-title">
        {icon}
        <span className="job-name" title={j.title}>
          {j.title}
        </span>
        {j.status === JobStatus.JobRunning && (
          <button className="icon" title="Cancel" onClick={() => Transfer.CancelJob(j.id)}>
            <Square size={11} />
          </button>
        )}
      </div>
      {j.status === JobStatus.JobRunning && (
        <div className="progress">
          <div className={"progress-bar" + (pct === undefined ? " indeterminate" : "")} style={pct !== undefined ? { width: `${pct}%` } : undefined} />
        </div>
      )}
      <div className="job-meta">
        {j.status === JobStatus.JobRunning
          ? `${j.done.toLocaleString()}${j.total ? ` / ${j.total.toLocaleString()}` : ""} ${j.unit}`
          : j.message}
        {j.errors > 0 && <span className="err"> · {j.errors} error{j.errors === 1 ? "" : "s"}</span>}
      </div>
      {!!j.log?.length && (
        <>
          <button className="link small" onClick={() => setShowLog((v) => !v)}>
            {showLog ? <ChevronDown size={11} /> : <ChevronRight size={11} />} Log
          </button>
          {showLog && <pre className="job-log">{j.log.join("\n")}</pre>}
        </>
      )}
    </div>
  );
}
