#!/usr/bin/env bash
# Preview by default. Publication requires a clean, pushed commit and complete release checks.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION=""
EXECUTE=0
REMOTE=origin
NOTES=""

usage() {
	cat <<'USAGE'
Usage: scripts/release.sh VERSION [--execute] [--notes FILE] [--remote NAME]

The default mode previews the release. --execute runs all release checks, creates and pushes an
annotated tag, verifies the downloaded Go module and CLI, and publishes a GitHub release.
The notes file uses Markdown. Without it, commit subjects supply the release notes.
USAGE
}

fail() { printf 'Error: %s\n' "$1" >&2; exit 1; }

while [[ $# -gt 0 ]]; do
	case "$1" in
		--execute) EXECUTE=1; shift ;;
		--notes)
			[[ $# -ge 2 ]] || fail '--notes requires a file'
			NOTES="$(realpath "$2")"; shift 2 ;;
		--remote)
			[[ $# -ge 2 ]] || fail '--remote requires a name'
			REMOTE="$2"; shift 2 ;;
		-h|--help) usage; exit 0 ;;
		-*) fail "unknown option: $1" ;;
		*) [[ -z "$VERSION" ]] || fail 'version was supplied twice'; VERSION="$1"; shift ;;
	esac
done
[[ -n "$VERSION" ]] || fail 'supply an explicit release version'
[[ "$VERSION" == v* ]] || VERSION="v$VERSION"
[[ "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] || fail 'invalid version'
[[ -z "$NOTES" || -f "$NOTES" ]] || fail 'notes file does not exist'
cd "$REPO_ROOT"

MODULE_PATH="$(awk '$1 == "module" {print $2; exit}' go.mod)"
REPOSITORY="${MODULE_PATH#github.com/}"
RELEASE_HEAD="$(git rev-parse HEAD)"
PREVIOUS="$(git tag -l 'v*' --sort=-version:refname | sed -n '1p')"
TEMP_DIR="$(mktemp -d /tmp/gent-release.XXXXXX)"
trap 'rm -rf "$TEMP_DIR"' EXIT
if [[ -z "$NOTES" ]]; then
	NOTES="$TEMP_DIR/notes.md"
	if [[ -n "$PREVIOUS" ]]; then
		git log "$PREVIOUS..HEAD" --format='- %s (%h)' >"$NOTES"
	else
		git log --format='- %s (%h)' >"$NOTES"
	fi
fi
printf 'Gent %s from %s\nModule: %s\n\n' "$VERSION" "$RELEASE_HEAD" "$MODULE_PATH"
cat "$NOTES"
if [[ "$EXECUTE" == 0 ]]; then
	printf '\nPreview only. Add --execute to run checks and publish.\n'
	exit 0
fi

command -v gh >/dev/null || fail 'GitHub CLI is required'
gh auth status >/dev/null 2>&1 || fail 'GitHub CLI is not authenticated'
[[ "$(git branch --show-current)" == main ]] || fail 'release must run from main'
[[ -z "$(git status --porcelain)" ]] || fail 'release requires a clean worktree'
if git rev-parse -q --verify "refs/tags/$VERSION" >/dev/null; then
	fail "local tag $VERSION already exists"
fi
REMOTE_REFS="$(git ls-remote "$REMOTE" refs/heads/main "refs/tags/$VERSION")"
REMOTE_HEAD="$(awk '$2 == "refs/heads/main" {print $1}' <<<"$REMOTE_REFS")"
[[ "$REMOTE_HEAD" == "$RELEASE_HEAD" ]] || fail 'push main before release'
[[ "$(wc -l <<<"$REMOTE_REFS")" == 1 ]] || fail "remote tag $VERSION already exists"

export RELEASE_REPORT_DIR="${RELEASE_REPORT_DIR:-$(mktemp -d /tmp/gent-release-checks.XXXXXX)}"
mkdir -p "$RELEASE_REPORT_DIR"
RELEASE_REPORT_DIR="$(cd "$RELEASE_REPORT_DIR" && pwd)"
bash scripts/verify-release.sh "$VERSION"
[[ "$(git rev-parse HEAD)" == "$RELEASE_HEAD" ]] || fail 'HEAD changed during verification'
[[ -z "$(git status --porcelain)" ]] || fail 'worktree changed during verification'

# Check GitHub's actual source archive before creating the immutable public version tag.
gh api "repos/$REPOSITORY/zipball/$RELEASE_HEAD" >"$TEMP_DIR/source.zip"
"$RELEASE_REPORT_DIR/releasecheck" -source-zip "$TEMP_DIR/source.zip"

git tag -a "$VERSION" -m "Gent $VERSION" "$RELEASE_HEAD"
git push "$REMOTE" "refs/tags/$VERSION"
GOPROXY=https://proxy.golang.org go mod download -json "$MODULE_PATH@$VERSION" \
	>"$TEMP_DIR/download.json"
MODULE_ZIP="$(node -e 'console.log(require(process.argv[1]).Zip)' "$TEMP_DIR/download.json")"
"$RELEASE_REPORT_DIR/releasecheck" -version "$VERSION" -module-zip "$MODULE_ZIP"
GOBIN="$TEMP_DIR/bin" GOPROXY=https://proxy.golang.org go install "$MODULE_PATH/cmd/gent@$VERSION"
"$TEMP_DIR/bin/gent" model list >"$RELEASE_REPORT_DIR/released-cli-models.log"
gh release create "$VERSION" --verify-tag --title "Gent $VERSION" --notes-file "$NOTES"
printf 'Released Gent %s. Library and CLI install checks passed.\n' "$VERSION"
