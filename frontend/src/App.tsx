import { Activity, Bot, ChartColumn, ChevronDown, LayoutDashboard, Database, FilePlus2, History, Monitor, Moon, Network, PencilRuler, Plug, Shapes, Sun, Table2, TerminalSquare, UserRound, UsersRound, X } from "lucide-react";
import { ThemePref, onThemeChange, setThemePref, themePref } from "./theme";
import { memo, useEffect, useMemo, useLayoutEffect, useRef, useState } from "react";
import { Events, ObjectKind } from "./api";
import { ConnectionDialog } from "./components/ConnectionDialog";
import { ContextMenuHost } from "./components/ContextMenu";
import { DialogHost } from "./components/Dialogs";
import { DatabaseDialog } from "./components/DatabaseDialog";
import { KindIcon } from "./components/icons";
import { ManageConnections } from "./components/ManageConnections";
import { Split } from "./components/Split";
import { ExecuteFileDialog } from "./components/transfer/ExecuteFileDialog";
import { ExportWizard } from "./components/transfer/ExportWizard";
import { ImportWizard } from "./components/transfer/ImportWizard";
import { DataTransferWizard, openDataTransfer } from "./components/transfer/DataTransferWizard";
import { StructureSyncWizard, openStructureSync } from "./components/transfer/StructureSyncWizard";
import { DataSyncWizard, openDataSync } from "./components/transfer/DataSyncWizard";
import { JobsButton } from "./components/transfer/JobsPanel";
import { ServerMonitor } from "./components/ServerMonitor";
import { TableDesigner } from "./components/designer/TableDesigner";
import { ViewDesigner } from "./components/designer/ViewDesigner";
import { RedisConsole } from "./components/redis/RedisConsole";
import { CollectionViewer } from "./components/mongo/CollectionViewer";
import { CollectionDesigner } from "./components/mongo/CollectionDesigner";
import { DocEditorHost } from "./components/mongo/DocEditor";
import { MongoTransferHost } from "./components/mongo/TransferDialog";
import { MongoConsole } from "./components/mongo/MongoConsole";
import { MongoQuery } from "./components/mongo/MongoQuery";
import { designCollection, openMongoUsers } from "./components/mongo/actions";
import { MongoUsers } from "./components/mongo/MongoUsers";
import { registerMongoLanguage } from "./editor/mongo";
import { UsersPane } from "./components/users/UsersPane";
import { UserDesigner } from "./components/users/UserDesigner";
import { SetPrivilegesDialog } from "./components/users/SetPrivilegesDialog";
import { openUsers } from "./components/users/actions";
import { ERDiagram } from "./components/erd/ERDiagram";
import { ModelEditor } from "./components/model/ModelEditor";
import { WorkspaceView } from "./components/bi/WorkspaceView";
import { SourceEditor } from "./components/bi/SourceEditor";
import { ChartEditor } from "./components/bi/ChartEditor";
import { DashboardEditor } from "./components/bi/DashboardEditor";
import { CreateChartDialog } from "./components/bi/CreateChartDialog";
import { ExportHost } from "./components/bi/ExportHost";
import { listenBI } from "./components/bi/actions";
import { ChartTypeIcon } from "./components/bi/icons";
import { MenuItem, openMenu } from "./components/ContextMenu";
import { DBType } from "./api";
import { DDLView, ObjectsPane } from "./components/ObjectsPane";
import { QueryEditor } from "./components/QueryEditor";
import { HistoryLog, openHistoryLog } from "./components/HistoryLog";
import { MCPButton, MCPServer, openMCPServer } from "./components/mcp/MCPServer";
import { ApprovalHost } from "./components/mcp/ApprovalHost";
import { requestCloseTabs, saveQueryTab } from "./queries";
import { Sidebar, newQuery, openMonitor, selectAllSQL, useInitialLoad } from "./components/Sidebar";
import { TableViewer } from "./components/TableViewer";
import { Tab, connById, useApp } from "./store";
import { openRedisConsole } from "./components/redis/actions";
import { newTable } from "./tableActions";
import { RevealTarget, useTree } from "./tree";
import { Toolbar } from "./components/Toolbar";

registerMongoLanguage();

