# Experimental creation-only exercise fixtures

Goal: a small customer app containing synthetic records, where the learner removes excessive Everyone access while preserving the sales group's access.

## API contract

- [Add form fields](https://cybozu.dev/ja/kintone/docs/rest-api/apps/form/add-form-fields/): add SINGLE_LINE_TEXT fields to the preview app, with an explicit revision. This operation does not deploy settings. The client does not update or delete existing fields.
- [Add records](https://cybozu.dev/ja/kintone/docs/rest-api/records/add-records/): insert up to 100 records into a deployed app. Records do not have a preview endpoint. The client does not update or delete existing records.

`internal/kintone/fixtures.go` supplies typed client primitives and `exercise.go` orchestrates initialization. `kintone_app` connects this through creation-only JSON inputs. `examples/main.tf` demonstrates them; the separate JSON files are illustrative and not automatically loaded.

## Initialization sequence and remaining operational work

1. Authorize a dedicated test tenant and provisioning user. Never use real customer records.
2. Create one team-specific app, report its ID through the checkpoint, and retain its initial revision in memory. A future external journal is required for durable crash recovery.
3. Add fields using the revision from the preceding operation; retain the returned revision.
4. Configure app settings/ACL and deploy using the same revision chain. The initializer passes the expected revision explicitly; it does not re-read a new preview revision to adopt foreign changes.
5. Wait for deployment SUCCESS before inserting any records. Keep provisioning user's record-add and management permissions. Any deliberately excessive learner permissions must remain limited to the exercise app.
6. Validate fixture field codes against the declared text fields, insert the synthetic records once, checkpoint returned IDs, then verify stored types/values by reading those IDs back before exposing the app to the participant. The records API can ignore unknown field codes; returned IDs alone do not prove fixture data was saved.
7. Grade using actual deployed ACL/effective access, not Terraform state. No automatic apply during the exercise.

The initializer implements creation, field configuration, deploy-wait, insertion and value verification. Tenant authorization, licensing, learner access, grading and robust external recovery remain outside this resource. Local checkpoints are returned to Terraform on completion/error, not durably journaled between HTTP requests. A crash can still leave untracked remote changes.

## Retry, recovery and destruction

There is no idempotency key for the fixture POST in this implementation. A transport error, timeout, or malformed response may occur after the service has created records. **Do not automatically retry.** Inspect the app and reconcile IDs first. Field creation also stops on revision conflicts rather than re-reading and overwriting changes.

No automatic cleanup, field deletion, or record deletion is implemented. Fixtures belong in a disposable, isolated training app. Record IDs and stage are exposed in response state, but a future orchestrator needs a durable journal before crash recovery can be considered complete. API access and creation permissions alone do not establish licensing suitability for training delivery.

All current tests use local TLS mock servers. Actual field deployment, record creation and GUI usability remain unverified.
