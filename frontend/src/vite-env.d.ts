/// <reference types="vite/client" />

declare module "monaco-esm/editor/editor.worker.js?worker" {
  const WorkerFactory: new () => Worker;
  export default WorkerFactory;
}

// Monaco's SQL grammars, re-registered with word operators as keywords (editor/monaco.ts).
declare module "monaco-esm/languages/definitions/*.js" {
  export const language: { keywords: string[]; operators: string[]; builtinFunctions?: string[]; tokenizer: Record<string, unknown> };
}