export default function App() {
  useInitialLoad();
  const tabs = useApp((s) => s.tabs);
  const active = useApp((s) => s.activeTab);
  // Tabs that have been shown, and so are mounted.
  const seen = useRef(new Set<string>());
  seen.current.add(active);
  // Tools > History Log in the native menu.
  useEffect(() => Events.On("menu:history-log", () => openHistoryLog()), []);
  useEffect(() => Events.On("menu:mcp", () => openMCPServer()), []);
  useEffect(() => listenBI(), []);
  useEffect(() => Events.On("menu:data-transfer", () => openDataTransfer(useApp.getState().selection)), []);
  useEffect(() => Events.On("menu:structure-sync", () => openStructureSync(useApp.getState().selection)), []);
  useEffect(() => Events.On("menu:data-sync", () => openDataSync(useApp.getState().selection)), []);
  const selection = useApp((s) => s.selection);
  const open = useApp((s) => s.open);
  // The sidebar reads the store itself; the same element lets React skip it when App re-renders.
  const sidebar = useMemo(() => <Sidebar />, []);

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
        requestCloseTabs([useApp.getState().activeTab]);
      }
      // ⌘S saves the query tab (the editor handles it itself when focused).
      if (meta && e.key.toLowerCase() === "s" && !e.defaultPrevented) {
        const t = useApp.getState().tabs.find((x) => x.id === useApp.getState().activeTab);
        if (t?.kind === "query") {
          e.preventDefault();
          saveQueryTab(t.id, e.shiftKey);
        }
      }
      // New Query: ⌘T on macOS (⌘Q is the system Quit); Ctrl+T or Ctrl+Q elsewhere,
      // matching Navicat on Windows. With a table or view selected, it starts with a SELECT.
      const isMac = /Mac/i.test(navigator.platform);
      const newQueryKey = meta && (e.key === "t" || (!isMac && e.key === "q"));
      if (newQueryKey && selection?.connId && open[selection.connId] && connById(selection.connId)?.type === DBType.Redis) {
        e.preventDefault();
        openRedisConsole(selection.connId, Number(selection.database ?? 0));
      } else if (newQueryKey && selection?.connId && open[selection.connId]) {
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
  const selRedis = connById(selection?.connId)?.type === DBType.Redis;
  const selSQLite = connById(selection?.connId)?.type === DBType.SQLite;
  const selMongo = connById(selection?.connId)?.type === DBType.MongoDB;
  // SQLite has no server to monitor.
  const monitorable = Object.keys(open).filter((id) => connById(id)?.type !== DBType.SQLite);

  return (
    <div className="app">
      <header className="titlebar">
        <div className="titlebar-drag" />
        <Toolbar className="main-toolbar">
          <ToolButton icon={<Plug size={20} />} label="Connection" onClick={() => useApp.getState().editConnection("new")} />
          <ToolButton
            icon={<FilePlus2 size={20} />}
            label="New Query"
            disabled={!selConnected}
            onClick={() => selection && (selRedis ? openRedisConsole(selection.connId, Number(selection.database ?? 0)) : newQuery(selection.connId, selection.database, selection.schema))}
          />
          <ToolButton
            icon={<PencilRuler size={20} />}
            label={selMongo ? "New Collection" : "New Table"}
            disabled={!selection?.database || !selConnected || selRedis}
            onClick={() => selection?.database && (selMongo ? designCollection(selection.connId, selection.database, undefined) : newTable(selection.connId, selection.database, selection.schema))}
          />
          <ToolButton
            icon={<Table2 size={20} />}
            label={selMongo ? "Collections" : "Tables"}
            disabled={!selection?.database || selRedis}
            onClick={() => selection && useApp.getState().select({ ...selection, kind: selMongo ? ObjectKind.KindCollection : ObjectKind.KindTable, object: undefined })}
          />
          <ToolButton icon={<UsersRound size={20} />} label="User" disabled={!selConnected || selRedis || selSQLite} onClick={() => selection && (selMongo ? openMongoUsers(selection.connId, selection.database) : openUsers(selection.connId))} />
          <ToolButton icon={<Shapes size={20} />} label="Model" onClick={() => useApp.getState().showModels()} />
          <ToolButton icon={<ChartColumn size={20} />} label="BI" onClick={() => useApp.getState().showBI()} />
          <ToolButton icon={<Activity size={20} />} label="Monitor" disabled={!monitorable.length} onClick={() => openMonitor(selConnected && !selSQLite ? selection!.connId : monitorable[0])} />
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
        </Toolbar>
        <div className="titlebar-right">
          <ThemeButton />
          <MCPButton />
          <JobsButton />
        </div>
      </header>
      <Split
        id="main"
        className="body"
        firstSize={260}
        minFirst={160}
        maxFirst="50"
        first={sidebar}
        second={
          <div className="workspace">
            <TabBar tabs={tabs} active={active} />
            <div className="tab-content">
              {tabs.map((t) => (
                // Tabs mount when first shown (restored query tabs connect then) and stay
                // mounted, so editors and grids keep their state.
                <div key={t.id} className="tab-pane" style={{ display: t.id === active ? "flex" : "none" }}>
                  {(seen.current.has(t.id) || t.id === active) && <TabContent tab={t} active={t.id === active} />}
                </div>
              ))}
            </div>
          </div>
        }
      />
      <ManageConnections />
      <ExportWizard />
      <ImportWizard />
      <DataTransferWizard />
      <StructureSyncWizard />
      <DataSyncWizard />
      <ExecuteFileDialog />
      <DatabaseDialog />
      <SetPrivilegesDialog />
      <ConnectionDialog />
      <DocEditorHost />
      <MongoTransferHost />
      <DialogHost />
      <ApprovalHost />
      <CreateChartDialog />
      <ExportHost />
      <ContextMenuHost />
    </div>
  );
}

