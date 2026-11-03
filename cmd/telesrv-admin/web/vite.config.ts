import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "dist",
    emptyOutDir: true
  },
  server: {
    port: 2410,
    proxy: {
      // changeOrigin stays off: the panel's CSRF check compares Origin with
      // Host, and the string shorthand would rewrite Host to the target,
      // so every login through the dev server failed "origin is not allowed".
      "/api": { target: "http://127.0.0.1:2600", changeOrigin: false }
    }
  }
});
