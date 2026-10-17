# Collision-resistant Azure Names Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. Ashwin handles commits and pushes; do not commit or push.

**Goal:** Give distinct valid application names distinct Azure Key Vault and Storage Account names while retaining every active request's existing Azure name.

**Architecture:** New targets use a deterministic, ten-hex-character SHA-256 suffix. Existing requests keep a legacy name only when its Azure-target ConfigMap reservation is owned by their immutable request UID. Each pipeline resolves before producing an XR or applying Terraform; the Terraform apply stage rechecks its resolved claim after the gate to close the inter-container race.

**Tech Stack:** Go 1.21, Kratix Pipelines, Kubernetes ConfigMaps/RBAC, Crossplane, Terraform, Ruby Minitest, Docker, Kustomize.

**Spec:** `docs/superpowers/specs/2026-10-12-resource-naming-design.md`

## Global Constraints

- Keep XR names and `storage-account-{appName}-{environment}.tfstate` state keys unchanged.
- Keep existing Azure names whenever the request UID owns the legacy Azure-target reservation.
- New Storage Account names: `tek` + first six normalized app characters + environment + first ten SHA-256 hex characters + `sa`.
- New Key Vault names: `kv` + first nine normalized app characters + environment + first ten SHA-256 hex characters.
- Digest input: UTF-8 `v2\x00<kind>\x00<exact appName>\x00<environment>`, where kind is `storage` or `keyvault`.
- Claim lookup uses only the existing `get` permission in hub namespace `kratix-workloads`; malformed claims and API errors fail closed.
- No new service, controller, registry, credential, or dependency. Do not commit or push. Keep Backstage promise-flow testing deferred.

## Review Focus

1. **Case-only app difference:** `case` and `Case` have the same stem but must have different digest-backed names; test in Task 1.
2. **Long-name truncation:** names with the same retained stem must differ; test in Task 1.
3. **Legacy claim missing or owned by another UID:** choose the new name, while a malformed claim or API failure must stop; test in Task 1.
4. **Terraform gate/apply race:** if the resolved target claim changes between stages, apply must stop before `terraform init`; test in Task 4.
5. **Onboarding approval pending:** no Azure name is resolved or claimed until the approved watcher produces XRs; test in Task 3.

## File map

- `promises/keyvault/workflows/resource/configure/azure_name.go` and test: Key Vault candidate and legacy resolver; `pipeline.go` consumes it.
- `promises/storage-account/workflows/resource/configure/azure_name.go` and test: Storage Account candidate and legacy resolver; `pipeline.go` consumes it.
- `promises/team-onboarding/workflows/resource/configure/azure_name.go` and test: both candidates and legacy resolver; `watcher.go` consumes it after approval.
- `promises/storage-account-terraform/workflows/resource/configure/azure_name.go` and test: Storage Account candidate and legacy resolver; `main.go` consumes it for apply and destroy.
- `promises/shared/ownership/main.go` and test: Terraform gate chooses the same candidate, while existing output-based targets for Crossplane remain unchanged.
- `README.md`, `Architecture.md`, `Implementation.md`, `Experiments.md`: operational guidance, architectural decision, progress, and lessons respectively.

The small name formula and reservation GET client are repeated in the existing separate Go modules because Docker currently builds each Promise directory as an isolated context. Test identical vectors in every copy. Do not alter CI build contexts merely to deduplicate this helper.

---

### Task 1: Standalone Key Vault resolver

**Files:** Create `promises/keyvault/workflows/resource/configure/azure_name.go`, `azure_name_test.go`; modify `pipeline.go` to include `metadata.uid` and use the resolver.

**Interfaces:** `resolveVaultName(ctx context.Context, appName, env, requestUID string, get func(context.Context, string) (int, []byte, error)) (string, error)`; `vaultCandidate(appName, env string) string`. `get` receives the reservation ConfigMap name and returns HTTP status and body.

