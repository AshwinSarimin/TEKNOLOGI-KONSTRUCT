import { jsx } from 'react/jsx-runtime';
import { createFrontendModule } from '@backstage/frontend-plugin-api';
import { SignInPageBlueprint } from '@backstage/plugin-app-react';
import { SignInPage } from '@backstage/core-components';
import { githubAuthApiRef, microsoftAuthApiRef } from '@backstage/core-plugin-api';

// Overrides plugin-app's DefaultSignInPage (guest-only) — guest auth is
// removed entirely (RBAC hardening: no more anonymous/impersonated identity).
// Both Entra ID and GitHub resolve to a real catalog User via their configured
// resolvers (backend/src/index.ts): Entra ID matches microsoft.com/email,
// GitHub matches github.com/user-id (see annotations in
// backstage/catalog/groups.yaml). Only ashwin-sarimin carries both
// annotations — the other demo users (alex-de-vries, sara-bergman,
// kai-lindqvist) have no real account with either provider.
const signInModule = createFrontendModule({
  pluginId: 'app',
  extensions: [
    SignInPageBlueprint.make({
      params: {
        loader: async () => props =>
          jsx(SignInPage, {
            ...props,
            providers: [
              {
                id: 'microsoft',
                title: 'Entra ID',
                message: 'Sign in with your Microsoft Entra ID account',
                apiRef: microsoftAuthApiRef,
              },
              {
                id: 'github',
                title: 'GitHub',
                message: 'Sign in with your GitHub account',
                apiRef: githubAuthApiRef,
              },
            ],
          }),
      },
    }),
  ],
});

export { signInModule };
