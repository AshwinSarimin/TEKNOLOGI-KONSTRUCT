require 'minitest/autorun'
require 'yaml'

class ClusterSecretStoreTest < Minitest::Test
  ROOT = File.expand_path('..', __dir__)
  STORE = 'platform-secret-store'

  def test_only_platform_namespaces_can_use_the_store
    store = YAML.load_file(File.join(ROOT, 'tenants/platform/external-secrets/base/configs/cluster-secret-store.yaml'))
    conditions = store.fetch('spec').fetch('conditions')
    assert_equal 1, conditions.length
    assert_equal ['namespaces'], conditions.first.keys

    allowed = conditions.first.fetch('namespaces')
    assert_equal %w[argocd backstage cert-manager crossplane-system kratix-platform-system kratix-workloads rest-api], allowed.sort
    refute_includes allowed, 'app-a-dev'
    refute_includes allowed, 'default'

    consumers = Dir.glob(File.join(ROOT, 'tenants/platform/**/*.yaml')).map do |file|
      YAML.load_stream(File.read(file)).map do |resource|
        next unless resource.is_a?(Hash) && resource['kind'] == 'ExternalSecret'
        next unless resource.dig('spec', 'secretStoreRef', 'name') == STORE

        namespace = resource.dig('metadata', 'namespace')
        assert namespace, "#{file} must declare its namespace"
        namespace
      end.compact
    end.flatten.uniq.sort

    assert_equal allowed.sort, consumers
  end
end
