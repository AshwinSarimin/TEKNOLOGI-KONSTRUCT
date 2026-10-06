import { KubernetesObjectApi } from '@kubernetes/client-node';
import type { AuthService } from '@backstage/backend-plugin-api';
import { createKratixApplyAction } from './scaffolderKratixApply';

jest.mock('@kubernetes/client-node', () => ({
  KubeConfig: jest
    .fn()
    .mockImplementation(() => ({ loadFromCluster: jest.fn() })),
  KubernetesObjectApi: { makeApiClient: jest.fn() },
}));

const ownerKey = 'teknologi.io/request-owner';
const request = () => ({
  apiVersion: 'marketplace.kratix.io/v1alpha1',
  kind: 'KeyVaultRequest',
  metadata: {
    name: 'example',
    namespace: 'kratix-workloads',
    labels: { 'backstage.io/kubernetes-id': 'example' },
  },
  spec: { appName: 'example', environment: 'dev' },
});

function setup() {
  const api = {
    read: jest.fn().mockRejectedValue({ code: 404 }),
    create: jest.fn().mockImplementation(async object => object),
    replace: jest.fn().mockImplementation(async object => object),
  };
  jest
    .mocked(KubernetesObjectApi.makeApiClient)
    .mockReturnValue(api as unknown as KubernetesObjectApi);
  const auth = {
    isPrincipal: (credentials: { principal: { type: string } }, type: string) =>
      credentials.principal.type === type,
  } as unknown as AuthService;
  const action = createKratixApplyAction(auth);
  const ctx = {
    input: { manifest: request() },
    // Deliberately disagree: the owner must come from authenticated credentials.
    user: { ref: 'user:default/bob' },
    getInitiatorCredentials: jest.fn().mockResolvedValue({
      $$type: '@backstage/BackstageCredentials',
      principal: { type: 'user', userEntityRef: 'user:default/alice' },
    }),
    logger: { info: jest.fn() },
  };
  const run = () =>
    action.handler(ctx as unknown as Parameters<typeof action.handler>[0]);
  return { api, ctx, run };
}

it('stamps the authenticated initiator as owner on the Kubernetes request', async () => {
  const { api, run } = setup();
  await run();
  expect(api.create.mock.calls[0][0].metadata.annotations).toEqual({
    [ownerKey]: 'backstage:user:default/alice',
  });
});

it.each([
  'KeyVaultRequest',
  'NamespaceRequest',
  'StorageAccountRequest',
  'StorageAccountTerraformRequest',
  'TeamOnboardingRequest',
])('creates and stamps an owner for %s', async kind => {
  const { api, ctx, run } = setup();
  ctx.input.manifest.kind = kind;
  await run();
  expect(api.create.mock.calls[0][0]).toMatchObject({
    kind,
    metadata: {
      namespace: 'kratix-workloads',
      annotations: { [ownerKey]: 'backstage:user:default/alice' },
    },
  });
});

it.each(['', 'backstage:user:default/bob', 'rest-api:alice'])(
  'rejects an existing request with owner %j without writing',
  async owner => {
    const { api, run } = setup();
    api.read.mockResolvedValue({
      ...request(),
      metadata: { ...request().metadata, annotations: { [ownerKey]: owner } },
    });
    await expect(run()).rejects.toThrow(/not owned/);
    expect(api.create).not.toHaveBeenCalled();
    expect(api.replace).not.toHaveBeenCalled();
  },
);

function ownedRequest() {
  return {
    ...request(),
    metadata: {
      ...request().metadata,
      uid: 'original-uid',
      resourceVersion: '7',
      annotations: {
        [ownerKey]: 'backstage:user:default/alice',
        'controller.example/keep': 'yes',
      },
      finalizers: ['platform.kratix.io/resource-cleanup'],
    },
    status: { message: 'controller owned' },
  };
}

it('treats a same-owner identical retry as success without rewriting the request', async () => {
  const { api, run } = setup();
  api.read.mockResolvedValue(ownedRequest());
  await run();
  expect(api.create).not.toHaveBeenCalled();
  expect(api.replace).not.toHaveBeenCalled();
});

