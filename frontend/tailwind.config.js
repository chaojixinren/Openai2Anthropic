/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{vue,js,ts,jsx,tsx}'],
  theme: {
    extend: {
      colors: {
        paper: '#F7F3EC',
        ivory: '#FDFBF7',
        ink: '#27272A',
        mist: '#6B7280',
        oat: '#E8DFD1',
        sand: '#DCCFBE',
        pine: '#556B5D',
        clay: '#A67C52'
      },
      fontFamily: {
        serif: ['"Noto Serif SC"', '"Source Han Serif SC"', 'STSong', 'serif'],
        sans: ['"Noto Sans SC"', 'Inter', 'system-ui', 'sans-serif']
      },
      boxShadow: {
        cloud: '0 24px 80px rgba(64, 52, 38, 0.08)',
        veil: '0 18px 40px rgba(84, 67, 49, 0.06)',
        float: '0 8px 24px rgba(84, 67, 49, 0.08)'
      },
      backgroundImage: {
        grain:
          'radial-gradient(circle at top, rgba(255,255,255,0.72), transparent 42%), linear-gradient(135deg, rgba(255,255,255,0.64), rgba(247,243,236,0.92))'
      }
    }
  },
  plugins: []
}

