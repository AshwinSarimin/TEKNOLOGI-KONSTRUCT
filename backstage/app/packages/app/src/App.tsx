import { createApp } from '@backstage/frontend-defaults';
import catalogPlugin from '@backstage/plugin-catalog/alpha';
import catalogGraphPlugin from '@backstage/plugin-catalog-graph/alpha';
import apiDocsPlugin from '@backstage/plugin-api-docs/alpha';
import scaffolderPlugin from '@backstage/plugin-scaffolder/alpha';
import techDocsPlugin from '@backstage/plugin-techdocs/alpha';
import searchPlugin from '@backstage/plugin-search/alpha';
import orgPlugin from '@backstage/plugin-org/alpha';
import userSettingsPlugin from '@backstage/plugin-user-settings/alpha';
import devtoolsPlugin from '@backstage/plugin-devtools/alpha';
import adrPlugin from '@backstage-community/plugin-adr/alpha';
import { techDocsReportIssueAddonModule } from '@backstage/plugin-techdocs-module-addons-contrib/alpha';
import { navModule } from './modules/nav';
import { themeModule } from './modules/theme';
import { homeModule } from './modules/home';
import { entityPagesModule } from './modules/entityPages';
import { signInModule } from './modules/auth';

export default createApp({
  features: [
    catalogPlugin,
    catalogGraphPlugin,
    apiDocsPlugin,
    scaffolderPlugin,
    techDocsPlugin,
    searchPlugin,
    orgPlugin,
    userSettingsPlugin,
    devtoolsPlugin,
    adrPlugin,
    techDocsReportIssueAddonModule,
    navModule,
    themeModule,
    homeModule,
    entityPagesModule,
    signInModule,
  ],
});
