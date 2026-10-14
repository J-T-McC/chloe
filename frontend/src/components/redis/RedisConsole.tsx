// Redis console: commands run on a dedicated connection (SELECT and MULTI keep their
// state), replies print like redis-cli, ↑/↓ walk the history, Tab completes commands.
import { useEffect, useMemo, useRef, useState } from "react";
import { Redis, arr, errorText } from "../../api";
import { tabComplete } from "../../redis/complete";
import { Tab, connById, useApp } from "../../store";

interface Line {
  kind: "cmd" | "out" | "err" | "info";
  text: string;
  db?: number;
}

const HISTORY_KEY = "redis-console-history";

function loadHistory(): string[] {
  try {
    return JSON.parse(localStorage.getItem(HISTORY_KEY) || "[]");
  } catch {
    return [];
  }
}

export function RedisConsole({ tab }: { tab: Tab }) {
  const connId = tab.connId!;
  const [session, setSession] = useState<string | null>(null);
  const [db, setDb] = useState(Number(tab.database) || 0);
  const [lines, setLines] = useState<Line[]>([{ kind: "info", text: `Connected to ${connById(connId)?.name ?? ""}. Tab completes commands, ↑/↓ walks the history, "clear" or ⌃L clears.` }]);
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const [commands, setCommands] = useState<string[]>([]);
  const history = useRef<string[]>(loadHistory());
  const pos = useRef(-1);
  const out = useRef<HTMLDivElement>(null);
  const field = useRef<HTMLInputElement>(null);

  useEffect(() => {
    let id: string | null = null;
    let cancelled = false;
    Redis.OpenConsole(connId, Number(tab.database) || 0)
      .then((s) => (cancelled ? Redis.CloseConsole(s) : ((id = s), setSession(s))))
      .catch((e) => setLines((l) => [...l, { kind: "err", text: errorText(e) }]));
    Redis.Commands(connId)
      .then((c) => setCommands(arr(c)))
      .catch(() => {});
    return () => {
      cancelled = true;
      if (id) Redis.CloseConsole(id);
    };
  }, [connId, tab.database]);

  useEffect(() => {
    out.current?.scrollTo({ top: out.current.scrollHeight });
  }, [lines]);

  // Focus the command line when the console is ready and whenever its tab is shown.
  const isActive = useApp((s) => s.activeTab === tab.id);
  useEffect(() => {
    if (isActive) setTimeout(() => field.current?.focus(), 0);
  }, [isActive, session]);

  const word = input.trimStart().split(/\s+/)[0].toUpperCase();
  // While cycling with Tab, keep offering the matches for the word as first typed.
  const typed = useRef<{ word: string; last: string } | null>(null);
  const base = typed.current && input.trim().toUpperCase() === typed.current.last.trim() ? typed.current.word : word;
  const matches = useMemo(() => (base && !/\s/.test(input.trimStart()) ? commands.filter((c) => c.startsWith(base)) : []), [base, input, commands]);
  const hints = matches.slice(0, 12);

  const run = async () => {
    const cmd = input.trim();
    if (!cmd || !session || busy) return;
    if (cmd.toLowerCase() === "clear") {
      setLines([]);
      setInput("");
      return;
    }
    history.current = [cmd, ...history.current.filter((h) => h !== cmd)].slice(0, 200);
    try {
      localStorage.setItem(HISTORY_KEY, JSON.stringify(history.current));
    } catch {
      /* history is a convenience */
    }
    pos.current = -1;
    setInput("");
    setBusy(true);
    setLines((l) => [...l, { kind: "cmd", text: cmd, db }]);
    try {
      const r = await Redis.ConsoleExec(session, cmd);
      setLines((l) => [...l, r.error ? { kind: "err", text: r.error } : { kind: r.output.startsWith("(error)") ? "err" : "out", text: r.output }]);
      setDb(r.db);
    } catch (e) {
      setLines((l) => [...l, { kind: "err", text: errorText(e) }]);
    } finally {
      setBusy(false);
      field.current?.focus();
    }
  };

  return (
    <div className="redis-console" onClick={() => window.getSelection()?.isCollapsed && field.current?.focus()}>
      <div className="redis-console-out" ref={out}>
        {lines.map((l, i) => (
          <pre key={i} className={`rc-${l.kind}`}>
            {l.kind === "cmd" ? `db${l.db}> ${l.text}` : l.text}
          </pre>
        ))}
      </div>
      {hints.length > 0 && (
        <div className="redis-console-hints">
          {hints.map((h) => (
            <span key={h} onMouseDown={(e) => (e.preventDefault(), setInput(h + " "), field.current?.focus())}>
              {h}
            </span>
          ))}
        </div>
      )}
      <div className="redis-console-in">
        <span className="prompt">db{db}&gt;</span>
        <input
          ref={field}
          autoFocus
          value={input}
          placeholder={session ? "" : "Connecting…"}
          onChange={(e) => ((typed.current = null), setInput(e.target.value))}
          onKeyDown={(e) => {
            if (e.key === "Enter" || ((e.metaKey || e.ctrlKey) && e.key === "r")) {
              e.preventDefault();
              run();
            }
            else if (e.key === "Tab") {
              e.preventDefault();
              const next = tabComplete(base, matches, typed.current && input.trim().toUpperCase() === typed.current.last.trim() ? typed.current.last : null);
              if (next === null) return;
              typed.current = next.endsWith(" ") ? null : { word: base, last: next };
              setInput(next);
            } else if (e.key === "ArrowUp" || e.key === "ArrowDown") {
              e.preventDefault();
              const h = history.current;
              pos.current = Math.max(-1, Math.min(h.length - 1, pos.current + (e.key === "ArrowUp" ? 1 : -1)));
              setInput(pos.current < 0 ? "" : h[pos.current]);
            } else if (e.key === "l" && e.ctrlKey) {
              e.preventDefault();
              setLines([]);
            }
          }}
        />
        {busy && <span className="muted small">running…</span>}
      </div>
    </div>
  );
}
