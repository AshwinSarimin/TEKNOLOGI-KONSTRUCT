import {
  coreServices,
  createBackendPlugin,
} from '@backstage/backend-plugin-api';

export const healthCheck = createBackendPlugin({
  pluginId: 'healthcheck',
  register(env) {
    env.registerInit({
      deps: {
        rootHttpRouter: coreServices.rootHttpRouter,
      },
      init: async ({ rootHttpRouter }) => {
        rootHttpRouter.use('/healthcheck', (_, res) => {
          res.json({ status: 'ok' });
        });
      },
    });
  },
});
