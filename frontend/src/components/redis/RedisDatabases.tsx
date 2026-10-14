// Objects pane for a Redis connection with no database selected: the logical
// databases and their key counts.
import { Database, TerminalSquare } from "lucide-react";
import { useEffect, useState } from "react";
import { R, Redis, arr, errorText } from "../../api";
import { useApp } from "../../store";
import { openRedisConsole } from "./actions";

export function RedisDatabases({ connId }: { connId: string }) {
  const [dbs, setDbs] = useState<R.DBInfo[]>([]);
  const [error, setError] = useState("");
  const version = useApp((s) => s.objectsVersion);
  useEffect(() => {
    Redis.Databases(connId)
      .then((d) => setDbs(arr(d)))
      .catch((e) => setError(errorText(e)));
  }, [connId, version]);
  const pick = (i: number) => useApp.getState().select({ connId, database: String(i) });
  return (
    <div className="redis-dbs">
      <div className="toolbar">
        <button onClick={() => openRedisConsole(connId, 0)}>
          <TerminalSquare size={14} /> Console
        </button>
      </div>
      {error && <div className="error-banner">{error}</div>}
      <table className="redis-table">
        <thead>
          <tr>
            <th>Database</th>
            <th>Keys</th>
          </tr>
        </thead>
        <tbody>
          {dbs.map((d) => (
            <tr key={d.index} className={d.keys ? "" : "muted"} onDoubleClick={() => pick(d.index)} onClick={() => pick(d.index)}>
              <td>
                <Database size={13} className="kind-icon" /> db{d.index}
              </td>
              <td>{d.keys.toLocaleString()}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
