# Exercise fixture groundwork (not yet a Terraform resource)

Goal: a small customer app containing synthetic records, where the learner removes excessive Everyone access while preserving the sales group's access.

## API contract

- [Add form fields](https://cybozu.dev/ja/kintone/docs/rest-api/apps/form/add-form-fields/): add SINGLE_LINE_TEXT fields to the preview app, with an explicit revision. This operation does not deploy settings. The client does not update or delete existing fields.
- [Add records](https://cybozu.dev/ja/kintone/docs/rest-api/records/add-records/): insert up to 100 records into a deployed app. Records do not have a preview endpoint. The client does not update or delete existing records.

`internal/kintone/fixtures.go` supplies typed, limited client primitives. They are not called by `kintone_app`, not a CLI, and not an end-to-end provisioner. JSON examples under `examples/fixtures/` are illustrative API data, not automatically loaded Terraform configuration.

## Required orchestration before real use

1. Authorize a dedicated test tenant and provisioning user. Never use real customer records.
2. Create one team-specific app and durably save its app ID and initial revision.
3. Add fields using the revision from the preceding operation; retain the returned revision.
4. Configure app settings/ACL and deploy using the same revision chain. Do **not** call the existing ConfigureNewApp helper after field changes as if it established ownership of those changes: lifecycle integration must pass the expected revision explicitly.
5. Wait for deployment SUCCESS before inserting any records. Keep provisioning user's record-add and management permissions. Any deliberately excessive learner permissions must remain limited to the exercise app.
6. Confirm fixture field codes match the deployed form, insert the synthetic records once, verify the stored values, save returned IDs, and then expose the app to the participant. The records API can ignore unknown field codes; returned IDs alone do not prove fixture data was saved.
7. Grade using actual deployed ACL/effective access, not Terraform state. No automatic apply during the exercise.

The client methods do not enforce this whole sequence. Terraform schema/state design and orchestration integration are still required; adding these primitives alone does not make an exercise deployable.

## Retry, recovery and destruction

There is no idempotency key for the fixture POST in this implementation. A transport error, timeout, or malformed response may occur after the service has created records. **Do not automatically retry.** Inspect the app and reconcile IDs first. Field creation also stops on revision conflicts rather than re-reading and overwriting changes.

No automatic cleanup, field deletion, or record deletion is implemented. Fixtures belong in a disposable, isolated training app. Record IDs and deployment-stage progress must be persisted by a future orchestrator before any recovery logic is considered complete. API access and creation permissions alone do not establish licensing suitability for training delivery.

All current tests use local TLS mock servers. Actual field deployment, record creation and GUI usability remain unverified.
