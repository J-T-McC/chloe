// Mounts the dashboard export stage (ExportHost) only once something asks for a render,
// so the canvas, the charts and html-to-image load then rather than at startup.
import { Suspense, lazy, useEffect } from "react";
import { create } from "zustand";
import { Events } from "../../api";

export const useExportStage = create<{ on: boolean }>(() => ({ on: false }));

const Stage = lazy(() => import("./ExportHost").then((m) => ({ default: m.ExportHost })));

export function ExportGate() {
  const on = useExportStage((s) => s.on);
  // Agents ask over MCP.
  useEffect(
    () =>
      Events.On("bi:render", (ev) => {
        useExportStage.setState({ on: true });
        void import("./ExportHost").then((m) => m.onRender(ev.data));
      }),
    [],
  );
  return on ? (
    <Suspense fallback={null}>
      <Stage />
    </Suspense>
  ) : null;
}
