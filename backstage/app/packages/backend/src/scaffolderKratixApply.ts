import {
  AuthService,
  coreServices,
  createBackendModule,
} from '@backstage/backend-plugin-api';
import {
  createTemplateAction,
  scaffolderActionsExtensionPoint,
} from '@backstage/plugin-scaffolder-node';
import {
  KubeConfig,
  KubernetesObjectApi,
  KubernetesObject,
} from '@kubernetes/client-node';
import { isDeepStrictEqual } from 'node:util';

const ownerAnnotation = 'teknologi.io/request-owner';
const requestNamespace = 'kratix-workloads';
const requestKinds = new Set([
  'TeamOnboardingRequest',
  'NamespaceRequest',
  'KeyVaultRequest',
  'StorageAccountRequest',
  'StorageAccountTerraformRequest',
]);

type PromiseRequest = KubernetesObject & { spec: Record<string, unknown> };

function kubernetesCode(error: unknown): number | undefined {
  return typeof error === 'object' && error !== null && 'code' in error
    ? (error as { code?: number }).code
    : undefined;
}

// Templates provide only the request spec and display labels. Ownership comes
// exclusively from the authenticated task initiator, not ctx.user or input.
export function createKratixApplyAction(auth: AuthService) {
  return createTemplateAction({
    id: 'kratix:apply',
    description:
      'Creates or updates a Kratix request owned by the authenticated user',
    schema: {
      input: {
        manifest: z =>
          z
            .record(z.any())
            .describe(
              'Full resource manifest (apiVersion, kind, metadata, spec)',
            ),
      },
    },
    async handler(ctx) {
      const credentials = await ctx.getInitiatorCredentials();
      if (
        !auth.isPrincipal(credentials, 'user') ||
        !credentials.principal.userEntityRef
      ) {
        throw new Error(
          'An authenticated user is required to submit a Kratix request',
        );
      }
      const owner = `backstage:${credentials.principal.userEntityRef}`;
      const manifest = ctx.input.manifest as PromiseRequest;
      const name = manifest.metadata?.name;
      if (
        manifest.apiVersion !== 'marketplace.kratix.io/v1alpha1' ||
        !requestKinds.has(manifest.kind ?? '') ||
        manifest.metadata?.namespace !== requestNamespace ||
        typeof name !== 'string' ||
        name.length > 253 ||
        !/^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$/.test(
          name,
        ) ||
        !manifest.spec ||
        typeof manifest.spec !== 'object' ||
        Array.isArray(manifest.spec)
      ) {
        throw new Error(
          'Expected a named Promise request with a spec object in kratix-workloads',
        );
      }
      if (
        Object.keys(manifest.metadata ?? {}).some(
          key => !['name', 'namespace', 'labels'].includes(key),
        )
      ) {
        throw new Error(
          'Request metadata only supports name, namespace and labels; ownership is assigned by the broker',
        );
      }

      const kubeConfig = new KubeConfig();
      kubeConfig.loadFromCluster();
      const api = KubernetesObjectApi.makeApiClient(kubeConfig);

      const ref = `${manifest.kind}/${name}`;
      const object: PromiseRequest = {
        apiVersion: manifest.apiVersion,
        kind: manifest.kind,
        metadata: {
          name,
          namespace: requestNamespace,
          labels: manifest.metadata?.labels,
          annotations: { [ownerAnnotation]: owner },
        },
        spec: manifest.spec,
      };

      let existing: PromiseRequest | undefined;
      try {
        existing = await api.read<PromiseRequest>({
          apiVersion: object.apiVersion,
          kind: object.kind,
          metadata: { name, namespace: requestNamespace },
        });
      } catch (error) {
        if (kubernetesCode(error) !== 404) {
          throw new Error(`Failed to read ${ref}; request was not written`);
        }
      }
      if (existing) {
        if (existing.metadata?.annotations?.[ownerAnnotation] !== owner) {
          throw new Error(
            `Request ${ref} is not owned by this caller; administrator review required for unowned requests`,
          );
        }
        if (existing.metadata?.deletionTimestamp) {
          throw new Error(`Request ${ref} is being deleted`);
        }
        if (isDeepStrictEqual(existing.spec, object.spec)) {
          ctx.logger.info(`Request ${ref} is unchanged`);
          return;
        }
      }
      try {
        if (existing) {
          // Retain owner, UID, resourceVersion and all controller metadata.
          // Kubernetes rejects concurrent writes or deletion/recreation.
          await api.replace({ ...existing, spec: object.spec });
        } else {
          await api.create(object);
        }
      } catch (error) {
        const code = kubernetesCode(error);
        if (code === 409 || code === 404) {
          throw new Error(
            `Request ${ref} changed concurrently; retry to recheck ownership`,
          );
        }
        throw new Error(
          `Failed to write ${ref} (Kubernetes status ${code ?? 'unknown'})`,
        );
      }
      ctx.logger.info(
        `${
          existing ? 'Updated' : 'Created'
        } ${ref} in namespace ${requestNamespace}`,
      );
    },
  });
}

export const scaffolderModuleKratixApply = createBackendModule({
  pluginId: 'scaffolder',
  moduleId: 'kratix-apply',
  register(env) {
    env.registerInit({
      deps: {
        scaffolder: scaffolderActionsExtensionPoint,
        auth: coreServices.auth,
      },
      init: async ({ scaffolder, auth }) => {
        scaffolder.addActions(createKratixApplyAction(auth));
      },
    });
  },
});
