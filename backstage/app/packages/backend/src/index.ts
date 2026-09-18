import { createBackend } from '@backstage/backend-defaults';
import { createServiceFactory, coreServices } from '@backstage/backend-plugin-api';
import { WinstonLogger } from '@backstage/backend-defaults/rootLogger';
import { format, transports } from 'winston';
import catalogModuleUnprocessedEntities from '@backstage/plugin-catalog-backend-module-unprocessed';
import { healthCheck } from './healthcheck';
import { scaffolderModuleKratixApply } from './scaffolderKratixApply';
import { permissionModulePlatformPolicy } from './permissionPolicy';
import { catalogModuleGithubDiscovery } from './catalogModuleGithubDiscovery';

const backend = createBackend();

// JSON structured logging — LOG_LEVEL env var controls verbosity
backend.add(
  createServiceFactory({
    service: coreServices.rootLogger,
    deps: {},
    async factory() {
      return WinstonLogger.create({
        level: process.env.LOG_LEVEL || 'info',
        format: format.json(),
        transports: [new transports.Console()],
      });
    },
  }),
);

backend.add(import('@backstage/plugin-app-backend'));
backend.add(import('@backstage/plugin-proxy-backend'));

// scaffolder plugin
backend.add(import('@backstage/plugin-scaffolder-backend'));
backend.add(import('@backstage/plugin-scaffolder-backend-module-github'));
backend.add(scaffolderModuleKratixApply);

// techdocs plugin
backend.add(import('@backstage/plugin-techdocs-backend'));

// auth plugin — guest removed (RBAC hardening); Entra ID and GitHub are both
// offered on the sign-in page (see app/src/modules/auth) for real per-user
// identity, resolved against catalog User annotations
backend.add(import('@backstage/plugin-auth-backend'));
backend.add(import('@backstage/plugin-auth-backend-module-microsoft-provider'));
backend.add(import('@backstage/plugin-auth-backend-module-github-provider'));

// catalog plugin
backend.add(import('@backstage/plugin-catalog-backend'));
backend.add(
  import('@backstage/plugin-catalog-backend-module-scaffolder-entity-model'),
);
backend.add(import('@backstage/plugin-catalog-backend-module-logs'));
backend.add(catalogModuleUnprocessedEntities);
// github-discovery location type — onboarded teams' Group entities in
// ORCHESTRATION are found by glob instead of one hardcoded URL per team.
// The module's default export only wires up org/team entity-provider
// discovery, NOT the GithubDiscoveryProcessor that github-discovery locations
// actually need — that has to be registered separately.
backend.add(import('@backstage/plugin-catalog-backend-module-github'));
backend.add(catalogModuleGithubDiscovery);

// ADR backend — enables ADR tab on entity pages
backend.add(import('@backstage-community/plugin-adr-backend'));

// permission plugin — custom policy gates Flow B templates by team membership
backend.add(import('@backstage/plugin-permission-backend'));
backend.add(permissionModulePlatformPolicy);

// search plugin — uses Lunr (in-memory) by default, no additional engine module needed
backend.add(import('@backstage/plugin-search-backend'));
backend.add(import('@backstage/plugin-search-backend-module-catalog'));
backend.add(import('@backstage/plugin-search-backend-module-techdocs'));

// kubernetes plugin — surfaces CRD status on entity pages
backend.add(import('@backstage/plugin-kubernetes-backend'));

// custom health endpoint — used by Kubernetes liveness/readiness probes
backend.add(healthCheck);

backend.start();
