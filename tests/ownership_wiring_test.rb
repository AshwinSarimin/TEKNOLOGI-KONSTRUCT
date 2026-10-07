require 'minitest/autorun'
require 'yaml'

class OwnershipWiringTest < Minitest::Test
  ROOT = File.expand_path('..', __dir__)

  def containers(promise, pipeline_index = 0)
    manifest = YAML.load_file(File.join(ROOT, "promises/#{promise}/promise.yaml"))
    manifest.fetch('spec').fetch('workflows').fetch('resource').fetch('configure').fetch(pipeline_index).fetch('spec').fetch('containers').map { |c| c.fetch('name') }
  end

  def test_crossplane_outputs_are_reserved_before_work_writer
    {
      'namespace' => 'configure-namespace',
      'keyvault' => 'configure-keyvault',
      'storage-account' => 'configure-storage-account'
    }.each do |promise, configure|
      assert_equal [configure, 'ownership-gate', 'notify'], containers(promise)
    end
    assert_equal ['pr-watcher', 'ownership-gate'], containers('team-onboarding', 1)
  end

  def test_terraform_reserves_before_apply
    assert_equal ['ownership-gate', 'configure-tf'], containers('storage-account-terraform')
  end

  def test_terraform_delete_verifies_owner_before_destroy
    manifest = YAML.load_file(File.join(ROOT, 'promises/storage-account-terraform/promise.yaml'))
    deletion = manifest.fetch('spec').fetch('workflows').fetch('resource').fetch('delete').first
    assert_equal ['ownership-gate', 'destroy-tf'], deletion.fetch('spec').fetch('containers').map { |c| c.fetch('name') }
    gate = deletion.fetch('spec').fetch('containers').first
    assert_equal 'verify', gate.fetch('env').find { |entry| entry.fetch('name') == 'OWNERSHIP_MODE' }.fetch('value')
  end

  def test_only_pipeline_service_accounts_can_create_reservations
    docs = YAML.load_stream(File.read(File.join(ROOT, 'tenants/platform/kratix/base/configs/workload/ownership-rbac.yaml')))
    role = docs.find { |d| d['kind'] == 'Role' }
    binding = docs.find { |d| d['kind'] == 'RoleBinding' }
    assert_equal 'kratix-workloads', role.dig('metadata', 'namespace')
    assert_equal ['configmaps'], role.dig('rules', 0, 'resources')
    assert_equal %w[create delete get], role.dig('rules', 0, 'verbs').sort
    assert_equal %w[
      keyvault-resource-configure-configure-keyvault
      namespace-resource-configure-configure-namespace
      storage-account-resource-configure-configure-storage-account
      storage-account-terraform-resource-configure-configure-tf
      storage-account-terraform-resource-delete-destroy-tf
      team-onboarding-resource-configure-pr-watcher
    ].sort, binding.fetch('subjects').map { |s| s.fetch('name') }.sort
  end
end
