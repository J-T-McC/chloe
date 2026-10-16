import { ChangeKind, RowChange } from "./api";
import type { Cell, GridEdits } from "./components/DataGrid";

/**
 * Turns pending grid edits into row changes: deletes, then updates, then inserts.
 * colName maps a grid column to its table column ("" or undefined skips it); keyCols
 * names the table columns that identify a row, read from the row's original values.
 */
export async function buildRowChanges(
  edits: GridEdits,
  colName: (c: number) => string | undefined,
  keyCols: string[],
  original: (r: number) => Promise<Cell[]>,
  colCount: number,
): Promise<{ changes: RowChange[]; rows: number[] }> {
  const indexOf = new Map<string, number>();
  for (let c = 0; c < colCount; c++) {
    const n = colName(c);
    if (n && !indexOf.has(n)) indexOf.set(n, c);
  }
  const keyOf = (row: Cell[]) => {
    const key: Record<string, Cell> = {};
    for (const name of keyCols) key[name] = row[indexOf.get(name)!];
    return key;
  };
  const valuesOf = (m: Map<number, Cell> | undefined) => {
    const values: Record<string, Cell> = {};
    for (const [c, v] of m ?? []) {
      const n = colName(c);
      if (n) values[n] = v;
    }
    return values;
  };

  const changes: RowChange[] = [];
  const rows: number[] = [];
  for (const r of [...edits.deleted].sort((a, b) => a - b)) {
    changes.push({ kind: ChangeKind.ChangeDelete, key: keyOf(await original(r)), values: {} });
    rows.push(r);
  }
  for (const [r, m] of edits.updated) {
    if (edits.deleted.has(r) || edits.inserted.has(r)) continue;
    const values = valuesOf(m);
    if (!Object.keys(values).length) continue;
    changes.push({ kind: ChangeKind.ChangeUpdate, key: keyOf(await original(r)), values });
    rows.push(r);
  }
  for (const r of [...edits.inserted].sort((a, b) => a - b)) {
    changes.push({ kind: ChangeKind.ChangeInsert, key: {}, values: valuesOf(edits.updated.get(r)) });
    rows.push(r);
  }
  return { changes, rows };
}
