require 'minitest/autorun'
require 'yaml'

class GitopsRevisionTest < Minitest::Test
  REPO = 'https://github.com/AshwinSarimin/TEKNOLOGI-KONSTRUCT.git'
  FILES = %w[
    bootstrap/k3d-teknologi-hub-cluster.yaml
    bootstrap/k3d-teknologi-workload-cluster.yaml
    appsets/base/argocd-configs.yaml
    appsets/base/tenant-platform-configs.yaml
    appsets/base/tenant-platform-controllers.yaml
    appsets/hub/kratix-promises.yaml
  ].freeze

  def test_platform_gitops_sources_follow_main
    FILES.each do |file|
      documents = YAML.load_stream(File.read(File.expand_path("../#{file}", __dir__)))
      sources = documents.flat_map { |document| git_sources(document) }
      assert_equal 1, sources.length, "expected one platform Git source in #{file}"
      assert_equal 'main', sources.first.fetch('targetRevision'), file
    end
  end

  private

  def git_sources(value)
    case value
    when Hash
      found = value['repoURL'] == REPO ? [value] : []
      found + value.values.flat_map { |child| git_sources(child) }
    when Array
      value.flat_map { |child| git_sources(child) }
    else
      []
    end
  end
end