it('updates only spec while retaining owner, resourceVersion, UID and controller metadata', async () => {
  const { api, ctx, run } = setup();
  const existing = ownedRequest();
  api.read.mockResolvedValue(existing);
  ctx.input.manifest.spec.environment = 'prd';
  await run();
  expect(api.replace.mock.calls[0][0]).toEqual({
    ...existing,
    spec: { appName: 'example', environment: 'prd' },
  });
  expect(api.create).not.toHaveBeenCalled();
});

it.each([409, 404])(
  'surfaces update race %s without falling back to create or patch',
  async code => {
    const { api, ctx, run } = setup();
    api.read.mockResolvedValue(ownedRequest());
    api.replace.mockRejectedValue({ code });
    ctx.input.manifest.spec.environment = 'prd';
    await expect(run()).rejects.toThrow(/changed concurrently/);
    expect(api.replace).toHaveBeenCalledTimes(1);
    expect(api.create).not.toHaveBeenCalled();
  },
);

it('surfaces a create race without overwriting the competing request', async () => {
  const { api, run } = setup();
  api.create.mockRejectedValue({ code: 409 });
  await expect(run()).rejects.toThrow(/changed concurrently/);
  expect(api.create).toHaveBeenCalledTimes(1);
  expect(api.replace).not.toHaveBeenCalled();
});

it.each([
  { type: 'none' },
  { type: 'service', subject: 'plugin:scaffolder' },
  { type: 'user', userEntityRef: '' },
])('requires an authenticated user, got %j', async principal => {
  const { api, ctx, run } = setup();
  ctx.getInitiatorCredentials.mockResolvedValue({ principal });
  await expect(run()).rejects.toThrow(/authenticated user/);
  expect(api.read).not.toHaveBeenCalled();
  expect(api.create).not.toHaveBeenCalled();
});

it('fails closed when initiator credentials cannot be obtained', async () => {
  const { api, ctx, run } = setup();
  ctx.getInitiatorCredentials.mockRejectedValue(
    new Error('expired credentials'),
  );
  await expect(run()).rejects.toThrow('expired credentials');
  expect(api.create).not.toHaveBeenCalled();
});

it.each([
  ['apiVersion', 'v1'],
  ['kind', 'Secret'],
  ['spec', null],
  ['spec', []],
  ['metadata', { name: 'example', namespace: 'default' }],
  ['metadata', { name: '../other', namespace: 'kratix-workloads' }],
  ['metadata', { generateName: 'example-', namespace: 'kratix-workloads' }],
  [
    'metadata',
    {
      ...request().metadata,
      annotations: { [ownerKey]: 'backstage:user:default/bob' },
    },
  ],
  [
    'metadata',
    { ...request().metadata, finalizers: ['attacker.example/hold'] },
  ],
])(
  'rejects unsafe manifest %s=%j before Kubernetes calls',
  async (key, value) => {
    const { api, ctx, run } = setup();
    ctx.input.manifest = { ...request(), [key as string]: value } as ReturnType<
      typeof request
    >;
    await expect(run()).rejects.toThrow();
    expect(api.read).not.toHaveBeenCalled();
    expect(api.create).not.toHaveBeenCalled();
    expect(api.replace).not.toHaveBeenCalled();
  },
);

it('rejects requests being deleted', async () => {
  const { api, run } = setup();
  const existing = ownedRequest();
  api.read.mockResolvedValue({
    ...existing,
    metadata: {
      ...existing.metadata,
      deletionTimestamp: '2026-10-04T00:00:00Z',
    },
  });
  await expect(run()).rejects.toThrow(/being deleted/);
  expect(api.replace).not.toHaveBeenCalled();
});

it('fails closed on Kubernetes read failures', async () => {
  const { api, run } = setup();
  api.read.mockRejectedValue({ code: 503 });
  await expect(run()).rejects.toThrow(/read/);
  expect(api.create).not.toHaveBeenCalled();
  expect(api.replace).not.toHaveBeenCalled();
});
