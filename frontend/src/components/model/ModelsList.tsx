// Chloe's model library: every model it manages, listed in the Objects tab when the
// Model toolbar button is pressed (like Navicat's model object list). Files kept
// elsewhere open with Open External and can be saved into the library.
import { Shapes } from "lucide-react";
import { useEffect, useState } from "react";
import { DM, Models, arr } from "../../api";
import { engineLabel } from "../../dialect";
import { connById } from "../../store";
import { LibraryList } from "../LibraryList";
import { useAsync } from "../useAsync";
import { libraryChanged, listenModelLibrary, newModel, openModel, useModelLibrary } from "./actions";

/** Where a model came from or was last synchronized to, for display. */
export function linkLabel(link?: DM.Link | null) {
  if (!link) return "";
  const conn = connById(link.connId);
  return [conn ? conn.name : "(deleted connection)", link.database, link.schema].filter(Boolean).join(" › ");
}

export function ModelsList() {
  const version = useModelLibrary((s) => s.version);
  const { data: models, error, loading } = useAsync(async () => arr(await Models.List()), [version], [] as DM.Entry[], { keep: true });
  useEffect(() => listenModelLibrary(), []);

  return (
    <LibraryList
      noun="Model"
      title="Models"
      icon={Shapes}
      splitId="models-info"
      listClass="models-list"
      items={models}
      error={error}
      loading={loading}
      refresh={libraryChanged}
      open={openModel}
      create={[
        { label: "New MySQL Model", onClick: () => newModel("mysql") },
        { label: "New PostgreSQL Model", onClick: () => newModel("postgres") },
      ]}
      openExternal={() => openModel()}
      openExternalTitle="Open a model file kept outside Chloe"
      columns={[
        { title: "Type", cell: (m) => engineLabel(m.dialect) },
        { title: "Tables", num: true, cell: (m) => m.tables },
        { title: "Database", muted: true, cell: (m) => linkLabel(m.link) },
      ]}
      rename={(m, name) => Models.Rename(m.path, name)}
      duplicate={(m) => Models.Duplicate(m.path)}
      remove={(m) => Models.Delete(m.path)}
      removeMessage={(m) => `Delete the model "${m.name}"? This can't be undone.`}
      saveExternal={async (m) => {
        const f = await Models.Open(m.path);
        await Models.SaveExternal(f.model);
      }}
      reveal={(m) => Models.Reveal(m.path)}
      details={(m) => (
        <>
          <dt>Type</dt>
          <dd>{engineLabel(m.dialect)}</dd>
          {m.link && (
            <>
              <dt>Database</dt>
              <dd>{linkLabel(m.link)}</dd>
            </>
          )}
        </>
      )}
      extra={(m) => <ModelTables m={m} />}
      empty="No models yet. Create one, or reverse engineer a database from its menu."
    />
  );
}

/** The model's tables, read from its file. */
function ModelTables({ m }: { m: DM.Entry }) {
  const [tables, setTables] = useState<string[]>([]);
  useEffect(() => {
    Models.Open(m.path)
      .then((f) => setTables(arr(f.model.tables).map((t) => (t.schema ? `${t.schema}.${t.name}` : t.name))))
      .catch(() => setTables([]));
  }, [m.path, m.modified]);
  if (!tables.length) return null;
  return (
    <>
      <div className="info-ddl-head">Tables ({tables.length})</div>
      <ul className="model-tables">
        {tables.map((t) => (
          <li key={t}>{t}</li>
        ))}
      </ul>
    </>
  );
}
