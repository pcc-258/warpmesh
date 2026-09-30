import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  // Absolute asset URLs. A relative base breaks every deep link: on
  // /devices/<id>/desktop the browser resolves "./assets/index.js" against
  // /devices/<id>/assets/..., the SPA fallback answers with index.html, and
  // strict module MIME checking refuses to execute it, leaving a blank page.
  base: "/",
  build: {
    outDir: "dist",
    target: "es2022",
  },
});
