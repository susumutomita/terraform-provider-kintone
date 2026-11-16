# Third-party components

The provider implementation and examples in this repository were authored for this project. No Cybozu implementation, website text, artwork or logos are bundled. API references in README document the contract, not an endorsement.

Dependencies are resolved through Go modules, not vendored. The authoritative versions are in go.mod / go.sum. MIT applies only to this project's original files, not to dependencies.

- HashiCorp Terraform Plugin Framework and supporting HashiCorp Go modules: Mozilla Public License 2.0 (see each module's LICENSE).
- Other transitive Go modules retain their own upstream licenses. Each downloaded module's LICENSE / NOTICE files must be reviewed and preserved when redistributing them, especially in future binary distributions.
- Terraform CLI is a separate tool and is not included in this repository or covered by this project's MIT license.

No dependency source or compiled binary is included in this initial source-only repository. A binary release requires a complete shipped-component license/notice inventory before distribution.