// Memoised: App re-renders on every tree click and tab switch, and a tab only
// needs to when its own tab object (kept by updateTab) or active flag changes.
const TabContent = memo(function TabContent({ tab, active }: { tab: Tab; active: boolean }) {
  switch (tab.kind) {
    case "history":
      return <HistoryLog active={active} />;
    case "mcp":
      return <MCPServer />;
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
    case "bi":
      return <WorkspaceView tab={tab} />;
    case "bisource":
      return <SourceEditor tab={tab} />;
    case "bichart":
      return <ChartEditor tab={tab} />;
    case "bidash":
      return <DashboardEditor tab={tab} />;
    case "viewdesign":
      return <ViewDesigner tab={tab} />;
    case "redisconsole":
      return <RedisConsole tab={tab} />;
    case "users":
      return <UsersPane tab={tab} />;
    case "user":
      return <UserDesigner tab={tab} />;
    case "collection":
      return <CollectionViewer tab={tab} />;
    case "mongoquery":
      return <MongoQuery tab={tab} />;
    case "mongoshell":
      return <MongoConsole tab={tab} />;
    case "mongodesign":
      return <CollectionDesigner tab={tab} />;
    case "mongousers":
      return <MongoUsers tab={tab} />;
  }
});

// The open tabs. When they don't fit, the bar scrolls (its scrollbar is hidden,
// as macOS hides them), so a button at its end lists every tab, as Navicat's does,
// and the active tab is scrolled into view.
function TabBar({ tabs, active }: { tabs: Tab[]; active: string }) {
  const bar = useRef<HTMLDivElement>(null);
  const [over, setOver] = useState(false);
  useLayoutEffect(() => {
    const el = bar.current;
    if (!el) return;
    const check = () => setOver(el.scrollWidth > el.clientWidth + 1);
    check();
    const ro = new ResizeObserver(check);
    ro.observe(el);
    return () => ro.disconnect();
  }, [tabs.length]);
  useEffect(() => {
    bar.current?.querySelector(".tab.active")?.scrollIntoView({ block: "nearest", inline: "nearest" });
  }, [active, over]);
  return (
    <div className="tabbar-wrap">
      <div ref={bar} className="tabbar">
        {tabs.map((t) => (
          <TabButton key={t.id} tab={t} active={t.id === active} />
        ))}
      </div>
      {over && (
        <button
          className="tab-list"
          aria-label="All tabs"
          title="All tabs"
          onMouseDown={(e) => e.stopPropagation()}
          onClick={(e) => {
            const r = e.currentTarget.getBoundingClientRect();
            openMenu(
              { clientX: r.right - 220, clientY: r.bottom + 2, preventDefault() {} },
              tabs.map((t) => ({ label: t.title, checked: t.id === active, onClick: () => useApp.getState().setActive(t.id) })),
            );
          }}
        >
          <ChevronDown size={14} />
        </button>
      )}
    </div>
  );
}

