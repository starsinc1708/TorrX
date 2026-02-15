import { defineConfig, loadEnv } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), '');
  const proxyTarget = env.VITE_API_PROXY_TARGET || 'http://localhost:8080';
  const searchProxyTarget = env.VITE_SEARCH_PROXY_TARGET || 'http://localhost:8090';

  return {
    plugins: [react()],
    server: {
      proxy: {
        '/torrents': {
          target: proxyTarget,
          changeOrigin: true,
        },
        '/settings/storage': {
          target: proxyTarget,
          changeOrigin: true,
        },
        '/settings/player': {
          target: proxyTarget,
          changeOrigin: true,
        },
        '/settings/encoding': {
          target: proxyTarget,
          changeOrigin: true,
        },
        '/swagger': {
          target: proxyTarget,
          changeOrigin: true,
        },
        '/watch-history': {
          target: proxyTarget,
          changeOrigin: true,
        },
        '/ws': {
          target: proxyTarget,
          changeOrigin: true,
          ws: true,
        },
        '/search': {
          target: searchProxyTarget,
          changeOrigin: true,
        },
      },
    },
    build: {
      outDir: 'dist',
    },
    define: {
      __APP_MODE__: JSON.stringify(mode),
    },
  };
});
