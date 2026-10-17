# Collision-resistant Azure names with legacy preservation

## Goal and scope

Two valid application names such as `a-b` and `ab` must be able to request distinct Azure Key Vaults and Storage Accounts. The same application and environment must resolve to the same Azure name through standalone, Terraform, and approved team-onboarding Promises. Existing Azure names must remain stable on reconciliation. The current XR names and Terraform state-key pattern remain unchanged; their ownership reservations continue to prevent independent requests from sharing those targets.

The change covers Azure Key Vault names and Azure Storage Account names. Kubernetes namespaces, resource-group names, Backstage promise-flow testing, and the separate `private` namespace finding are outside this change.

## Options considered

1. Reject colliding application names at admission. Small, but leaves valid teams unable to provision a resource.
2. Add a deterministic hash suffix to new Azure names and use existing UID reservations to recognize legacy owners. **Selected:** resolves the collision without renaming active resources or adding a controller or database.
3. Allocate names from a central registry. Supports arbitrary aliases, but adds mutable mapping and a new lifecycle to this platform.

## Name derivation

For a new target, derive a stem by lowercasing the application name, removing characters outside `a-z0-9`, and taking at most the first six characters for Storage Accounts or nine for Key Vaults. Compute the first ten lowercase hexadecimal characters of SHA-256 over the UTF-8 bytes of `v2`, a NUL byte, resource kind (`storage` or `keyvault`), NUL, the **original case-sensitive** application name, NUL, and the environment.

- Storage Account: `tek` + storage stem + environment + digest + `sa` (at most 24 lowercase alphanumeric characters).
- Key Vault: `kv` + vault stem + environment + digest (at most 24 lowercase alphanumeric characters, beginning with a letter).

The digest distinguishes punctuation, case, and text beyond the truncated stem. Ten hexadecimal characters make an accidental collision unlikely; the existing atomic ownership gate remains the final guard if one occurs. The same kind, exact application name, and environment always produce the same candidate name.

## Legacy preservation and trust boundary

Each pipeline first computes its current legacy Azure name. Before using it, it reads the existing reservation ConfigMap for `azure-storage/<legacy-name>` or `azure-keyvault/<legacy-name>` in hub namespace `kratix-workloads`, using its existing Pipeline ServiceAccount. It uses the legacy name only when the ConfigMap's exact `data.target` matches and `data.ownerUID` equals the immutable UID in `/kratix/input/object.yaml`.

On HTTP 404, or when a valid reservation belongs to a different UID, the pipeline uses the new hashed name. A malformed claim, hash mismatch, API error, missing request UID, or unavailable Kubernetes API fails the workflow rather than risking a rename. The ownership gate then reserves the produced Azure target before Kratix publishes Work or Terraform applies. The Terraform gate resolves and claims the name before apply; the Terraform pipeline resolves it again and requires the selected target claim to still belong to the same UID immediately before invoking Terraform. If resolution changed between the two stages, apply fails without touching Azure. Its unchanged app/environment state-key claim still blocks another request for that state.

Existing live requests were seeded with UID reservations for their targets during finding #4. This rule therefore preserves their names on retry, while a newly created request, even with the same request name, has a new UID and receives a new Azure name only after any conflicting XR or Terraform state-key claim is released. A request that adds a resource for the first time uses the new naming rule for that resource. Legacy orphan reservations marked `legacy-unowned` are never adopted automatically.

## Implementation boundaries

Update the naming logic in standalone Key Vault, standalone Crossplane Storage Account, Terraform Storage Account, approved team-onboarding generation, and the Terraform ownership gate. Reuse the existing ConfigMap GET permission; do not add a dependency, controller, registry, or cross-cluster credential. Keep the four Promise outputs and Crossplane compositions otherwise unchanged. Document the operational naming rule and the fact that legacy names persist while their UID reservation exists.

## Verification and rollout

Write failing tests first for `a-b` versus `ab`, long names with the same truncated prefix, exact parity across all paths, legacy-UID preservation, foreign/missing reservations, and fail-closed API errors. Keep tests for Azure length and character restrictions. Run Go tests and vet, wiring tests, Docker builds, Kustomize rendering, and server-side Promise dry-runs.

Before rollout, inventory active request UIDs, workload XRs, and their reservations. Confirm the expected legacy claims exist for every active target. After the user's commit and push, verify CI image digests and GitOps revisions before live requests. Check `teknologi-eur1-prd-platform-rg` before Azure tests. Exercise two fresh app names that previously normalized to the same Azure name, verify distinct Azure resources, and verify an existing request still resolves to its prior Azure name. Remove test requests and verify downstream Azure resources, Terraform state, and reservations are cleaned according to their respective lifecycle rules. Keep Backstage promise-flow testing deferred.

## Failure behavior

If a claim lookup fails for reasons other than a clean 404, the Pipeline fails without publishing a changed Azure name. If the candidate hash name collides with another live claim, the ownership gate rejects the request before publication or apply. No migration automatically renames or adopts an existing Azure resource.
