import { createUnifiedTheme, palettes } from '@backstage/theme';

// Blowfish "blowfish" scheme — slate-900 background, cyan accent
export const teknologiDarkTheme = createUnifiedTheme({
  palette: {
    ...palettes.dark,
    primary: { main: '#06b6d4' },      // Tailwind cyan-500
    secondary: { main: '#5cc1dd' },    // blog --color-note
    error: { main: '#ee293d' },        // blog --color-caution
    warning: { main: '#f08453' },      // blog --color-warn
    info: { main: '#5cc1dd' },
    success: { main: '#57ab5a' },      // blog --color-tip
    background: {
      default: '#0f172a',              // Tailwind slate-900
      paper: '#1e293b',                // Tailwind slate-800
    },
    navigation: {
      background: '#0d1424',
      indicator: '#06b6d4',
      color: '#94a3b8',                // slate-400
      selectedColor: '#f8fafc',        // slate-50
      navItem: {
        hoverBackground: '#1e293b',
      },
      submenu: {
        background: '#1e293b',
      },
    },
    pinSidebarButton: {
      icon: '#94a3b8',
      background: '#1e293b',
    },
    tabbar: {
      indicator: '#06b6d4',
    },
  },
  defaultPageTheme: 'home',
  pageTheme: {
    home: {
      colors: ['#0e7490', '#06b6d4'],  // cyan-700 → cyan-500
      fontColor: '#f8fafc',
      shape: 'none',
      backgroundImage: '',
    },
    documentation: {
      colors: ['#0e7490', '#0891b2'],
      fontColor: '#f8fafc',
      shape: 'none',
      backgroundImage: '',
    },
    tool: {
      colors: ['#164e63', '#0e7490'],
      fontColor: '#f8fafc',
      shape: 'none',
      backgroundImage: '',
    },
    service: {
      colors: ['#06b6d4', '#5cc1dd'],
      fontColor: '#f8fafc',
      shape: 'none',
      backgroundImage: '',
    },
    website: {
      colors: ['#0891b2', '#06b6d4'],
      fontColor: '#f8fafc',
      shape: 'none',
      backgroundImage: '',
    },
    library: {
      colors: ['#155e75', '#0e7490'],
      fontColor: '#f8fafc',
      shape: 'none',
      backgroundImage: '',
    },
    other: {
      colors: ['#0f172a', '#1e293b'],
      fontColor: '#f8fafc',
      shape: 'none',
      backgroundImage: '',
    },
    app: {
      colors: ['#06b6d4', '#0891b2'],
      fontColor: '#f8fafc',
      shape: 'none',
      backgroundImage: '',
    },
    apis: {
      colors: ['#0e7490', '#06b6d4'],
      fontColor: '#f8fafc',
      shape: 'none',
      backgroundImage: '',
    },
  },
});