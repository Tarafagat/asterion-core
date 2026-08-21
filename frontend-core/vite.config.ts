import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// frontend-core habla contra backend-core (Python/FastAPI local), no contra
// la API de Asterion Cloud — por eso en dev proxyea /api a un puerto fijo
// en vez de VITE_API_BASE_URL: backend-core normalmente arranca en un
// puerto libre elegido por el SO (ver `asterion local serve`), así que en
// desarrollo se fija BACKEND_CORE_PORT a mano.
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      "/api": `http://127.0.0.1:${process.env.BACKEND_CORE_PORT || 8091}`,
    },
  },
});
