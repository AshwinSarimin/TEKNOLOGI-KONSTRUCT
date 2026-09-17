import { createFrontendModule, PageBlueprint } from '@backstage/frontend-plugin-api';
import React from 'react';

const homePage = PageBlueprint.make({
  name: 'home',
  params: {
    path: '/',
    loader: () => import('./HomePage').then(({ HomePage }) => React.createElement(HomePage)),
  },
});

export const homeModule = createFrontendModule({
  pluginId: 'app',
  extensions: [homePage],
});
