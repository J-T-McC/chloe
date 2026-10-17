// Every kind of tab in one place: what it shows, its icon in the tab bar, and whether
// it shows one object of the tree (Reveal in Navigation Pane goes to that object).
import { Activity, Bot, ChartColumn, Database, FilePlus2, History, LayoutDashboard, Network, PencilRuler, Shapes, TerminalSquare, UserRound, UsersRound } from "lucide-react";
import { lazy, type ReactNode } from "react";
import { ChartTypeIcon } from "./components/bi/icons";
import { TableDesigner } from "./components/designer/TableDesigner";
import { ViewDesigner } from "./components/designer/ViewDesigner";
import { HistoryLog } from "./components/HistoryLog";
import { KindIcon } from "./components/icons";
import { MCPServer } from "./components/mcp/MCPServer";
import { CollectionDesigner } from "./components/mongo/CollectionDesigner";
import { CollectionViewer } from "./components/mongo/CollectionViewer";
import { MongoConsole } from "./components/mongo/MongoConsole";
import { MongoQuery } from "./components/mongo/MongoQuery";
import { MongoUsers } from "./components/mongo/MongoUsers";
import { DDLView, ObjectsPane } from "./components/ObjectsPane";
import { QueryEditor } from "./components/QueryEditor";
import { RedisConsole } from "./components/redis/RedisConsole";
import { ServerMonitor } from "./components/ServerMonitor";
import { TableViewer } from "./components/TableViewer";
import { UserDesigner } from "./components/users/UserDesigner";
import { UsersPane } from "./components/users/UsersPane";
import type { Tab, TabKind } from "./store";

// The diagram, model and BI tabs bring xyflow, elkjs, echarts and react-grid-layout:
// they load when such a tab first opens.
const ERDiagram = lazy(() => import("./components/erd/ERDiagram").then((m) => ({ default: m.ERDiagram })));
const ModelEditor = lazy(() => import("./components/model/ModelEditor").then((m) => ({ default: m.ModelEditor })));
const WorkspaceView = lazy(() => import("./components/bi/WorkspaceView").then((m) => ({ default: m.WorkspaceView })));
const SourceEditor = lazy(() => import("./components/bi/SourceEditor").then((m) => ({ default: m.SourceEditor })));
const ChartEditor = lazy(() => import("./components/bi/ChartEditor").then((m) => ({ default: m.ChartEditor })));
const DashboardEditor = lazy(() => import("./components/bi/DashboardEditor").then((m) => ({ default: m.DashboardEditor })));

interface TabKindDef {
  view(tab: Tab, active: boolean): ReactNode;
  /** the tab bar icon; the object's kind icon when left out */
  icon?: ReactNode;
  /** the tab shows the object in tab.ref */
  object?: boolean;
}

const I = 13;

export const TAB_KINDS: Record<TabKind, TabKindDef> = {
  objects: { view: () => <ObjectsPane />, icon: <Database size={I} /> },
  history: { view: (_, active) => <HistoryLog active={active} />, icon: <History size={I} /> },
  mcp: { view: () => <MCPServer />, icon: <Bot size={I} /> },
  query: { view: (t) => <QueryEditor tab={t} />, icon: <FilePlus2 size={I} /> },
  table: { view: (t) => <TableViewer tab={t} />, object: true },
  ddl: { view: (t) => <DDLView connId={t.connId!} objRef={t.ref!} />, object: true },
  monitor: { view: (t, active) => <ServerMonitor tab={t} active={active} />, icon: <Activity size={I} /> },
  design: { view: (t) => <TableDesigner tab={t} />, icon: <PencilRuler size={I} />, object: true },
  viewdesign: { view: (t) => <ViewDesigner tab={t} />, icon: <PencilRuler size={I} />, object: true },
  erd: { view: (t) => <ERDiagram tab={t} />, icon: <Network size={I} /> },
  model: { view: (t) => <ModelEditor tab={t} />, icon: <Shapes size={I} /> },
  bi: { view: (t) => <WorkspaceView tab={t} />, icon: <ChartColumn size={I} /> },
  bisource: { view: (t) => <SourceEditor tab={t} />, icon: <Database size={I} /> },
  bichart: { view: (t) => <ChartEditor tab={t} />, icon: <ChartTypeIcon type="bar" size={I} /> },
  bidash: { view: (t) => <DashboardEditor tab={t} />, icon: <LayoutDashboard size={I} /> },
  redisconsole: { view: (t) => <RedisConsole tab={t} />, icon: <TerminalSquare size={I} /> },
  users: { view: (t) => <UsersPane tab={t} />, icon: <UsersRound size={I} /> },
  user: { view: (t) => <UserDesigner tab={t} />, icon: <UserRound size={I} /> },
  collection: { view: (t) => <CollectionViewer tab={t} />, object: true },
  mongoquery: { view: (t) => <MongoQuery tab={t} />, icon: <FilePlus2 size={I} /> },
  mongoshell: { view: (t) => <MongoConsole tab={t} />, icon: <TerminalSquare size={I} /> },
  mongodesign: { view: (t) => <CollectionDesigner tab={t} />, icon: <PencilRuler size={I} />, object: true },
  mongousers: { view: (t) => <MongoUsers tab={t} />, icon: <UsersRound size={I} /> },
};

export const tabIcon = (t: Tab) => TAB_KINDS[t.kind].icon ?? (t.ref ? <KindIcon kind={t.ref.kind} size={I} /> : null);
