import { Activity, Database, FilePlus2, Network, PencilRuler, Plug, Shapes, Table2, X } from "lucide-react";
import { useEffect } from "react";
import { Events, ObjectKind } from "./api";
import { ConnectionDialog } from "./components/ConnectionDialog";
import { ContextMenuHost } from "./components/ContextMenu";
import { DialogHost } from "./components/Dialogs";
import { KindIcon } from "./components/icons";
import { ManageConnections } from "./components/ManageConnections";
import { Split } from "./components/Split";
import { ExecuteFileDialog } from "./components/transfer/ExecuteFileDialog";
import { ExportWizard } from "./components/transfer/ExportWizard";
import { ImportWizard } from "./components/transfer/ImportWizard";
import { JobsButton } from "./components/transfer/JobsPanel";
import { ServerMonitor } from "./components/ServerMonitor";
import { TableDesigner } from "./components/designer/TableDesigner";
import { ERDiagram } from "./components/erd/ERDiagram";
import { ModelEditor } from "./components/model/ModelEditor";
import { newModel, openModel } from "./components/model/actions";
import { openMenu } from "./components/ContextMenu";
import { Models, arr } from "./api";
import { DDLView, ObjectsPane } from "./components/ObjectsPane";
import { QueryEditor } from "./components/QueryEditor";
import { Sidebar, newQuery, openMonitor, selectAllSQL, useInitialLoad } from "./components/Sidebar";
import { TableViewer } from "./components/TableViewer";
import { Tab, useApp } from "./store";
import { newTable } from "./tableActions";
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
      // New Query: ⌘T on macOS (⌘Q is the system Quit); Ctrl+T or Ctrl+Q elsewhere,
      // matching Navicat on Windows. With a table or view selected, it starts with a SELECT.
      const isMac = /Mac/i.test(navigator.platform);
      const newQueryKey = meta && (e.key === "t" || (!isMac && e.key === "q"));
      if (newQueryKey && selection?.connId && open[selection.connId]) {
        e.preventDefault();
        const o = selection.object;
        const isData = o && (o.kind === ObjectKind.KindTable || o.kind === ObjectKind.KindView || o.kind === ObjectKind.KindMatView);
        newQuery(selection.connId, selection.database, selection.schema, isData ? selectAllSQL(selection.connId, o!) : "");
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [selection, open]);

  const selConnected = !!(selection?.connId && open[selection.connId]);

  return (
    <div className="app">
      <header className="titlebar">
        <div className="titlebar-drag" />
        <div className="main-toolbar">
          <ToolButton icon={<Plug size={20} />} label="Connection" onClick={() => useApp.getState().editConnection("new")} />
          <ToolButton icon={<FilePlus2 size={20} />} label="New Query" disabled={!selConnected} onClick={() => selection && newQuery(selection.connId, selection.database, selection.schema)} />
          <ToolButton
            icon={<PencilRuler size={20} />}
            label="New Table"
            disabled={!selection?.database || !selConnected}
            onClick={() => selection?.database && newTable(selection.connId, selection.database, selection.schema)}
          />
          <ToolButton
            icon={<Table2 size={20} />}
            label="Tables"
            disabled={!selection?.database}
            onClick={() => selection && useApp.getState().select({ ...selection, kind: ObjectKind.KindTable, object: undefined })}
          />
          <ToolButton icon={<Shapes size={20} />} label="Model" onClick={(e) => modelMenu(e)} />
          <ToolButton icon={<Activity size={20} />} label="Monitor" disabled={!Object.keys(open).length} onClick={() => openMonitor(selConnected ? selection!.connId : Object.keys(open)[0])} />
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
        <div className="titlebar-right">
          <JobsButton />
        </div>
      </header>
      <Split
        id="main"
        className="body"
        firstSize={260}
        minFirst={160}
        maxFirst="50"
        first={<Sidebar />}
        second={
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
        }
      />
      <ManageConnections />
      <ExportWizard />
      <ImportWizard />
      <ExecuteFileDialog />
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
    case "monitor":
      return <ServerMonitor tab={tab} />;
    case "design":
      return <TableDesigner tab={tab} />;
    case "erd":
      return <ERDiagram tab={tab} />;
    case "model":
      return <ModelEditor tab={tab} />;
  }
}

function TabButton({ tab, active }: { tab: Tab; active: boolean }) {
  const icon = tab.kind === "model" ? <Shapes size={13} /> : tab.kind === "erd" ? <Network size={13} /> : tab.kind === "design" ? <PencilRuler size={13} /> : tab.kind === "monitor" ? <Activity size={13} /> : tab.kind === "query" ? <FilePlus2 size={13} /> : tab.kind === "objects" ? <Database size={13} /> : tab.ref ? <KindIcon kind={tab.ref.kind} size={13} /> : null;
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

async function modelMenu(e: React.MouseEvent) {
  const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
  const recent = arr(await Models.Recent().catch(() => [] as string[]));
  openMenu({ clientX: r.left, clientY: r.bottom + 2, preventDefault() {} }, [
    { label: "New MySQL Model", onClick: () => newModel("mysql") },
    { label: "New PostgreSQL Model", onClick: () => newModel("postgres") },
    { label: "Open Model…", onClick: () => openModel() },
    ...(recent.length
      ? [{ label: "", separator: true }, ...recent.map((p) => ({ label: p.split("/").pop()!.replace(/\.chloemodel$/, ""), shortcut: p.split("/").slice(-2, -1)[0], onClick: () => openModel(p) }))]
      : []),
  ]);
}

function ToolButton({ icon, label, onClick, disabled }: { icon: React.ReactNode; label: string; onClick(e: React.MouseEvent): void; disabled?: boolean }) {
  return (
    <button className="tool-button" onClick={onClick} disabled={disabled}>
      {icon}
      <span>{label}</span>
    </button>
  );
}

