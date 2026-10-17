require 'minitest/autorun'
require 'yaml'

class BackstageCatalogTest < Minitest::Test
  def test_noop_capabilities_template_is_not_published
    values = YAML.load_file('tenants/platform/backstage/base/helm-values.yaml')
    locations = values.dig('backstage', 'appConfig', 'catalog', 'locations').map { |entry| entry['target'] }
    refute locations.any? { |target| target&.end_with?('/backstage/templates/capabilities-request.yaml') }
  end
end
