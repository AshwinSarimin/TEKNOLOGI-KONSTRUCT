import {
  createBackendModule,
} from '@backstage/backend-plugin-api';
import {
  createTemplateAction,
  scaffolderActionsExtensionPoint,
} from '@backstage/plugin-scaffolder-node';
import { KubeConfig, KubernetesObjectApi, KubernetesObject } from '@kubernetes/client-node';

// Generic action for creating any Kubernetes/Kratix resource on the cluster Backstage
// itself runs in (in-cluster service account auth via loadFromCluster()). KubernetesObjectApi
// derives group/version/plural from the manifest's apiVersion+kind, so this works for any
// resource type — used by the onboarding wizard for TeamOnboardingRequest, reusable for
// other Promise CRDs later (Phase 4.2 individual templates).
function createKratixApplyAction() {
  return createTemplateAction({
    id: 'kratix:apply',
    description: 'Creates a Kratix Promise resource request on the hub cluster',
    schema: {
      input: {
        manifest: (z) =>
          z.record(z.any()).describe('Full resource manifest (apiVersion, kind, metadata, spec)'),
      },
    },
    async handler(ctx) {
      const manifest = ctx.input.manifest as KubernetesObject;

      const kubeConfig = new KubeConfig();
      kubeConfig.loadFromCluster();
      const api = KubernetesObjectApi.makeApiClient(kubeConfig);

      const ref = `${manifest.kind}/${manifest.metadata?.name}`;
      ctx.logger.info(`Creating ${ref} in namespace ${manifest.metadata?.namespace}`);

      try {
        await api.create(manifest);
      } catch (e) {
        throw new Error(`Failed to create ${ref}: ${e instanceof Error ? e.message : String(e)}`);
      }
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
      },
      init: async ({ scaffolder }) => {
        scaffolder.addActions(createKratixApplyAction());
      },
    });
  },
});
