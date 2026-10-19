# Changelog

All notable changes are documented here, following
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Evidence-asset identity on every JSONL row (the frozen gopinfo↔gomount Origin
  field contract). Each of the twelve parsers now stamps an `Origin` —
  `Image`/`Volume`/`FSUUID`/`Label`/`Path` plus the asset keys `ImageSHA256`,
  `DiskSerial`, `DiskID`, `VolumeGUID`, `PartGUID`, `PartOffset` (all omitempty)
  — joined from the gomount stage manifest, and a `Host.MachineID` (`host.id`)
  read as the MachineGuid from the staged SOFTWARE hive
  (`HKLM\SOFTWARE\Microsoft\Cryptography\MachineGuid`). The wiring is the new
  gopinfo v0.3.0 `framework` Tool.Stamp hook, shared by every parser through the
  new `origin` package; goese stamps each per-table row and its index. Rows with
  no provenance (a loose tree, a live collection with no manifest, an argv debug
  pass) are unchanged, and CSV output — flat by format — does not carry the block.
- `GOWINDOWLICKER_IMAGE_HASH` (default on): opt out of the container `image_sha256`
  hash for a very large image where the extra full-container read is not worth it.
- The conformance gate of the get-sybers standard (DX_DFIR
  `docs/reference/build-and-test.md`): `make conform` (gofmt, vet, build,
  shuffled tests with coverage, `go mod tidy -diff`, a Windows cross-compile,
  a `go:generate` drift check and `scripts/structlint.sh`), `make lint`
  (golangci-lint v2, `.golangci.yml`) and `make vuln` (govulncheck), run by the
  `CI` workflow on pull requests and `main` — on amd64 and arm64 — with
  `all_green` as the one required status check.
- Repository hygiene: `.editorconfig`, `.gitattributes`, `.shellcheckrc`,
  Dependabot (actions and Go modules, weekly, grouped), `CODEOWNERS`, a
  security policy and a pull-request template.

### Changed
- gopinfo raised to v0.3.0 (the Origin asset fields, the manifest-key reader and
  the `framework` stamp hook). A disk-image run now materialises WITH
  `gomount materialise --manifest --hash` (and always stages the SOFTWARE hive),
  so the manifest the Origin join reads is written and the host MachineGuid is
  available; run against gomount v0.4.0.

### Fixed
- The findings the new lint gate surfaced, none of which changes behaviour:
  the tar readers no longer test for the deprecated `tar.TypeRegA` (the Go
  reader has normalised it to `TypeReg` since 1.11); `errors.Is` for the
  `io.EOF` and `ErrUnsupported` comparisons; the summary, contract and live
  closer writes explicitly discarded; import grouping on eight files; a test
  fixture built with `slices.Concat`; a comment on the sqlite driver import
  and a spelling.

## [0.2.1] - 2026-10-03

The last release before this changelog was started; see the git history for
this and the earlier tags.

## [0.2.0] - 2026-10-03

## [0.1.0] - 2026-09-28

[Unreleased]: https://github.com/Get-Sybers/gowindowlicker/compare/v0.2.1...HEAD
[0.2.1]: https://github.com/Get-Sybers/gowindowlicker/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/Get-Sybers/gowindowlicker/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/Get-Sybers/gowindowlicker/releases/tag/v0.1.0