- [ ] **Step 1: Write failing tests** for these exact candidate vectors: `a-b/dev` -> `kvabdevf79f2139fb`, `ab/dev` -> `kvabdev7c81fe2370`, `case/dev` -> `kvcasedeva5ce1785f6`, `Case/dev` -> `kvcasedev60b244d1eb`; include two app names with the same nine-character stem. Test a mock GET returning an exact legacy claim owned by the request UID, a foreign UID, 404, malformed JSON, mismatched `data.target`, HTTP 403, and empty request UID. Assert the legacy owner gets the old name and all errors fail before writing an XR.
- [ ] **Step 2: Run** `GOCACHE=/private/tmp/naming-go-cache go test ./...` from the Key Vault configure directory. Expected: compile or assertion failure in the new resolver test.
- [ ] **Step 3: Implement** the formula using `sha256.Sum256([]byte("v2\x00keyvault\x00"+appName+"\x00"+env))`, `hex.EncodeToString(sum[:])[:10]`, a lowercase alphanumeric stem, and a GET for `ownership-` plus the first 32 hex characters of SHA-256 over `azure-keyvault/<legacy-name>`. Decode only `data.target` and `data.ownerUID`. HTTP 404 and a valid foreign UID select the candidate; exact same UID selects legacy; all other responses fail. Use the in-cluster service-account token and CA already used by the ownership gate. Add `UID string \`yaml:"uid"\`` to the request metadata and call the resolver before `writeXKeyVault`.
- [ ] **Step 4: Run** the same Go test and `go vet ./...`; expect passing tests and no vet output.
- [ ] **Step 5: Review** that no XR field other than `spec.vaultName` can change for an existing request. Do not commit; Ashwin handles commits.

### Task 2: Standalone Crossplane Storage Account resolver

**Files:** Create `promises/storage-account/workflows/resource/configure/azure_name.go`, `azure_name_test.go`; modify `pipeline.go` to include `metadata.uid` and use the resolver.

**Interfaces:** `resolveStorageName(ctx context.Context, appName, env, requestUID string, get func(context.Context, string) (int, []byte, error)) (string, error)`; `storageCandidate(appName, env string) string`.

- [ ] **Step 1: Write failing tests** for `a-b/dev` -> `tekabdev9b1eebd050sa`, `ab/dev` -> `tekabdeved81cd984esa`, `case/dev` -> `tekcasedev3b876f687csa`, `Case/dev` -> `tekcasedev333b23ad09sa`, and long names sharing their first six normalized characters. Test the same claim ownership, 404, malformed, mismatched-target, HTTP 403, and empty-UID cases against `azure-storage/<legacy-name>`.
- [ ] **Step 2: Run** `GOCACHE=/private/tmp/naming-go-cache go test ./...` in the Storage Account configure directory; expect the new tests to fail.
- [ ] **Step 3: Implement** SHA-256 over `"v2\x00storage\x00"+appName+"\x00"+env`, first ten hex characters, `tek` + first six normalized app characters + environment + digest + `sa`. Resolve the legacy claim with the same strict status/body rules as Task 1, then pass the chosen name to `writeXStorageAccount` before the ownership gate runs.
- [ ] **Step 4: Run** Go tests and vet in that directory. Inspect a generated XStorageAccount to confirm only `spec.storageAccountName` changes for a new request.
- [ ] **Step 5: Review** Docker `COPY *.go` includes the new file; do not commit.

### Task 3: Approved team-onboarding resolver

**Files:** Create `promises/team-onboarding/workflows/resource/configure/azure_name.go`, `azure_name_test.go`; modify `watcher.go` naming calls. Keep `configure.go`'s PR approval content and pending behavior unchanged.

**Interfaces:** `resolveVaultName` and `resolveStorageName` with the signatures from Tasks 1 and 2. `Request.Metadata.UID` already exists.

- [ ] **Step 1: Write failing tests** using the same eight exact vectors from Tasks 1 and 2. Assert an approved watcher with both resource flags resolves two independent names, while a pending approval emits no XRs or Azure claims. Test legacy Vault-owned/current Storage-missing as a mixed case, plus API 403 and malformed claim failures.
- [ ] **Step 2: Run** `GOCACHE=/private/tmp/naming-go-cache go test ./...` in the onboarding configure directory; expect failure.
- [ ] **Step 3: Implement** the two formulas and claim lookups in `azure_name.go`. Change `watcher.go` so it resolves only each approved, requested Azure resource and supplies the chosen names to `buildXKeyVault` and `buildXStorageAccount`; remove the old duplicate computations in those builders. Keep the approved team file and XR metadata names unchanged.
- [ ] **Step 4: Run** onboarding Go tests and vet; verify the approved-content tests still pass and pending approval emits no output.
- [ ] **Step 5: Review** that a claimed legacy Key Vault plus newly requested Storage Account yields one legacy and one hashed name; do not commit.

