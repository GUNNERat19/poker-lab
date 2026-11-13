import { defineConfig } from "vite";

const backend = process.env.POKER_BACKEND_URL ?? "http://127.0.0.1:8080";
const proxy = {
  "/live/rooms": { target: backend, ws: true },
};

export default defineConfig({
  server: { host: "127.0.0.1", port: 5173, strictPort: true, proxy },
  preview: { host: "127.0.0.1", port: 4173, strictPort: true, proxy },
});
