import { defineConfig } from "vite";
import react from "@vitejs/plugin-react-swc";
import { readFileSync, writeFileSync } from "fs";
import { resolve } from "path";
import { createHash } from "crypto";
// Injects a build hash into sw.js so the browser detects updates on every deploy.
// During dev, __BUILD_HASH__ stays as-is (the SW is served from public/ unchanged).
function swBuildHash() {
    return {
        name: "sw-build-hash",
        writeBundle: function (options) {
            var _a;
            var outDir = (_a = options.dir) !== null && _a !== void 0 ? _a : "dist";
            var swPath = resolve(outDir, "sw.js");
            try {
                var content = readFileSync(swPath, "utf-8");
                var hash = createHash("md5")
                    .update(Date.now().toString())
                    .digest("hex")
                    .slice(0, 8);
                writeFileSync(swPath, content.replace("__BUILD_HASH__", hash));
            }
            catch (_b) {
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
