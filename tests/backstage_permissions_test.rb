require 'minitest/autorun'
require 'yaml'

class BackstagePermissionsTest < Minitest::Test
  RESTRICTED = {
    'keyvault-request' => 'features-only',
    'storage-account-request' => 'platform-only'
  }.freeze

  def test_restricted_templates_tag_every_parameter_and_step_for_scaffolder_authorization
    RESTRICTED.each do |template_name, access_tag|
      template = YAML.load_file("backstage/templates/#{template_name}.yaml")
      assert_equal access_tag, template.dig('metadata', 'labels', 'teknologi.io/access')

      template.fetch('spec').fetch('parameters').each do |parameter|
        assert_includes parameter.dig('backstage:permissions', 'tags') || [], access_tag,
                        "#{template_name} parameter #{parameter['title']} lacks execution tag"
      end
      template.fetch('spec').fetch('steps').each do |step|
        assert_includes step.dig('backstage:permissions', 'tags') || [], access_tag,
                        "#{template_name} step #{step['id']} lacks execution tag"
      end
    end
  end
end
