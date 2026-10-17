// Add / edit a whole document as shell-syntax text in a modal editor.
import { useEffect, useRef, useState } from "react";
import { create } from "zustand";
import { Mongo, errorText, type MG } from "../../api";
import { editorTheme, monaco, type Monaco } from "../../editor/monaco";
import { mongoEditorOptions, registerMongoLanguage, setMongoContext } from "../../editor/mongo";
import { formatVal } from "../../mongo/docs";
import { Modal, ModalFooter } from "../Modal";

interface Request {
  connId: string;
  db: string;
  coll: string;
  /** Edit this document; without it the dialog adds documents. */
  doc?: MG.Val;
  /** Starting text for a new document. */
  text?: string;
  resolve(saved: boolean): void;
}

const useDocEditor = create<{ req: Request | null }>(() => ({ req: null }));

/** Open the editor; resolves true once the document was saved. */
export function editDocument(r: Omit<Request, "resolve">): Promise<boolean> {
  return new Promise((resolve) => useDocEditor.setState({ req: { ...r, resolve } }));
}

const idOf = (doc?: MG.Val) => doc?.f?.find((f) => f.k === "_id")?.v;

export function DocEditorHost() {
  const req = useDocEditor((s) => s.req);
  if (!req) return null;
  return <DocEditorDialog key={JSON.stringify([req.coll, idOf(req.doc)])} req={req} />;
}

function DocEditorDialog({ req }: { req: Request }) {
  const host = useRef<HTMLDivElement>(null);
  const editorRef = useRef<Monaco.editor.IStandaloneCodeEditor | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const id = idOf(req.doc);
  const adding = !req.doc;

  const close = (saved: boolean) => {
    useDocEditor.setState({ req: null });
    req.resolve(saved);
  };

  const save = async () => {
    const text = editorRef.current?.getValue() ?? "";
    setBusy(true);
    setError("");
    try {
      if (adding || !id) await Mongo.InsertDocs(req.connId, req.db, req.coll, text);
      else await Mongo.ReplaceDoc(req.connId, req.db, req.coll, id, text);
      close(true);
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };
  const saveRef = useRef(save);
  saveRef.current = save;

  useEffect(() => {
    registerMongoLanguage();
    const initial = req.doc ? formatVal(req.doc, "  ", true) : req.text ?? "{\n  \n}";
    const model = monaco.editor.createModel(initial, "mongo");
    setMongoContext(model, { connId: req.connId, database: () => req.db });
    const ed = monaco.editor.create(host.current!, { ...mongoEditorOptions, model, theme: editorTheme() });
    editorRef.current = ed;
    ed.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => saveRef.current());
    ed.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.Enter, () => saveRef.current());
    if (!req.doc && !req.text) ed.setPosition({ lineNumber: 2, column: 3 });
    ed.focus();
    return () => {
      ed.dispose();
      model.dispose();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <Modal title={`${adding ? "Add Document" : "Edit Document"} — ${req.coll}`} className="doc-editor" onClose={() => close(false)}>
      <div className="modal-body">
        <div className="doc-editor-host" ref={host} />
        <div className="muted small">
          {adding ? "A document, or an array of documents to add several." : "The whole document is replaced; _id stays the same."} Shell syntax: ObjectId(), ISODate(), NumberLong()…
        </div>
      </div>
      {error && <div className="status-line err">{error}</div>}
      <ModalFooter onCancel={() => close(false)} onOk={save} okLabel={adding ? "Add" : "Save"} okDisabled={busy} />
    </Modal>
  );
}
