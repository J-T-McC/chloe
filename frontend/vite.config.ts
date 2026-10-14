import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import wails from "@wailsio/runtime/plugins/vite";
import { fileURLToPath } from "node:url";

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
  plugins: [react(), wails("./bindings")],
});
