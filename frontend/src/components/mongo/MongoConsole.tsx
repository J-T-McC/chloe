// MongoShell: a mongosh-style console. Each line runs in the tab's shell (variables
// and "use" persist); the last statement's value prints, 20 documents at a time.
import { useRef, useState } from "react";
import { Mongo, arr, errorText, type MG } from "../../api";
import { invalidateMongoCatalog } from "../../editor/mongo";
import { formatCompact } from "../../mongo/docs";
import { isSensitiveMongo } from "../../shellHistory";
import { Tab } from "../../store";
import { ShellView, useShell } from "../ShellConsole";

const BATCH = 20;

/** Text of a run the way mongosh prints it: output, then the last statement's value. */
export function consoleText(o: MG.RunOutput): { text: string; cursor?: { id: string; next: number } } {
  const parts: string[] = [];
  if (o.output) parts.push(o.output.replace(/\n$/, ""));
  const rs = arr(o.results);
  const last = rs[rs.length - 1];
  let cursor: { id: string; next: number } | undefined;
  if (last) {
    if (last.kind === "documents") {
      const docs = arr(last.page?.docs);
      if (last.cursorId) {
        parts.push(formatCompact({ t: "array", a: docs }));
        if (last.hasMore) {
          parts.push('Type "it" for more');
          cursor = { id: last.cursorId, next: docs.length };
        }
      } else {
        parts.push(docs.length === 1 && !/^\s*\[/.test(last.statement) ? formatCompact(docs[0]) : formatCompact({ t: "array", a: docs }));
      }
    } else if (last.kind === "value" && last.value) {
      parts.push(last.value.t === "string" ? last.value.v ?? "" : formatCompact(last.value));
    } else if (last.kind === "text") {
      parts.push(last.text ?? "");
    }
  }
  return { text: parts.filter((p) => p !== "").join("\n"), cursor };
}

export function MongoConsole({ tab }: { tab: Tab }) {
  const [db, setDb] = useState(tab.database ?? "test");
  const [pending, setPending] = useState(""); // an unfinished statement spanning lines
  const cursor = useRef<{ id: string; next: number } | null>(null);
  const shell = useShell(tab, {
    open: (connId, database) => Mongo.OpenShell(connId, database ?? "test"),
    close: (id) => void Mongo.CloseShell(id),
    historyKey: "mongo-console-history",
    sensitive: isSensitiveMongo,
    help: '↑/↓ walks the history, "it" shows more documents, "cls" or ⌃L clears.',
  });
  const { connId, session, input, setInput, busy, setBusy, print, field } = shell;

  const run = async () => {
    if (!session || busy) return;
    const line = input;
    const prompt = pending ? "..." : `${db}>`;
    setInput("");
    if (!pending && (line.trim() === "cls" || line.trim() === "clear")) {
      shell.clear();
      return;
    }
    print({ kind: "cmd", text: line, prompt });
    const script = pending ? pending + "\n" + line : line;
    if (!script.trim()) return;
    if (script.trim() === "it") {
      const c = cursor.current;
      if (!c) {
        print({ kind: "err", text: "no cursor" });
        return;
      }
      setBusy(true);
      try {
        const f = await Mongo.Fetch(session, c.id, c.next, BATCH);
        const docs = arr(f.page.docs);
        cursor.current = f.hasMore ? { id: c.id, next: c.next + docs.length } : null;
        print({ kind: "out", text: formatCompact({ t: "array", a: docs }) + (f.hasMore ? '\nType "it" for more' : "") });
      } catch (e) {
        print({ kind: "err", text: errorText(e) });
      } finally {
        setBusy(false);
      }
      return;
    }
    setBusy(true);
    try {
      const o = await Mongo.Run(session, script, BATCH);
      if (o.error && /Unexpected end of input|Unterminated/i.test(o.error) && !arr(o.results).length) {
        // An unfinished statement: keep reading lines.
        setPending(script);
        return;
      }
      setPending("");
      shell.remember(script.trim());
      const { text, cursor: c } = consoleText(o);
      cursor.current = c ?? (arr(o.results).length ? null : cursor.current);
      if (text) print({ kind: "out", text });
      if (o.error) print({ kind: "err", text: o.error });
      if (o.db) setDb(o.db);
      if (/\b(create|drop|rename|insert)\w*/i.test(script)) invalidateMongoCatalog(connId);
    } catch (e) {
      setPending("");
      print({ kind: "err", text: errorText(e) });
    } finally {
      setBusy(false);
      field.current?.focus();
    }
  };

  return (
    <ShellView
      shell={shell}
      className="mongo-console"
      prompt={pending ? "..." : `${db}>`}
      onRun={run}
      onKeyDown={(e) => {
        if (e.key !== "c" || !e.ctrlKey) return;
        if (busy && session) Mongo.Stop(session);
        else if (pending) {
          setPending("");
          print({ kind: "info", text: "(cancelled)" });
        }
      }}
    />
  );
}
