import { Plugin, defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import wails from "@wailsio/runtime/plugins/vite";
import { fileURLToPath } from "node:url";

// The built app runs only its own scripts, so text from a database that reaches
// HTML can't run code. Dev builds skip it: Vite's hot reload injects inline scripts.
const csp = [
  "default-src 'self'",
  "script-src 'self'",
  "worker-src 'self' blob:",
  "style-src 'self' 'unsafe-inline'",
  "img-src 'self' data: blob:",
  "font-src 'self' data:",
  "connect-src 'self' data: blob: ws: wss:",
  "object-src 'none'",
  "base-uri 'none'",
].join("; ");

const contentSecurityPolicy = (): Plugin => ({
  name: "chloe-csp",
  apply: "build",
  transformIndexHtml: (html) => html.replace("<head>", `<head>\n    <meta http-equiv="Content-Security-Policy" content="${csp}" />`),
});

export default defineConfig({
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  resolve: {
    alias: {
      // Direct access to monaco's ESM tree (its package exports don't expose CSS files).
      "monaco-esm": fileURLToPath(new URL("./node_modules/monaco-editor/esm/vs", import.meta.url)),
    },
  },
  // Monaco makes up most of the bundle; it loads from disk, so size warnings are noise.
  build: { chunkSizeWarningLimit: 6000 },
  plugins: [react(), wails("./bindings"), contentSecurityPolicy()],
});
