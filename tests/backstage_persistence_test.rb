require 'minitest/autorun'
require 'yaml'

class BackstagePersistenceTest < Minitest::Test
  def test_deployed_backstage_sqlite_uses_a_persistent_volume
    values = YAML.load_file('tenants/platform/backstage/base/helm-values.yaml').fetch('backstage')
    assert_equal 'better-sqlite3', values.dig('appConfig', 'backend', 'database', 'client')
    connection = values.dig('appConfig', 'backend', 'database', 'connection')
    assert_kind_of Hash, connection
    assert_equal '/app/data', connection['directory']
    assert_equal 1, values.fetch('replicas')
    assert_equal 1000, values.dig('podSecurityContext', 'fsGroup')

    mount = values.fetch('extraVolumeMounts').find { |entry| entry['mountPath'] == '/app/data' }
    refute_nil mount
    volume = values.fetch('extraVolumes').find { |entry| entry['name'] == mount['name'] }
    assert_equal 'backstage-data', volume.dig('persistentVolumeClaim', 'claimName')

    pvc = YAML.load_file('tenants/platform/backstage/base/configs/data-pvc.yaml')
    assert_equal 'PersistentVolumeClaim', pvc['kind']
    assert_equal 'backstage-data', pvc.dig('metadata', 'name')
    assert_equal 'backstage', pvc.dig('metadata', 'namespace')
    assert_includes YAML.load_file('tenants/platform/backstage/base/kustomization.yaml').fetch('resources'), 'configs/data-pvc.yaml'
  end
end
