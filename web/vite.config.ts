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
      '/api/ws': {
        target: WS_TARGET,
        ws: true,
        // 后端一重启，代理到它的 WebSocket 就会抛 ECONNRESET。不接住的话这个错误
        // 会冒到 socket 上变成未捕获异常，把整个 dev server 带崩——调试断线重连时必踩。
        configure: (proxy) => {
          proxy.on('error', (err) => {
            console.warn(`[ws proxy] ${err.message}`);
          });
          proxy.on('proxyReqWs', (_proxyReq, _req, socket) => {
            socket.on('error', (err) => {
              console.warn(`[ws proxy socket] ${err.message}`);
            });
          });
        },
      },
      '/api': { target: API_TARGET, changeOrigin: true },
    },
  },
  preview: { port: PORT, host: true },
});
