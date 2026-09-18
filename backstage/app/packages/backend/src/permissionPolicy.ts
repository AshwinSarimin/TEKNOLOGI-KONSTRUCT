import { createBackendModule } from '@backstage/backend-plugin-api';
import {
  AuthorizeResult,
  isResourcePermission,
  PolicyDecision,
} from '@backstage/plugin-permission-common';
import {
  PermissionPolicy,
  PolicyQuery,
  PolicyQueryUser,
} from '@backstage/plugin-permission-node';
import { policyExtensionPoint } from '@backstage/plugin-permission-node/alpha';
import {
  createScaffolderTemplateConditionalDecision,
  scaffolderTemplateConditions,
} from '@backstage/plugin-scaffolder-backend/alpha';
import {
  catalogConditions,
  createCatalogConditionalDecision,
} from '@backstage/plugin-catalog-backend/alpha';

/**
 * Flow B gating: templates marked `features-only` are visible/executable only by
 * members of the Features or RE teams; `platform-only` requires Fundamentals.
 * Enforced twice: the `teknologi.io/access` label hides the entity from catalog
 * lists (which is what the Create page and Catalog page render), and the tag of
 * the same name blocks scaffolder parameter/step access as defense in depth.
 * Everything unmarked stays open to everyone.
 */
class PlatformPermissionPolicy implements PermissionPolicy {
  async handle(
    request: PolicyQuery,
    user?: PolicyQueryUser,
  ): Promise<PolicyDecision> {
    const ownership = user?.info.ownershipEntityRefs ?? [];
    const memberOfAny = (groups: string[]) =>
      groups.some(g => ownership.includes(`group:default/${g}`));
    const deniedAccessLevels = [
      ...(memberOfAny(['features', 're']) ? [] : ['features-only']),
      ...(memberOfAny(['fundamentals']) ? [] : ['platform-only']),
    ];

    if (deniedAccessLevels.length === 0) {
      return { result: AuthorizeResult.ALLOW };
    }

    if (isResourcePermission(request.permission, 'catalog-entity')) {
      const restrictions = deniedAccessLevels.map(level => ({
        not: catalogConditions.hasLabel({
          label: 'teknologi.io/access',
          value: level,
        }),
      }));
      return createCatalogConditionalDecision(request.permission, {
        allOf: [restrictions[0], ...restrictions.slice(1)],
      });
    }

    if (isResourcePermission(request.permission, 'scaffolder-template')) {
      const restrictions = deniedAccessLevels.map(level => ({
        not: scaffolderTemplateConditions.hasTag({ tag: level }),
      }));
      return createScaffolderTemplateConditionalDecision(request.permission, {
        allOf: [restrictions[0], ...restrictions.slice(1)],
      });
    }

    return { result: AuthorizeResult.ALLOW };
  }
}

export const permissionModulePlatformPolicy = createBackendModule({
  pluginId: 'permission',
  moduleId: 'platform-policy',
  register(reg) {
    reg.registerInit({
      deps: { policy: policyExtensionPoint },
      async init({ policy }) {
        policy.setPolicy(new PlatformPermissionPolicy());
      },
    });
  },
});
