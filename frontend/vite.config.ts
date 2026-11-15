import { defineConfig, loadEnv } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '');
  const backendAPI = env.VITE_BACKEND_API;

  return {
    plugins: [react()],
    server: {
      port: 5173,
      ...(backendAPI ? { proxy: { '/api': { target: backendAPI, changeOrigin: true } } } : {}),
    },
    build: {
      outDir: 'dist',
    },
  };
});
