# plib/bin

The release build (`build/build-panel.sh plesk <version>`) puts three files
here; none of them is kept in Git:

- `relical`: the core, built from the same commit as the extension.
- `restic`: the pinned restic release, checked against `tools/SHA256SUMS.pinned`.
- `SHA256SUMS`: the sha256 of both, in `sha256sum` format.

On install and upgrade, `plib/scripts/post-install.php` refuses to go on
unless both binaries match `SHA256SUMS`, then installs them into
`/usr/local/lib/relical/`.
