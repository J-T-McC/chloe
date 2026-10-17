// Chloe's BI library: every workspace it manages, listed in the Objects tab when
// the BI toolbar button is pressed (Navicat's workspace object list).
import { ChartColumn } from "lucide-react";
import { useEffect } from "react";
import { B, BI, arr } from "../../api";
import { useApp } from "../../store";
import { LibraryList } from "../LibraryList";
import { useAsync } from "../useAsync";
import { biChanged, listenBI, moveWorkspaceTabs, newWorkspace, openWorkspace, useBIVersion } from "./actions";

export function BIList() {
  const version = useBIVersion((s) => s.lib);
  const { data: items, error, loading } = useAsync(async () => arr(await BI.List()), [version], [] as B.Entry[], { keep: true });
  useEffect(() => listenBI(), []);

  return (
    <LibraryList
      noun="Workspace"
      title="BI Workspaces"
      icon={ChartColumn}
      splitId="bi-library-info"
      items={items}
      error={error}
      loading={loading}
      refresh={() => biChanged()}
      open={openWorkspace}
      create={[{ label: "New Workspace", onClick: newWorkspace }]}
      openExternal={async () => {
        const p = await BI.Import();
        if (p) openWorkspace(p);
      }}
      openExternalTitle="Copy a workspace file kept outside Chloe into the library"
      columns={[
        { title: "Data Sources", num: true, cell: (m) => m.dataSources },
        { title: "Charts", num: true, cell: (m) => m.charts },
        { title: "Dashboards", num: true, cell: (m) => m.dashboards },
      ]}
      rename={async (m, name) => {
        const np = await BI.Rename(m.path, name);
        moveWorkspaceTabs(m.path, np, name.trim());
        return np;
      }}
      duplicate={(m) => BI.Duplicate(m.path)}
      remove={async (m) => {
        await BI.Delete(m.path);
        const app = useApp.getState();
        app.closeTabs(app.tabs.filter((t) => t.biPath === m.path).map((t) => t.id));
      }}
      removeMessage={(m) => `Delete the workspace "${m.name}" with its data sources and charts? This can't be undone.`}
      saveExternal={(m) => BI.SaveExternal(m.path)}
      reveal={(m) => BI.Reveal(m.path)}
      details={(m) => (
        <>
          <dt>Data sources</dt>
          <dd>{m.dataSources}</dd>
          <dt>Charts</dt>
          <dd>{m.charts}</dd>
          <dt>Dashboards</dt>
          <dd>{m.dashboards}</dd>
        </>
      )}
      empty="No BI workspaces yet. Create one, or use Create Chart on a table or query result."
    />
  );
}
