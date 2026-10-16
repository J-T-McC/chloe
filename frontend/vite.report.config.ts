// The HTML report viewer (src/report): one script and one stylesheet that an
// exported report inlines, so it opens anywhere without Chloe. Built into
// public/report, which the app serves, before the app itself.
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  publicDir: false,
  define: { "process.env.NODE_ENV": JSON.stringify("production") },
  build: {
    outDir: "public/report",
    emptyOutDir: true,
    chunkSizeWarningLimit: 4000,
    lib: {
      entry: "src/report/main.tsx",
      name: "ChloeReport",
      formats: ["iife"],
      fileName: () => "report.js",
      cssFileName: "report",
    },
  },
});
