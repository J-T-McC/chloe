// Shared state for transfer wizards and background jobs.
import { create } from "zustand";
import { Events, Job, ObjectRef, Transfer, arr } from "../../api";

export interface ExportTarget {
  connId: string;
  database: string;
  schema?: string;
  /** Tables to export; empty with sql set = query result export. */
  tables: ObjectRef[];
  sql?: string;
  /** Suggested file name for query exports. */
  name?: string;
}

export interface ImportTarget {
  connId: string;
  database: string;
  schema?: string;
  table?: ObjectRef;
}

interface TransferState {
  jobs: Job[];
  jobsOpen: boolean;
  exporting?: ExportTarget;
  importing?: ImportTarget;
  executing?: { connId: string; database: string; schema?: string };
  setJobsOpen(v: boolean): void;
}

export const useTransfer = create<TransferState>((set) => ({
  jobs: [],
  jobsOpen: false,
  setJobsOpen: (jobsOpen) => set({ jobsOpen }),
}));

let listening = false;

/** Track job progress events; returns an unsubscribe. */
export function listenJobs() {
  if (listening) return () => {};
  listening = true;
  Transfer.Jobs().then((jobs) => useTransfer.setState({ jobs: arr(jobs) }));
  const off = Events.On("job:progress", (ev) => {
    const j = ev.data as Job;
    j.log = arr(j.log);
    useTransfer.setState((s) => {
      const others = s.jobs.filter((x) => x.id !== j.id);
      return { jobs: [j, ...others].sort((a, b) => (a.started < b.started ? 1 : -1)) };
    });
  });
  return () => {
    listening = false;
    off();
  };
}

export const openExport = (t: ExportTarget) => useTransfer.setState({ exporting: t });
export const openImport = (t: ImportTarget) => useTransfer.setState({ importing: t });
export const openExecuteFile = (connId: string, database: string, schema?: string) => useTransfer.setState({ executing: { connId, database, schema } });

/** Start a job and open the jobs panel so its progress is visible. */
export async function track(start: Promise<Job>) {
  const j = await start;
  useTransfer.setState((s) => ({ jobs: [j, ...s.jobs.filter((x) => x.id !== j.id)], jobsOpen: true }));
  return j;
}
