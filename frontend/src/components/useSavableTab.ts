import { useEffect, useRef } from "react";
import { tabSavers } from "../queries";
import { useApp } from "../store";

/** Marks an editing tab dirty and registers its save, so closing it asks to save first.
 *  save resolves false when nothing was saved (cancelled or failed); the tab then stays open.
 *  Editors that define save after an early return set it on the returned ref instead. */
export function useSavableTab(tabId: string, dirty: boolean, save: () => Promise<boolean> = async () => false) {
  const saveRef = useRef(save);
  saveRef.current = save;
  useEffect(() => {
    tabSavers.set(tabId, () => saveRef.current());
    return () => void tabSavers.delete(tabId);
  }, [tabId]);
  useEffect(() => {
    useApp.getState().updateTab(tabId, { dirty });
  }, [dirty, tabId]);
  return saveRef;
}
