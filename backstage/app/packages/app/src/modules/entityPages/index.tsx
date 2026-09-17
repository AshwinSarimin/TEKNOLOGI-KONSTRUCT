import { jsx } from 'react/jsx-runtime';
import Grid from '@material-ui/core/Grid';
import { createFrontendModule } from '@backstage/frontend-plugin-api';
import { EntityContentBlueprint } from '@backstage/plugin-catalog-react/alpha';
import {
  EntityDependsOnComponentsCard,
  EntityDependsOnResourcesCard,
} from '@backstage/plugin-catalog';
import { EntityCatalogGraphCard, Direction } from '@backstage/plugin-catalog-graph';

// The catalog/api-docs/org/techdocs/adr plugins already deliver most of the
// per-kind layout (Overview cards, API tab, Docs tab, ADR tab, Group/User
// profile cards, orphan/relation/processing-error warnings) once registered
// as features in App.tsx — see catalogGraphPlugin/apiDocsPlugin there. These
// two tabs are the only pieces without an out-of-the-box equivalent.

const dependenciesEntityContent = EntityContentBlueprint.make({
  name: 'dependencies',
  params: {
    path: '/dependencies',
    title: 'Dependencies',
    group: 'overview',
    filter: { kind: 'component' },
    loader: async () =>
      jsx(Grid, {
        container: true,
        spacing: 3,
        children: [
          jsx(Grid, { item: true, xs: 12, md: 6, children: jsx(EntityDependsOnComponentsCard, { variant: 'gridItem' }) }, 'components'),
          jsx(Grid, { item: true, xs: 12, md: 6, children: jsx(EntityDependsOnResourcesCard, { variant: 'gridItem' }) }, 'resources'),
        ],
      }),
  },
});

const diagramEntityContent = EntityContentBlueprint.make({
  name: 'diagram',
  params: {
    path: '/diagram',
    title: 'Diagram',
    group: 'overview',
    filter: { kind: 'system' },
    loader: async () =>
      jsx(EntityCatalogGraphCard, {
        variant: 'gridItem',
        direction: Direction.TOP_BOTTOM,
        title: 'System diagram',
        height: 700,
        relations: [
          'ownerOf',
          'ownedBy',
          'consumesApi',
          'apiConsumedBy',
          'providesApi',
          'apiProvidedBy',
          'hasPart',
          'partOf',
          'dependsOn',
          'dependencyOf',
        ],
        kinds: ['component', 'system', 'api', 'domain', 'resource'],
      }),
  },
});

export const entityPagesModule = createFrontendModule({
  pluginId: 'catalog',
  extensions: [dependenciesEntityContent, diagramEntityContent],
});
