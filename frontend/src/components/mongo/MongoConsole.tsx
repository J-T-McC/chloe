// MongoShell: a mongosh-style console. Each line runs in the tab's shell (variables
// and "use" persist); the last statement's value prints, 20 documents at a time.
import { useEffect, useRef, useState } from "react";
import { Mongo, arr, errorText, type MG } from "../../api";
import { invalidateMongoCatalog } from "../../editor/mongo";
import { formatCompact } from "../../mongo/docs";
import { Tab, connById, useApp } from "../../store";
import { ShellHistory, isSensitiveMongo } from "../../shellHistory";

interface Line {
  kind: "cmd" | "out" | "err" | "info";
  text: string;
  prompt?: string;
}

const HISTORY_KEY = "mongo-console-history";
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
  const connId = tab.connId!;
  const [shell, setShell] = useState<string | null>(null);
  const [db, setDb] = useState(tab.database ?? "test");
  const [lines, setLines] = useState<Line[]>([{ kind: "info", text: `Connected to ${connById(connId)?.name ?? ""}. ↑/↓ walks the history, "it" shows more documents, "cls" or ⌃L clears.` }]);
  const [input, setInput] = useState("");
  const [pending, setPending] = useState(""); // an unfinished statement spanning lines
  const [busy, setBusy] = useState(false);
  const cursor = useRef<{ id: string; next: number } | null>(null);
  const history = useRef<ShellHistory>(null!);
  history.current ??= new ShellHistory(HISTORY_KEY, isSensitiveMongo);
  const pos = useRef(-1);
  const out = useRef<HTMLDivElement>(null);
  const field = useRef<HTMLInputElement>(null);

  useEffect(() => {
    let id: string | null = null;
    let cancelled = false;
    Mongo.OpenShell(connId, tab.database ?? "test")
      .then((s) => (cancelled ? Mongo.CloseShell(s) : ((id = s), setShell(s))))
      .catch((e) => setLines((l) => [...l, { kind: "err", text: errorText(e) }]));
    return () => {
      cancelled = true;
      if (id) Mongo.CloseShell(id);
    };
  }, [connId, tab.database]);

  useEffect(() => {
    out.current?.scrollTo({ top: out.current.scrollHeight });
  }, [lines]);

  const isActive = useApp((s) => s.activeTab === tab.id);
  useEffect(() => {
    if (isActive) setTimeout(() => field.current?.focus(), 0);
  }, [isActive, shell]);

  const remember = (cmd: string) => {
    history.current.add(cmd);
    pos.current = -1;
  };

  const run = async () => {
    if (!shell || busy) return;
    const line = input;
    const prompt = pending ? "..." : `${db}>`;
    setInput("");
    if (!pending && (line.trim() === "cls" || line.trim() === "clear")) {
      setLines([]);
      return;
    }
    setLines((l) => [...l, { kind: "cmd", text: line, prompt }]);
    const script = pending ? pending + "\n" + line : line;
    if (!script.trim()) return;
    if (script.trim() === "it") {
      const c = cursor.current;
      if (!c) {
        setLines((l) => [...l, { kind: "err", text: "no cursor" }]);
        return;
      }
      setBusy(true);
      try {
        const f = await Mongo.Fetch(shell, c.id, c.next, BATCH);
        const docs = arr(f.page.docs);
        cursor.current = f.hasMore ? { id: c.id, next: c.next + docs.length } : null;
        setLines((l) => [...l, { kind: "out", text: formatCompact({ t: "array", a: docs }) + (f.hasMore ? '\nType "it" for more' : "") }]);
      } catch (e) {
        setLines((l) => [...l, { kind: "err", text: errorText(e) }]);
      } finally {
        setBusy(false);
      }
      return;
    }
    setBusy(true);
    try {
      const o = await Mongo.Run(shell, script, BATCH);
      if (o.error && /Unexpected end of input|Unterminated/i.test(o.error) && !arr(o.results).length) {
        // An unfinished statement: keep reading lines.
        setPending(script);
        return;
      }
      setPending("");
      remember(script.trim());
      const { text, cursor: c } = consoleText(o);
      cursor.current = c ?? (arr(o.results).length ? null : cursor.current);
      const add: Line[] = [];
      if (text) add.push({ kind: "out", text });
      if (o.error) add.push({ kind: "err", text: o.error });
      setLines((l) => [...l, ...add]);
      if (o.db) setDb(o.db);
      if (/\b(create|drop|rename|insert)\w*/i.test(script)) invalidateMongoCatalog(connId);
    } catch (e) {
      setPending("");
      setLines((l) => [...l, { kind: "err", text: errorText(e) }]);
    } finally {
      setBusy(false);
      field.current?.focus();
    }
  };

  return (
    <div className="redis-console mongo-console" onClick={() => window.getSelection()?.isCollapsed && field.current?.focus()}>
      <div className="redis-console-out" ref={out}>
        {lines.map((l, i) => (
          <pre key={i} className={`rc-${l.kind}`}>
            {l.kind === "cmd" ? `${l.prompt} ${l.text}` : l.text}
          </pre>
        ))}
      </div>
      <div className="redis-console-in">
        <span className="prompt">{pending ? "..." : `${db}>`}</span>
        <input
          ref={field}
          autoFocus
          spellCheck={false}
          value={input}
          placeholder={shell ? "" : "Connecting…"}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              run();
            } else if (e.key === "ArrowUp" || e.key === "ArrowDown") {
              e.preventDefault();
              const h = history.current.items;
              pos.current = Math.max(-1, Math.min(h.length - 1, pos.current + (e.key === "ArrowUp" ? 1 : -1)));
              setInput(pos.current < 0 ? "" : h[pos.current]);
            } else if (e.key === "l" && e.ctrlKey) {
              e.preventDefault();
              setLines([]);
            } else if (e.key === "c" && e.ctrlKey) {
              if (busy && shell) Mongo.Stop(shell);
              else if (pending) {
                setPending("");
                setLines((l) => [...l, { kind: "info", text: "(cancelled)" }]);
              }
            }
          }}
        />
        {busy && <span className="muted small">running…</span>}
      </div>
    </div>
  );
}
