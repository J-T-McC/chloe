// Opening data models: Chloe's library, files elsewhere, new ones, or reverse
// engineered from a database.
import { create } from "zustand";
import { DM, Events, Models, errorText } from "../../api";
import { Dialect } from "../../designer/model";
import { newId, useApp } from "../../store";
import { alert } from "../Dialogs";

/** Models handed to a tab before it mounts, by tab id. */
export const pendingModels = new Map<string, DM.Model>();

/** Bumped when the library changes, so the Models list reloads. */
export const useModelLibrary = create<{ version: number }>(() => ({ version: 0 }));
export const libraryChanged = () => useModelLibrary.setState((s) => ({ version: s.version + 1 }));

let listening = false;
/** Agents add and edit models over MCP. */
export function listenModelLibrary() {
  if (listening) return;
  listening = true;
  Events.On("mcp:model-changed", () => libraryChanged());
}

function openModelTab(model: DM.Model, path?: string) {
  const id = newId("model");
  pendingModels.set(id, model);
  useApp.getState().openTab({ id, kind: "model", title: `${model.name || "Untitled"} (model)`, modelPath: path });
}

export async function newModel(dialect: Dialect) {
  openModelTab(await Models.New("Untitled", dialect));
}

/** Open a model file: one of the library's, or with no path a file elsewhere the user picks. */
export async function openModel(path = "") {
  if (path) {
    const existing = useApp.getState().tabs.find((t) => t.kind === "model" && t.modelPath === path);
    if (existing) return useApp.getState().setActive(existing.id);
  }
  try {
    const f = await Models.Open(path);
    if (!f.path) return;
    openModelTab(f.model, f.path);
  } catch (e) {
    alert("Could not open model", errorText(e));
  }
}

/** Reverse engineer a database (or PostgreSQL schema) into a new model. */
export async function reverseToModel(connId: string, database: string, schema?: string, tables?: string[]) {
  try {
    openModelTab(await Models.Reverse(connId, database, schema ?? "", tables ?? null));
  } catch (e) {
    alert("Reverse engineering failed", errorText(e));
  }
}
