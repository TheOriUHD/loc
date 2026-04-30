#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

fail() {
	echo "release_test.sh: $*" >&2
	exit 1
}

assert_nonzero_with() {
	local label="$1"
	local expected="$2"
	shift 2
	local output="$TMPDIR/${label}.out"
	set +e
	"$@" >"$output" 2>&1
	local status=$?
	set -e
	[[ $status -ne 0 ]] || fail "${label}: expected non-zero exit"
	grep -qi -- "$expected" "$output" || fail "${label}: expected output to contain ${expected}"
}

make_fake_toolchain() {
	local bin="$1"
	mkdir -p "$bin"
	cat >"$bin/go" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
	cat >"$bin/gofmt" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
	cat >"$bin/gh" <<'EOF'
#!/usr/bin/env bash
if [[ "$1" == "auth" && "${2:-}" == "status" ]]; then
	exit 0
fi
exit 0
EOF
	cat >"$bin/make" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" != "release" ]]; then
	exit 0
fi
mkdir -p dist
for artifact in loc-darwin-amd64 loc-darwin-arm64 loc-linux-amd64 loc-linux-arm64; do
	printf 'fake %s\n' "$artifact" >"dist/$artifact"
done
EOF
	cat >"$bin/shasum" <<'EOF'
#!/usr/bin/env bash
if [[ "${1:-}" == "-a" && "${2:-}" == "256" ]]; then
	shift 2
fi
for file in "$@"; do
	printf '%064d  %s\n' 0 "$file"
done
EOF
	chmod +x "$bin/go" "$bin/gofmt" "$bin/gh" "$bin/make" "$bin/shasum"
}

make_fixture_repo() {
	local repo="$1"
	mkdir -p "$repo/scripts"
	cp "$ROOT/scripts/release.sh" "$repo/scripts/release.sh"
	chmod +x "$repo/scripts/release.sh"
	cat >"$repo/go.mod" <<'EOF'
module github.com/theoriuhd/loc

go 1.22
EOF
	touch "$repo/go.sum"
	cat >"$repo/Makefile" <<'EOF'
VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: release
release:
	@go build -ldflags="$(LDFLAGS)" -o dist/loc-linux-amd64 ./cmd/loc
EOF
	git -C "$repo" init -q
	git -C "$repo" checkout -q -b main
	git -C "$repo" config user.email release-test@example.invalid
	git -C "$repo" config user.name "Release Test"
	git -C "$repo" add .
	git -C "$repo" commit -q -m init
	local origin="$TMPDIR/origin.git"
	git init --bare -q "$origin"
	git -C "$repo" remote add origin "$origin"
}

assert_nonzero_with "missing-args" "Usage" "$ROOT/scripts/release.sh"
assert_nonzero_with "bad-version" "semver" "$ROOT/scripts/release.sh" notaversion

fixture="$TMPDIR/fixture"
fakebin="$TMPDIR/fakebin"
make_fixture_repo "$fixture"
make_fake_toolchain "$fakebin"

output="$TMPDIR/dry-run.out"
(
	cd "$fixture"
	PATH="$fakebin:$PATH" ./scripts/release.sh 99.99.99 --dry-run
) >"$output" 2>&1 || {
	cat "$output" >&2
	fail "dry-run: expected zero exit"
}

grep -q "DRY RUN" "$output" || fail "dry-run: expected DRY RUN output"
[[ -z "$(git -C "$fixture" tag --list v99.99.99)" ]] || fail "dry-run: tag was created"

echo "release script tests passed"