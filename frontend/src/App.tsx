import { Database, FilePlus2, Plug, Table2, X } from "lucide-react";
import { useEffect } from "react";
import { Group, Panel, Separator } from "react-resizable-panels";
import { Events, ObjectKind } from "./api";
import { ConnectionDialog } from "./components/ConnectionDialog";
import { ContextMenuHost } from "./components/ContextMenu";
import { DialogHost } from "./components/Dialogs";
import { KindIcon } from "./components/icons";
import { DDLView, ObjectsPane } from "./components/ObjectsPane";
import { QueryEditor } from "./components/QueryEditor";
import { Sidebar, newQuery, useInitialLoad } from "./components/Sidebar";
import { TableViewer } from "./components/TableViewer";
import { Tab, useApp } from "./store";
import { useTree } from "./tree";

export default function App() {
  useInitialLoad();
  const tabs = useApp((s) => s.tabs);
  const active = useApp((s) => s.activeTab);
  const selection = useApp((s) => s.selection);
  const open = useApp((s) => s.open);

  // The backend can drop a connection (e.g. when its profile is edited).
  useEffect(
    () =>
      Events.On("connection:closed", (ev) => {
        const id = ev.data;
        if (useApp.getState().open[id]) useApp.getState().disconnect(id);
        useTree.getState().collapseConnection(id);
      }),
    [],
  );

  // Global shortcuts.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const meta = e.metaKey || e.ctrlKey;
      if (meta && e.key === "w") {
        e.preventDefault();
        useApp.getState().closeTab(useApp.getState().activeTab);
      }
      if (meta && e.key === "t" && selection && open[selection.connId]) {
        e.preventDefault();
        newQuery(selection.connId, selection.database, selection.schema);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [selection, open]);

  const selConnected = !!(selection && open[selection.connId]);

  return (
    <div className="app">
      <header className="titlebar">
        <div className="titlebar-drag" />
        <div className="main-toolbar">
          <ToolButton icon={<Plug size={20} />} label="Connection" onClick={() => useApp.getState().editConnection("new")} />
          <ToolButton icon={<FilePlus2 size={20} />} label="New Query" disabled={!selConnected} onClick={() => selection && newQuery(selection.connId, selection.database, selection.schema)} />
          <ToolButton
            icon={<Table2 size={20} />}
            label="Tables"
            disabled={!selection?.database}
            onClick={() => selection && useApp.getState().select({ ...selection, kind: ObjectKind.KindTable, object: undefined })}
          />
          <ToolButton
            icon={<Database size={20} />}
            label={selConnected ? "Disconnect" : "Connect"}
            disabled={!selection}
            onClick={() => {
              if (!selection) return;
              if (selConnected) {
                useApp.getState().disconnect(selection.connId);
                useTree.getState().collapseConnection(selection.connId);
              } else {
                const c = useApp.getState().connections.find((x) => x.id === selection.connId)!;
                useTree.getState().expand({ key: c.id, type: "conn", label: c.name, connId: c.id, depth: 0, expandable: true });
              }
            }}
          />
        </div>
      </header>
      <Group orientation="horizontal" className="body">
        <Panel defaultSize="260px" minSize="160px" maxSize="50%">
          <Sidebar />
        </Panel>
        <Separator className="split-handle vertical" />
        <Panel>
          <div className="workspace">
            <div className="tabbar">
              {tabs.map((t) => (
                <TabButton key={t.id} tab={t} active={t.id === active} />
              ))}
            </div>
            <div className="tab-content">
              {tabs.map((t) => (
                // Keep inactive tabs mounted so editors and grids keep their state.
                <div key={t.id} className="tab-pane" style={{ display: t.id === active ? "flex" : "none" }}>
                  <TabContent tab={t} />
                </div>
              ))}
            </div>
          </div>
        </Panel>
      </Group>
      <ConnectionDialog />
      <DialogHost />
      <ContextMenuHost />
    </div>
  );
}

function TabContent({ tab }: { tab: Tab }) {
  switch (tab.kind) {
    case "objects":
      return <ObjectsPane />;
    case "query":
      return <QueryEditor tab={tab} />;
    case "table":
      return <TableViewer tab={tab} />;
    case "ddl":
      return <DDLView connId={tab.connId!} objRef={tab.ref!} />;
  }
}

function TabButton({ tab, active }: { tab: Tab; active: boolean }) {
  const icon = tab.kind === "query" ? <FilePlus2 size={13} /> : tab.kind === "objects" ? <Database size={13} /> : tab.ref ? <KindIcon kind={tab.ref.kind} size={13} /> : null;
  return (
    <div
      className={"tab" + (active ? " active" : "")}
      onMouseDown={(e) => {
        if (e.button === 1) useApp.getState().closeTab(tab.id);
        else useApp.getState().setActive(tab.id);
      }}
      title={tab.title}
    >
      {icon}
      <span className="tab-title">{tab.title}</span>
      {tab.kind !== "objects" && (
        <button
          className="tab-close"
          onMouseDown={(e) => e.stopPropagation()}
          onClick={() => useApp.getState().closeTab(tab.id)}
        >
          <X size={12} />
        </button>
      )}
    </div>
  );
}

function ToolButton({ icon, label, onClick, disabled }: { icon: React.ReactNode; label: string; onClick(): void; disabled?: boolean }) {
  return (
    <button className="tool-button" onClick={onClick} disabled={disabled}>
      {icon}
      <span>{label}</span>
    </button>
  );
}

