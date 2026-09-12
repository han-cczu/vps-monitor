import path from 'path';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';
import checker from 'vite-plugin-checker';

// ----------------------------------------------------------------------

const PORT = 8080;
const API_TARGET = 'http://localhost:9000';
const WS_TARGET = 'ws://localhost:9000';

// https://vite.dev/config/
export default defineConfig({
  plugins: [
    react(),
    checker({
      typescript: true,
      eslint: {
        lintCommand: 'eslint "./src/**/*.{js,jsx,ts,tsx}"',
      },
      overlay: {
        position: 'tl',
        initialIsOpen: false,
      },
    }),
  ],
  resolve: {
    alias: [
      {
        find: /^src(.+)/,
        replacement: path.resolve(process.cwd(), 'src/$1'),
      },
    ],
  },
  server: {
    port: PORT,
    host: true,
    proxy: {
      // 顺序有意义：WebSocket 规则必须排在 /api 前面
      '/api/ws': { target: WS_TARGET, ws: true },
      '/api': { target: API_TARGET, changeOrigin: true },
    },
  },
  preview: { port: PORT, host: true },
});
