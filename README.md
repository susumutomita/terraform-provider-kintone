# terraform-provider-kintone (community prototype)

Experimental, unofficial Terraform Plugin Framework provider for **an existing dedicated kintone tenant**. Not affiliated with Cybozu. Not published to the Terraform Registry.

## Current scope

`kintone_app` creates a preview app, updates its name/description and complete ordered app ACL, deploys the settings and waits for deployment. Refresh reads deployed settings to expose GUI drift. Import accepts an existing numeric app ID.

This is **not yet a complete TenkaCloud exercise**: form fields, fixture records, record/field ACLs, participant provisioning, per-user grading and TenkaCloud integration are not implemented. No real kintone acceptance tests have been run.

## Safety and ownership

- Use a dedicated test tenant, never a production app for initial testing. ACL writes replace the entire ACL; omitted principals may lose access. Include a named provisioning administrator with `app_editable = true` and keep break-glass administrative access.
- The provider authenticates as one user with `KINTONE_USERNAME` / `KINTONE_PASSWORD` using HTTPS. API tokens alone cannot create apps. OAuth, guest spaces, additional Basic authentication and MFA login flows are not implemented. Do not put passwords in `.tf` files, CLI arguments, logs or Git. Terraform state/plan files can contain sensitive information; secure them.
- App deployment publishes **all preview changes**, not only the fields managed by this provider. Updates fail if live and preview revisions differ. Do not edit preview settings concurrently; exact revisions guard writes/deploy, but there is no multi-request transaction or automatic rollback.
- Failed create retains the app ID in state after the server returns it. A network timeout during initial creation can still have an unknown outcome: inspect the tenant before retrying so you do not create duplicates. Failed configuration/deployment can leave pending drafts. Resolve the draft manually before retrying, or import the completed app.
- No public app deletion API is implemented. Destroy **fails by default**. Set `retain_on_destroy = true` and apply before destroy to intentionally forget it from state without any remote deletion. Permissions and data remain. This is NOT cleanup and not an automatic environment reset.
- Read errors, including access denied and not-found responses, preserve state instead of treating the app as deleted.
- The provider never provisions a contract/tenant or installs software on kintone. Confirm applicable developer-license/education usage terms with Cybozu before training use.

## Local development

```sh
go mod download
go test -race ./...
go vet ./...
go build -o bin/terraform-provider-kintone .
```

Use Terraform's `dev_overrides` in a **project-local** CLI config file (do not change global configuration). Example, replace the absolute path with this checkout:

```hcl
provider_installation {
  dev_overrides {
    "susumutomita/kintone" = "/absolute/path/terraform-provider-kintone/bin"
  }
  direct {}
}
```

Set `TF_CLI_CONFIG_FILE` to that file. `terraform init` is not needed for this local dev override; no registry package exists. Run `terraform validate` and inspect `terraform plan` first. **Do not apply the example without permission to modify the specific tenant.** Set the three KINTONE_* environment variables in your own authorized environment. The repository includes no credentials or remote state.

```sh
terraform import kintone_app.exercise 123
```

A GUI change can be inspected using `terraform plan`; applying the original config intentionally restores the desired ACL, so do not automatically apply during a learner's exercise. Grading is a separate read-only operation and is not implemented here.

## Source contracts

- [Create preview app](https://cybozu.dev/ja/kintone/docs/rest-api/apps/add-app/)
- [Get app settings](https://cybozu.dev/ja/kintone/docs/rest-api/apps/settings/get-general-settings/)
- [Update app settings](https://cybozu.dev/ja/kintone/docs/rest-api/apps/settings/update-general-settings/)
- [Get app ACL](https://cybozu.dev/ja/kintone/docs/rest-api/apps/settings/get-app-permissions/)
- [Update app ACL](https://cybozu.dev/ja/kintone/docs/rest-api/apps/settings/update-app-permissions/)
- [Deploy](https://cybozu.dev/ja/kintone/docs/rest-api/apps/settings/deploy-app-settings/)
- [Deployment status](https://cybozu.dev/ja/kintone/docs/rest-api/apps/settings/get-app-deploy-status/)
- [Terraform Plugin Framework](https://developer.hashicorp.com/terraform/plugin/framework)

The implementation follows these reference contracts. Tests use a fake HTTP server; passing tests establish local behavior, not real-tenant compatibility.

## 検証状況と未検証事項

ローカルで `go test -race ./...`、`go vet ./...`、ビルド、Terraform 1.14.8 の `validate` とダミー認証による `plan -refresh=false` を確認済みです。HTTP は TLS モックで検証しました。実 kintone への `apply`、受入テスト、研修運用は未検証です。

kintone は説明文を HTML として返します。文字列の正規化による永続差分がないか、実テナントで確認が必要です。最初の実験では説明文を空文字にしてください。

ローカルの `retain_on_destroy` だけの変更はリモート更新を行いません。`include_subs=true` は ORGANIZATION に限定しています。

## Distribution and license

This is an unofficial community prototype, not a Cybozu product or an endorsed integration. kintone is a Cybozu product; product names identify API compatibility only. No affiliation, partnership or transfer agreement is implied.

Source code is available under the MIT license. Dependencies retain their respective licenses; see THIRD_PARTY_NOTICES.md. This repository does not distribute the Terraform CLI or vendored dependency source/binaries.

No Terraform Registry release or signed binary distribution exists yet. `susumutomita/kintone` is the development source address. Before a release, add generated Registry documentation, signed release artifacts, supported platform verification and real-tenant acceptance tests. A future source-address change requires coordinated configuration and state migration; repository transfer alone does not migrate Registry consumers.

## Pending settings and concurrency

The official [ACL update documentation](https://cybozu.dev/ja/kintone/docs/rest-api/apps/settings/update-app-permissions/) warns that writing the live app ACL can publish all preview settings. This provider does **not** use the live ACL PUT: the HTTP client rejects live app-settings mutations. A separate, narrowly allowlisted `POST /k/v1/records.json` primitive exists for synthetic fixture creation; it cannot update ACLs.

For an existing app, the exact flow is:

1. Read preview and live general-settings revisions; stop without writes if they differ.
2. PUT preview general settings with the preview revision.
3. PUT preview ACL with the revision returned by step 2.
4. POST preview deployment with the revision returned by step 3, then poll status.

A revision conflict at any write stops the operation. There is no automatic retry with a newer revision, unguarded revision `-1`, live-ACL shortcut, or automatic revert. A conflict or error after step 2 may leave this provider's partial changes in the preview. Inspect and reconcile them manually; retrying must not publish someone else's changes.

These are optimistic concurrency safeguards, not an exclusive lock or transactional rollback. Fresh creation skips the live comparison because no deployed app exists; nobody else may edit that newly created preview before initial configuration. Existing/imported production apps remain unsuitable for this prototype until real-tenant acceptance covers revision behavior, concurrent UI edits and asynchronous deployment. Mock tests simulate those contracts; they cannot establish the service's real behavior.

## Fixture groundwork

Typed client methods for preview text-field addition and synthetic record insertion are being developed separately from the Terraform resource. See [exercise-fixtures.md](docs/exercise-fixtures.md) for contracts, examples and remaining lifecycle work. They are not yet wired into `kintone_app`, so current HCL does not create fields or records.
