// What the Redis console and the MongoDB shell share: a session opened for the tab,
// the printed lines, the command line with its history (↑/↓) and ⌃L to clear.
import { useEffect, useRef, useState, type KeyboardEvent, type ReactNode } from "react";
import { errorText } from "../api";
import { ShellHistory } from "../shellHistory";
import { Tab, connById, useApp } from "../store";

export interface ShellLine {
  kind: "cmd" | "out" | "err" | "info";
  text: string;
  /** the prompt a command was typed at */
  prompt?: string;
}

export function useShell(
  tab: Tab,
  o: {
    /** opens the session; the tab's connection and database */
    open(connId: string, database: string | undefined): Promise<string>;
    close(id: string): void;
    historyKey: string;
    /** commands never written to the stored history (they carry passwords) */
    sensitive(cmd: string): boolean;
    /** printed after "Connected to …." */
    help: string;
  },
) {
  const connId = tab.connId!;
  const [session, setSession] = useState<string | null>(null);
  const [lines, setLines] = useState<ShellLine[]>([{ kind: "info", text: `Connected to ${connById(connId)?.name ?? ""}. ${o.help}` }]);
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const history = useRef<ShellHistory>(null!);
  history.current ??= new ShellHistory(o.historyKey, o.sensitive);
  const pos = useRef(-1);
  const out = useRef<HTMLDivElement>(null);
  const field = useRef<HTMLInputElement>(null);
  const print = (...l: ShellLine[]) => setLines((x) => [...x, ...l]);

  useEffect(() => {
    let id: string | null = null;
    let cancelled = false;
    o.open(connId, tab.database)
      .then((s) => (cancelled ? o.close(s) : ((id = s), setSession(s))))
      .catch((e) => print({ kind: "err", text: errorText(e) }));
    return () => {
      cancelled = true;
      if (id) o.close(id);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [connId, tab.database]);

  useEffect(() => {
    out.current?.scrollTo({ top: out.current.scrollHeight });
  }, [lines]);

  // Focus the command line when the session is ready and whenever its tab is shown.
  const isActive = useApp((s) => s.activeTab === tab.id);
  useEffect(() => {
    if (isActive) setTimeout(() => field.current?.focus(), 0);
  }, [isActive, session]);

  return {
    connId,
    session,
    lines,
    print,
    clear: () => setLines([]),
    input,
    setInput,
    busy,
    setBusy,
    field,
    out,
    remember(cmd: string) {
      history.current.add(cmd);
      pos.current = -1;
    },
    /** ↑/↓ and ⌃L; true when the key was one of them */
    keys(e: KeyboardEvent<HTMLInputElement>) {
      if (e.key === "ArrowUp" || e.key === "ArrowDown") {
        e.preventDefault();
        const h = history.current.items;
        pos.current = Math.max(-1, Math.min(h.length - 1, pos.current + (e.key === "ArrowUp" ? 1 : -1)));
        setInput(pos.current < 0 ? "" : h[pos.current]);
        return true;
      }
      if (e.key === "l" && e.ctrlKey) {
        e.preventDefault();
        setLines([]);
        return true;
      }
      return false;
    },
  };
}

export type Shell = ReturnType<typeof useShell>;

export function ShellView({ shell, className, prompt, hints, onRun, onKeyDown, onChange }: { shell: Shell; className?: string; prompt: string; hints?: ReactNode; onRun(): void; onKeyDown?(e: KeyboardEvent<HTMLInputElement>): void; onChange?(): void }) {
  const { field } = shell;
  return (
    <div className={className ? `redis-console ${className}` : "redis-console"} onClick={() => window.getSelection()?.isCollapsed && field.current?.focus()}>
      <div className="redis-console-out" ref={shell.out}>
        {shell.lines.map((l, i) => (
          <pre key={i} className={`rc-${l.kind}`}>
            {l.kind === "cmd" ? `${l.prompt} ${l.text}` : l.text}
          </pre>
        ))}
      </div>
      {hints}
      <div className="redis-console-in">
        <span className="prompt">{prompt}</span>
        <input
          ref={field}
          autoFocus
          spellCheck={false}
          value={shell.input}
          placeholder={shell.session ? "" : "Connecting…"}
          onChange={(e) => (onChange?.(), shell.setInput(e.target.value))}
          onKeyDown={(e) => {
            if (e.key === "Enter" || ((e.metaKey || e.ctrlKey) && e.key === "r")) {
              e.preventDefault();
              onRun();
            } else if (!shell.keys(e)) onKeyDown?.(e);
          }}
        />
        {shell.busy && <span className="muted small">running…</span>}
      </div>
    </div>
  );
}
