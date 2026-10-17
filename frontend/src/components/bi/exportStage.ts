// Whether the dashboard export stage is mounted (ExportGate); on once a render is asked for.
import { create } from "zustand";

export const useExportStage = create<{ on: boolean }>(() => ({ on: false }));
