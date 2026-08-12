import { defineConfig, Plugin } from "vite";
import react from "@vitejs/plugin-react-swc";
import { readFileSync, writeFileSync } from "fs";
import { resolve } from "path";
import { createHash } from "crypto";

// Injects a build hash into sw.js so the browser detects updates on every deploy.
// During dev, __BUILD_HASH__ stays as-is (the SW is served from public/ unchanged).
function swBuildHash(): Plugin {
  return {
    name: "sw-build-hash",
    writeBundle(options) {
      const outDir = options.dir ?? "dist";
      const swPath = resolve(outDir, "sw.js");
      try {
        const content = readFileSync(swPath, "utf-8");
        const hash = createHash("md5")
          .update(Date.now().toString())
          .digest("hex")
          .slice(0, 8);
        writeFileSync(swPath, content.replace("__BUILD_HASH__", hash));
      } catch {
        // sw.js not in output — skip
      }
    },
  };
}

export default defineConfig({
  server: {
    port: 5173,
    proxy: {
      "/api": {
        target: "http://localhost:8080",
        changeOrigin: true,
        // The dashboard's live telemetry rides a WebSocket on /api/ui. Without
        // this the upgrade is not forwarded and dev only ever sees the initial
        // REST fetch, with the connection dot stuck on reconnecting.
        ws: true,
      },
    },
  },
  plugins: [react(), swBuildHash()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
});

