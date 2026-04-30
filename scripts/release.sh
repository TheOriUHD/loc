#!/usr/bin/env bash
set -euo pipefail

STEP="startup"
STEP_NUMBER=0
COMMIT_CREATED=0
TAG_CREATED=0
BRANCH_PUSHED=0
TAG_PUSHED=0
RELEASE_CREATED=0

VERSION=""
TAG=""
DRY_RUN=0
NOTES=""
NOTES_PROVIDED=0
RELEASE_BRANCH="${RELEASE_BRANCH:-main}"
EXPECTED_MODULE="${RELEASE_MODULE:-github.com/theoriuhd/loc}"
RELEASE_REPO_URL="https://github.com/theoriuhd/loc"
GO_INSTALL_PATH="github.com/theoriuhd/loc/cmd/loc"

artifacts=(
	"dist/loc-darwin-amd64"
	"dist/loc-darwin-arm64"
	"dist/loc-linux-amd64"
	"dist/loc-linux-arm64"
	"dist/checksums.txt"
)

usage() {
	cat <<'USAGE'
Usage:
  ./scripts/release.sh VERSION [--dry-run] [--notes "Custom release notes here"]

VERSION is bare semver, for example 0.2.0. The git tag will be vVERSION.
USAGE
}

die() {
	echo "Error: $*" >&2
	exit 1
}

on_error() {
	local exit_code=$?
	if [[ $exit_code -eq 0 ]]; then
		return
	fi
	echo "Release aborted at step ${STEP}" >&2
	if (( STEP_NUMBER > 6 )); then
		echo "Release state:" >&2
		if [[ $COMMIT_CREATED -eq 1 ]]; then
			echo "  Completed: release commit was created." >&2
		else
			echo "  Not completed: release commit was not created by this script." >&2
		fi
		if [[ $TAG_CREATED -eq 1 ]]; then
			echo "  Completed: local tag ${TAG} was created." >&2
		else
			echo "  Not completed: local tag ${TAG} was not created." >&2
		fi
		if [[ $BRANCH_PUSHED -eq 1 ]]; then
			echo "  Completed: branch ${RELEASE_BRANCH} was pushed to origin." >&2
		else
			echo "  Not completed: branch ${RELEASE_BRANCH} was not pushed." >&2
		fi
		if [[ $TAG_PUSHED -eq 1 ]]; then
			echo "  Completed: tag ${TAG} was pushed to origin." >&2
		else
			echo "  Not completed: tag ${TAG} was not pushed." >&2
		fi
		if [[ $RELEASE_CREATED -eq 1 ]]; then
			echo "  Completed: GitHub release was created." >&2
		else
			echo "  Not completed: GitHub release was not created." >&2
		fi
		if [[ $TAG_CREATED -eq 1 && $TAG_PUSHED -eq 0 ]]; then
			echo "Manual cleanup: Tag ${TAG} exists locally but was not pushed. Run: git tag -d ${TAG}" >&2
		fi
		if [[ $TAG_PUSHED -eq 1 && $RELEASE_CREATED -eq 0 ]]; then
			echo "Manual cleanup: Tag ${TAG} exists on origin but the GitHub release was not created. Run: git push --delete origin ${TAG}" >&2
		fi
	fi
	exit "$exit_code"
}
trap on_error ERR

step() {
	STEP="$1"
	STEP_NUMBER="$2"
	echo
	echo "==> Step ${STEP_NUMBER}: $3"
}

parse_args() {
	if [[ $# -lt 1 ]]; then
		usage >&2
		exit 1
	fi
	VERSION="$1"
	shift
	while [[ $# -gt 0 ]]; do
		case "$1" in
			--dry-run)
				DRY_RUN=1
				shift
				;;
			--notes)
				[[ $# -ge 2 ]] || die "--notes requires a value"
				NOTES="$2"
				NOTES_PROVIDED=1
				shift 2
				;;
			-h|--help)
				usage
				exit 0
				;;
			*)
				die "unknown argument: $1"
				;;
		esac
	done
	if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
		die "version must match semver pattern ^[0-9]+\.[0-9]+\.[0-9]+$"
	fi
	TAG="v${VERSION}"
}

require_tool() {
	local tool="$1"
	command -v "$tool" >/dev/null 2>&1 || die "missing required tool: ${tool}"
}

assert_repo_root() {
	[[ -f go.mod ]] || die "go.mod not found; run this from the loc repo root"
	grep -qx "module ${EXPECTED_MODULE}" go.mod || die "go.mod module path is not ${EXPECTED_MODULE}"
}

assert_clean_tree() {
	local status
	status="$(git status --short --untracked-files=all)"
	if [[ -n "$status" ]]; then
		echo "$status" >&2
		die "working tree is dirty"
	fi
}

assert_branch() {
	local branch
	branch="$(git branch --show-current)"
	[[ "$branch" == "$RELEASE_BRANCH" ]] || die "must be on ${RELEASE_BRANCH}; currently on ${branch:-detached HEAD}"
}

assert_tag_available() {
	if git rev-parse -q --verify "refs/tags/${TAG}" >/dev/null; then
		die "tag ${TAG} already exists locally"
	fi
	set +e
	git ls-remote --exit-code --tags origin "refs/tags/${TAG}" >/dev/null 2>&1
	local remote_status=$?
	set -e
	case "$remote_status" in
		0) die "tag ${TAG} already exists on origin" ;;
		2) return 0 ;;
		*) die "could not check tag ${TAG} on origin" ;;
	esac
}

