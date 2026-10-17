// Which collection export or import dialog is open.
import { create } from "zustand";

export interface TransferRequest {
  kind: "export" | "import";
  connId: string;
  db: string;
  coll: string;
  /** export: start from the viewer's filter and sort */
  filter?: string;
  sort?: string;
}

export const useMongoTransfer = create<{ req: TransferRequest | null }>(() => ({ req: null }));

export const exportCollection = (connId: string, db: string, coll: string, filter = "", sort = "") =>
  useMongoTransfer.setState({ req: { kind: "export", connId, db, coll, filter, sort } });
export const importCollection = (connId: string, db: string, coll: string) => useMongoTransfer.setState({ req: { kind: "import", connId, db, coll } });
