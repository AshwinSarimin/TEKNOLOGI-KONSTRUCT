import { AuthorizeResult } from '@backstage/plugin-permission-common';
import type {
  PolicyQuery,
  PolicyQueryUser,
} from '@backstage/plugin-permission-node';
import { PlatformPermissionPolicy } from './permissionPolicy';

const scaffolderRead: PolicyQuery = {
  permission: {
    name: 'scaffolder.template.parameter.read',
    attributes: { action: 'read' },
    type: 'resource',
    resourceType: 'scaffolder-template',
  },
};

const catalogRead: PolicyQuery = {
  permission: {
    name: 'catalog.entity.read',
    attributes: { action: 'read' },
    type: 'resource',
    resourceType: 'catalog-entity',
  },
};

function user(groups: string[]): PolicyQueryUser {
  return {
    info: {
      userEntityRef: 'user:default/test-user',
      ownershipEntityRefs: groups.map(group => `group:default/${group}`),
    },
  } as PolicyQueryUser;
}

function expectedRestrictions(
  resourceType: 'catalog-entity' | 'scaffolder-template',
  accessLevels: string[],
) {
  const catalog = resourceType === 'catalog-entity';
  return {
    result: AuthorizeResult.CONDITIONAL,
    pluginId: catalog ? 'catalog' : 'scaffolder',
    resourceType,
    conditions: {
      allOf: accessLevels.map(level => ({
        not: {
          rule: catalog ? 'HAS_LABEL' : 'HAS_TAG',
          resourceType,
          params: catalog
            ? { label: 'teknologi.io/access', value: level }
            : { tag: level },
        },
      })),
    },
  };
}

it.each([
  { request: scaffolderRead, resourceType: 'scaffolder-template' as const },
  { request: catalogRead, resourceType: 'catalog-entity' as const },
])(
  'restricts both access labels for a nonmember on $resourceType',
  async ({ request, resourceType }) => {
    const decision = await new PlatformPermissionPolicy().handle(
      request,
      user([]),
    );
    expect(decision).toEqual(
      expectedRestrictions(resourceType, ['features-only', 'platform-only']),
    );
  },
);

it('fails closed when no user identity is supplied', async () => {
  const decision = await new PlatformPermissionPolicy().handle(scaffolderRead);
  expect(decision).toEqual(
    expectedRestrictions('scaffolder-template', [
      'features-only',
      'platform-only',
    ]),
  );
});

it.each([
  { groups: ['product'], denied: 'platform-only' },
  { groups: ['research'], denied: 'platform-only' },
  { groups: ['platform-core'], denied: 'features-only' },
])('restricts only $denied for $groups', async ({ groups, denied }) => {
  const decision = await new PlatformPermissionPolicy().handle(
    scaffolderRead,
    user(groups),
  );
  expect(decision).toEqual(
    expectedRestrictions('scaffolder-template', [denied]),
  );
});

it('allows a member of both authorized groups', async () => {
  const decision = await new PlatformPermissionPolicy().handle(
    scaffolderRead,
    user(['product', 'platform-core']),
  );
  expect(decision).toEqual({ result: AuthorizeResult.ALLOW });
});
