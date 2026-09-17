import { createFrontendModule } from '@backstage/frontend-plugin-api';
import { ThemeBlueprint } from '@backstage/plugin-app-react';
import { UnifiedThemeProvider } from '@backstage/theme';
import { teknologiDarkTheme } from '../../theme/teknologiTheme';

const teknologiDark = ThemeBlueprint.make({
  name: 'teknologi-dark',
  params: {
    theme: {
      id: 'teknologi-dark',
      title: 'Teknologi Dark',
      variant: 'dark',
      Provider: ({ children }) => (
        <UnifiedThemeProvider theme={teknologiDarkTheme}>
          {children}
        </UnifiedThemeProvider>
      ),
    },
  },
});

export const themeModule = createFrontendModule({
  pluginId: 'app',
  extensions: [teknologiDark],
});