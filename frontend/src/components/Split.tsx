// Resizable two-pane split with a remembered size. Every multi-pane area in Chloe
// uses this so panes can be resized like in Navicat.
import type { ReactNode } from "react";
import { Group, Panel, Separator, useDefaultLayout } from "react-resizable-panels";
import { safeStorage } from "../prefs";

type Size = number | string; // number = pixels, "30%" or "30" = percent

export interface SplitProps {
  /** Stable id under which the size is remembered. */
  id: string;
  vertical?: boolean;
  first: ReactNode;
  second: ReactNode;
  /** Initial size of one pane (the other takes the rest). */
  firstSize?: Size;
  secondSize?: Size;
  minFirst?: Size;
  minSecond?: Size;
  maxFirst?: Size;
  maxSecond?: Size;
  className?: string;
  /** Hide the second pane; the first keeps its state (it isn't remounted). */
  collapsed?: boolean;
}

export function Split(p: SplitProps) {
  const { defaultLayout, onLayoutChanged } = useDefaultLayout({ id: `chloe:split:${p.id}`, storage: safeStorage, onlySaveAfterUserInteractions: true });
  return (
    <Group orientation={p.vertical ? "vertical" : "horizontal"} className={"split " + (p.className ?? "")} defaultLayout={defaultLayout} onLayoutChanged={onLayoutChanged}>
      <Panel id="first" defaultSize={p.firstSize} minSize={p.minFirst} maxSize={p.maxFirst}>
        <div className="split-pane">{p.first}</div>
      </Panel>
      {!p.collapsed && <Separator className={"split-handle " + (p.vertical ? "horizontal" : "vertical")} />}
      {!p.collapsed && (
        <Panel id="second" defaultSize={p.secondSize} minSize={p.minSecond} maxSize={p.maxSecond}>
          <div className="split-pane">{p.second}</div>
        </Panel>
      )}
    </Group>
  );
}