### Task 4: Terraform resolver and gate parity

**Files:** Create `promises/storage-account-terraform/workflows/resource/configure/azure_name.go`, `azure_name_test.go`; modify its `main.go`, `destroy_test.go`; modify `promises/shared/ownership/main.go`, `main_test.go`.

**Interfaces:** Terraform `resolveStorageName` uses the Task 2 signature. Before `terraform init`, `requireSelectedClaim(ctx, requestUID, selectedName, get)` requires `azure-storage/<selectedName>` to be owned by the same UID. Change gate `targetsForRequest` to accept a resolver dependency; `run` creates the in-cluster client before deriving Terraform targets and passes a claim GET adapter. Non-Terraform targets remain output-based; the state-key target stays unchanged.

- [ ] **Step 1: Write failing tests** for the four Task 2 vectors in both the Terraform package and ownership gate. Add a same-UID legacy claim test, a 404 new-name test, and a gate/apply race test where the selected claim is removed or replaced after gate success: `requireSelectedClaim` must fail and no Terraform command may run. Test destroy retains a legacy name while its claim remains.
- [ ] **Step 2: Run** `GOCACHE=/private/tmp/naming-go-cache go test ./...` separately in the Terraform and ownership directories; expect failures for the new cases.
- [ ] **Step 3: Implement** the resolver and strict claim check in Terraform `azure_name.go`. Add request metadata UID in `main.go`, resolve the name for configure and delete, and verify the selected Azure-target claim immediately before the first Terraform command. In the ownership gate, create the client before `targetsForRequest` for Terraform requests, inject claim GET into target derivation, and replace only the Terraform account-name derivation with the same resolver. Retain the exact current state key and all Crossplane output handling. For delete verification, require both the selected Azure-target claim and state-key claim.
- [ ] **Step 4: Run** Go race tests and vet in both directories. Check the existing Terraform destroy tests and ownership rollback tests still pass.
- [ ] **Step 5: Review** that a changed claim cannot make the gate reserve one name and Terraform apply another; do not commit.

### Task 5: Repository and live verification

**Files:** Modify `README.md` for operational naming guidance; update Obsidian `Architecture.md`, `Experiments.md`, and `Implementation.md` at the end. No new deployment manifests or RBAC verbs are expected.

- [ ] **Step 1: Extend the repository wiring test** to assert the ownership gate is ordered before Terraform apply and both stages can read ConfigMaps, including delete verification. Run it after code changes and correct any manifest mismatch it finds. Keep existing Crossplane ordering coverage.
- [ ] **Step 2: Run** Go tests/vet for the four Promise packages and ownership gate, Ruby wiring tests, all changed Docker builds, Kustomize renders, and server-side Promise dry-runs with explicit hub context `k3d-k3d-teknologi-hub-cluster`.
- [ ] **Step 3: Inventory** live request UIDs, Azure-name XR fields, and hub claims. Fail rollout if an active legacy target lacks its matching UID claim. Record existing Azure names before the user's push.
- [ ] **Step 4: Handoff** reviewable code and tests to Ashwin for commit/push. After the push, confirm CI image builds, resolved Git revision, live Promise specs, and actual pipeline image digests before requests.
- [ ] **Step 5: Check** Azure resource group `teknologi-eur1-prd-platform-rg`; if present, run fresh `a-b`/`ab` variants through safe standalone paths, verify distinct Azure names and unchanged legacy targets with explicit hub/workload kubectl contexts. Delete fixtures, verify Azure/XR/Work/state removal, and release only their claims after downstream cleanup. Keep Backstage promise-flow testing deferred.
- [ ] **Step 6: Record** the hash collision lesson, migration decision, test evidence, and any operational limitation in `Experiments.md` and `Architecture.md`. Mark the finding complete in `Implementation.md` only after deployed verification; move `#NEXT` to the next open item. Do not commit or push.
