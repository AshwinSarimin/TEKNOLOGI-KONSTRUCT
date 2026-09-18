import {
  coreServices,
  createBackendModule,
} from '@backstage/backend-plugin-api';
import { catalogProcessingExtensionPoint } from '@backstage/plugin-catalog-node';
import { GithubDiscoveryProcessor } from '@backstage/plugin-catalog-backend-module-github';

// @backstage/plugin-catalog-backend-module-github's default export only
// registers org/team entity-provider discovery — it does NOT register
// GithubDiscoveryProcessor, which is what the `type: github-discovery`
// catalog.locations entry (onboarded teams, helm-values.yaml) actually needs.
// That processor is exported but has to be wired up manually.
export const catalogModuleGithubDiscovery = createBackendModule({
  pluginId: 'catalog',
  moduleId: 'github-discovery-processor',
  register(env) {
    env.registerInit({
      deps: {
        catalogProcessing: catalogProcessingExtensionPoint,
        logger: coreServices.logger,
        config: coreServices.rootConfig,
      },
      async init({ catalogProcessing, logger, config }) {
        catalogProcessing.addProcessor(
          GithubDiscoveryProcessor.fromConfig(config, { logger }),
        );
      },
    });
  },
});
