/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{vue,js}'],
  theme: {
    extend: {
      colors: {
        nexus: {
          bg: '#05070d',
          panel: 'rgba(10, 18, 32, 0.78)',
          line: 'rgba(120, 180, 255, 0.18)',
          green: '#00ff9d',
          cyan: '#38bdf8',
          warn: '#ffc53d',
          danger: '#ff4d6d',
          text: '#d7e3ff',
          dim: '#7c8db5',
        },
      },
      fontFamily: {
        mono: ['"JetBrains Mono"', 'Consolas', '"Courier New"', 'monospace'],
        sans: ['"Segoe UI"', '"PingFang SC"', '"Microsoft YaHei"', 'system-ui', 'sans-serif'],
      },
      boxShadow: {
        neon: '0 0 24px rgba(0, 255, 157, 0.25)',
        'neon-warn': '0 0 24px rgba(255, 197, 61, 0.25)',
        panel: '0 18px 60px rgba(0, 0, 0, 0.55)',
      },
      keyframes: {
        breathe: {
          '0%, 100%': { boxShadow: '0 0 12px rgba(0, 255, 157, 0.28)', opacity: '1' },
          '50%': { boxShadow: '0 0 34px rgba(0, 255, 157, 0.62)', opacity: '0.92' },
        },
        breatheWarn: {
          '0%, 100%': { boxShadow: '0 0 12px rgba(255, 197, 61, 0.32)', opacity: '1' },
          '50%': { boxShadow: '0 0 34px rgba(255, 197, 61, 0.7)', opacity: '0.94' },
        },
        blink: {
          '0%, 100%': { opacity: '1' },
          '50%': { opacity: '0.35' },
        },
        scan: {
          '0%': { transform: 'translateY(-100%)' },
          '100%': { transform: 'translateY(100%)' },
        },
        radar: {
          '0%': { transform: 'scale(0.6)', opacity: '0.85' },
          '100%': { transform: 'scale(2.4)', opacity: '0' },
        },
        floaty: {
          '0%, 100%': { transform: 'translateY(0)' },
          '50%': { transform: 'translateY(-4px)' },
        },
      },
      animation: {
        breathe: 'breathe 2.8s ease-in-out infinite',
        'breathe-warn': 'breatheWarn 1.1s ease-in-out infinite',
        blink: 'blink 1.4s ease-in-out infinite',
        scan: 'scan 6s linear infinite',
        radar: 'radar 2.6s ease-out infinite',
        floaty: 'floaty 4s ease-in-out infinite',
      },
    },
  },
  plugins: [],
}
