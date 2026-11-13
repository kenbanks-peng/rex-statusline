# Releases

Push a stable `vMAJOR.MINOR.PATCH` tag to run tests, build four platform
archives, and publish a GitHub release. Tags containing `-` publish
prereleases and do not update Homebrew.

After publishing a stable release, the workflow downloads its archives,
updates the version and SHA-256 checksums in
`kenbanks-peng/homebrew-tap`, opens a PR, and immediately squash-merges it.
The merge uses `--admin`: required reviews and checks are bypassed when
the token's identity is authorized to bypass them. Older releases cannot
downgrade the formula; reruns of an already merged release are no-ops.

## One-time credential setup

Create a fine-grained personal access token owned by a tap administrator,
restricted to `kenbanks-peng/homebrew-tap`, with these repository permissions:

- Contents: Read and write
- Pull requests: Read and write

Store it as the `HOMEBREW_TAP_TOKEN` Actions secret in
`kenbanks-peng/rex-statusline`. Do not commit the token.
If branch rules are added to the tap, explicitly authorize this identity
to bypass them; `--admin` cannot bypass rules that disallow that identity.

A Homebrew update failure does not undo the published release. After fixing
credentials or permissions, rerun the failed job. The workflow must already
exist in the release tag being run.
