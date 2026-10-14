// Drag and drop of connections and groups in the navigation tree.

export type Drag = { type: "conn"; id: string } | { type: "group"; name: string };

/** What the pointer is over: a connection row, a group row, or the empty area below the rows. */
export type DropTarget = { type: "conn"; id: string } | { type: "group"; name: string } | { type: "end" };

/** Where the indicator goes, and the store call that makes the move. */
export type Drop = {
  pos: "before" | "after" | "into" | "end";
  move: { conn: string; group: string; before: string } | { group: string; before: string };
};

/**
 * Resolve a drop. frac is how far down the target row the pointer is (0..1).
 * conns and groups are in tree order. Returns null where nothing can be dropped.
 */
export function resolveDrop(drag: Drag, target: DropTarget, frac: number, conns: { id: string; group?: string }[], groups: string[]): Drop | null {
  if (drag.type === "conn") {
    if (target.type === "group") return { pos: "into", move: { conn: drag.id, group: target.name, before: "" } };
    if (target.type === "end") return { pos: "end", move: { conn: drag.id, group: "", before: "" } };
    if (target.id === drag.id) return null;
    const over = conns.find((c) => c.id === target.id);
    if (!over) return null;
    const group = over.group ?? "";
    if (frac < 0.5) return { pos: "before", move: { conn: drag.id, group, before: over.id } };
    // After this connection = before the next one in its group, skipping the dragged one.
    const siblings = conns.filter((c) => (c.group ?? "") === group && c.id !== drag.id);
    const next = siblings[siblings.findIndex((c) => c.id === over.id) + 1];
    return { pos: "after", move: { conn: drag.id, group, before: next?.id ?? "" } };
  }
  // Groups are always above top-level connections, so they only reorder among themselves.
  if (target.type === "conn") return null;
  if (target.type === "end") return { pos: "end", move: { group: drag.name, before: "" } };
  if (target.name === drag.name) return null;
  if (frac < 0.5) return { pos: "before", move: { group: drag.name, before: target.name } };
  const rest = groups.filter((g) => g !== drag.name);
  return { pos: "after", move: { group: drag.name, before: rest[rest.indexOf(target.name) + 1] ?? "" } };
}
