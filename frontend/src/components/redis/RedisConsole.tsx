// Redis console: commands run on a dedicated connection (SELECT and MULTI keep their
// state), replies print like redis-cli, ↑/↓ walk the history, Tab completes commands.
import { useEffect, useMemo, useRef, useState } from "react";
import { Redis, arr, errorText } from "../../api";
import { tabComplete } from "../../redis/complete";
import { isSensitiveRedis } from "../../shellHistory";
import { type TabOf } from "../../store";
import { ShellView, useShell } from "../ShellConsole";

export function RedisConsole({ tab }: { tab: TabOf<"redisconsole"> }) {
  const [db, setDb] = useState(Number(tab.database) || 0);
  const [commands, setCommands] = useState<string[]>([]);
  const shell = useShell(tab, {
    open: (connId, database) => Redis.OpenConsole(connId, Number(database) || 0),
    close: (id) => void Redis.CloseConsole(id),
    historyKey: "redis-console-history",
    sensitive: isSensitiveRedis,
    help: 'Tab completes commands, ↑/↓ walks the history, "clear" or ⌃L clears.',
  });
  const { connId, session, input, setInput, busy, field } = shell;

  useEffect(() => {
    Redis.Commands(connId)
      .then((c) => setCommands(arr(c)))
      .catch(() => {});
  }, [connId]);

  const word = input.trimStart().split(/\s+/)[0].toUpperCase();
  // While cycling with Tab, keep offering the matches for the word as first typed.
  const typed = useRef<{ word: string; last: string } | null>(null);
  const cycling = typed.current && input.trim().toUpperCase() === typed.current.last.trim();
  const base = cycling ? typed.current!.word : word;
  const matches = useMemo(() => (base && !/\s/.test(input.trimStart()) ? commands.filter((c) => c.startsWith(base)) : []), [base, input, commands]);
  const hints = matches.slice(0, 12);

  const run = async () => {
    const cmd = input.trim();
    if (!cmd || !session || busy) return;
    if (cmd.toLowerCase() === "clear") {
      shell.clear();
      setInput("");
      return;
    }
    shell.remember(cmd);
    setInput("");
    shell.setBusy(true);
    shell.print({ kind: "cmd", text: cmd, prompt: `db${db}>` });
    try {
      const r = await Redis.ConsoleExec(session, cmd);
      shell.print(r.error ? { kind: "err", text: r.error } : { kind: r.output.startsWith("(error)") ? "err" : "out", text: r.output });
      setDb(r.db);
    } catch (e) {
      shell.print({ kind: "err", text: errorText(e) });
    } finally {
      shell.setBusy(false);
      field.current?.focus();
    }
  };

  return (
    <ShellView
      shell={shell}
      prompt={`db${db}>`}
      onRun={run}
      onChange={() => (typed.current = null)}
      hints={
        hints.length > 0 && (
          <div className="redis-console-hints">
            {hints.map((h) => (
              <span key={h} onMouseDown={(e) => (e.preventDefault(), setInput(h + " "), field.current?.focus())}>
                {h}
              </span>
            ))}
          </div>
        )
      }
      onKeyDown={(e) => {
        if (e.key !== "Tab") return;
        e.preventDefault();
        const next = tabComplete(base, matches, cycling ? typed.current!.last : null);
        if (next === null) return;
        typed.current = next.endsWith(" ") ? null : { word: base, last: next };
        setInput(next);
      }}
    />
  );
}