preflight() {
	step "1" 1 "Preflight checks"
	require_tool git
	require_tool go
	require_tool gofmt
	require_tool gh
	require_tool make
	require_tool shasum
	assert_repo_root
	assert_clean_tree
	assert_branch
	assert_tag_available
	gh auth status >/dev/null
	echo "Preflight checks passed for ${TAG}."
}

run_verification() {
	step "2" 2 "Run verification suite"
	go mod tidy
	local formatted
	formatted="$(gofmt -l .)"
	if [[ -n "$formatted" ]]; then
		echo "$formatted" >&2
		die "gofmt -l . reported files that need formatting"
	fi
	go vet ./...
	go test ./...
	echo "Verification suite passed."
}

check_version_injection() {
	step "3" 3 "Confirm embedded version injection"
	grep -q -- '-X main.version=$(VERSION)' Makefile || die "Makefile must inject -X main.version=\$(VERSION)"
	grep -q -- '-ldflags' Makefile || die "Makefile release target must pass -ldflags"
	export VERSION
	echo "Makefile version injection confirmed."
}

cross_compile() {
	step "4" 4 "Cross-compile release binaries"
	make release
	for artifact in "${artifacts[@]:0:4}"; do
		[[ -f "$artifact" ]] || die "missing release artifact: ${artifact}"
	done
	echo "Release binaries are present in dist/."
}

generate_checksums() {
	step "5" 5 "Generate checksums"
	(
		cd dist
		shasum -a 256 loc-* > checksums.txt
	)
	[[ -f dist/checksums.txt ]] || die "missing dist/checksums.txt"
	echo "Wrote dist/checksums.txt."
}

commit_changes_if_needed() {
	step "6" 6 "Commit version bump if needed"
	local changed_paths
	changed_paths="$(git status --short -- go.mod go.sum Makefile)"
	if [[ -z "$changed_paths" ]]; then
		echo "No version bump changes to commit."
		return
	fi
	echo "$changed_paths"
	if [[ $DRY_RUN -eq 1 ]]; then
		echo "DRY RUN: would commit version bump changes."
		return
	fi
	git add -A -- go.mod go.sum Makefile
	git commit -m "Release ${TAG}"
	COMMIT_CREATED=1
}

dry_run_summary() {
	local count=${#artifacts[@]}
	echo
	echo "DRY RUN: would tag ${TAG} and create release with ${count} artifacts"
	echo "No changes pushed."
}

tag_and_push() {
	step "7" 7 "Tag and push"
	git tag -a "$TAG" -m "$TAG"
	TAG_CREATED=1
	git push origin "$RELEASE_BRANCH"
	BRANCH_PUSHED=1
	git push origin "$TAG"
	TAG_PUSHED=1
}

create_github_release() {
	step "8" 8 "Create GitHub release"
	local args=(
		"$TAG"
		--title "$TAG"
	)
	if [[ $NOTES_PROVIDED -eq 1 ]]; then
		args+=(--notes "$NOTES")
	else
		args+=(--generate-notes)
	fi
	args+=("${artifacts[@]}")
	gh release create "${args[@]}"
	RELEASE_CREATED=1
}

success_summary() {
	step "9" 9 "Success summary"
	cat <<SUMMARY
✓ Released ${TAG}
  ${RELEASE_REPO_URL}/releases/tag/${TAG}

  Install with Go:
    go install ${GO_INSTALL_PATH}@${TAG}

  Or download a binary from the release page.
SUMMARY
}

main() {
	parse_args "$@"
	local script_dir
	script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
	cd "${script_dir}/.."
	preflight
	run_verification
	check_version_injection
	cross_compile
	generate_checksums
	commit_changes_if_needed
	if [[ $DRY_RUN -eq 1 ]]; then
		dry_run_summary
		return
	fi
	tag_and_push
	create_github_release
	success_summary
}

main "$@"