function TabButton({ tab, active }: { tab: Tab; active: boolean }) {
  const icon = tab.kind === "bi" ? <ChartColumn size={13} /> : tab.kind === "bisource" ? <Database size={13} /> : tab.kind === "bichart" ? <ChartTypeIcon type="bar" size={13} /> : tab.kind === "bidash" ? <LayoutDashboard size={13} /> : tab.kind === "users" || tab.kind === "mongousers" ? <UsersRound size={13} /> : tab.kind === "user" ? <UserRound size={13} /> : tab.kind === "redisconsole" || tab.kind === "mongoshell" ? <TerminalSquare size={13} /> : tab.kind === "mongoquery" ? <FilePlus2 size={13} /> : tab.kind === "mongodesign" ? <PencilRuler size={13} /> : tab.kind === "model" ? <Shapes size={13} /> : tab.kind === "erd" ? <Network size={13} /> : tab.kind === "design" || tab.kind === "viewdesign" ? <PencilRuler size={13} /> : tab.kind === "monitor" ? <Activity size={13} /> : tab.kind === "query" ? <FilePlus2 size={13} /> : tab.kind === "objects" ? <Database size={13} /> : tab.kind === "history" ? <History size={13} /> : tab.kind === "mcp" ? <Bot size={13} /> : tab.ref ? <KindIcon kind={tab.ref.kind} size={13} /> : null;
  return (
    <div
      className={"tab" + (active ? " active" : "")}
      onMouseDown={(e) => {
        if (e.button === 1) requestCloseTabs([tab.id]);
        else if (e.button === 0) useApp.getState().setActive(tab.id);
      }}
      onContextMenu={(e) => openMenu(e, tabMenu(tab))}
      title={tab.title}
    >
      {icon}
      <span className="tab-title">{tab.title}</span>
      {tab.dirty && <span className="tab-dirty" title="Unsaved changes" />}
      {tab.kind !== "objects" && (
        <button
          className="tab-close"
          onMouseDown={(e) => e.stopPropagation()}
          onClick={() => requestCloseTabs([tab.id])}
        >
          <X size={12} />
        </button>
      )}
    </div>
  );
}

function ThemeButton() {
  const [pref, setPref] = useState<ThemePref>(themePref());
  useEffect(() => onThemeChange(() => setPref(themePref())), []);
  const Icon = pref === "light" ? Sun : pref === "dark" ? Moon : Monitor;
  const choose = (p: ThemePref) => setThemePref(p);
  return (
    <button
      className="titlebar-button"
      title="Appearance"
      onClick={(e) => {
        const r = e.currentTarget.getBoundingClientRect();
        openMenu({ clientX: r.left, clientY: r.bottom + 2, preventDefault() {} }, [
          { label: "System", checked: pref === "system", onClick: () => choose("system") },
          { label: "Light", checked: pref === "light", onClick: () => choose("light") },
          { label: "Dark", checked: pref === "dark", onClick: () => choose("dark") },
        ]);
      }}
    >
      <Icon size={15} />
    </button>
  );
}

/** Right-click menu of a tab (Navicat's). */
function tabMenu(tab: Tab): MenuItem[] {
  const app = useApp.getState();
  const tabs = app.tabs;
  const idx = tabs.findIndex((t) => t.id === tab.id);
  const closable = (t: Tab) => t.kind !== "objects";
  const others = tabs.filter((t) => t.id !== tab.id && closable(t)).map((t) => t.id);
  const right = tabs.slice(idx + 1).filter(closable).map((t) => t.id);
  const target = revealTarget(tab);
  return [
    { label: "Close", shortcut: "⌘W", disabled: !closable(tab), onClick: () => requestCloseTabs([tab.id]) },
    { label: "Close Other Tabs", disabled: !others.length, onClick: () => requestCloseTabs(others) },
    { label: "Close Tabs to the Right", disabled: !right.length, onClick: () => requestCloseTabs(right) },
    { label: "Close All", disabled: !tabs.some(closable), onClick: () => requestCloseTabs(tabs.filter(closable).map((t) => t.id)) },
    { separator: true },
    { label: "Move Tab to New Window", disabled: true },
    { separator: true },
    { label: "Add to Favorites", disabled: true },
    { label: "Reveal in Navigation Pane", disabled: !target, onClick: () => target && useTree.getState().reveal(target) },
  ];
}

/** Where a tab's object lives in the tree, if anywhere. */
function revealTarget(tab: Tab): RevealTarget | null {
  if (!tab.connId) return null;
  const r = tab.ref;
  const database = tab.database ?? r?.database;
  const schema = tab.schema ?? r?.schema;
  const hasObject = r && r.name && (tab.kind === "table" || tab.kind === "design" || tab.kind === "ddl" || tab.kind === "viewdesign" || tab.kind === "collection" || tab.kind === "mongodesign");
  if (hasObject) return { connId: tab.connId, database: r.database, schema: r.schema, kind: r.kind, name: r.name, signature: r.signature };
  // New table/view designers point at their folder.
  if ((tab.kind === "design" || tab.kind === "viewdesign") && r) return { connId: tab.connId, database: r.database, schema: r.schema, kind: r.kind };
  return { connId: tab.connId, database: database || undefined, schema: schema || undefined };
}

function ToolButton({ icon, label, onClick, disabled }: { icon: React.ReactNode; label: string; onClick(e: React.MouseEvent): void; disabled?: boolean }) {
  return (
    <button className="tool-button" onClick={onClick} disabled={disabled}>
      {icon}
      <span>{label}</span>
    </button>
  );
}

