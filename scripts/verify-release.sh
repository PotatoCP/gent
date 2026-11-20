#!/usr/bin/env bash
# Verify source, release payloads, and real provider behavior before creating a release tag.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"
VERSION="${1:-v0.0.0}"
REPORT_DIR="${RELEASE_REPORT_DIR:-$(mktemp -d /tmp/gent-release-checks.XXXXXX)}"
mkdir -p "$REPORT_DIR"
REPORT_DIR="$(cd "$REPORT_DIR" && pwd)"
export GOMAXPROCS="${GOMAXPROCS:-4}"
export GOFLAGS="${GOFLAGS:-} -mod=readonly"
printf 'Release check logs: %s\n' "$REPORT_DIR"
trap 'printf "Release check failed at line %s. Logs: %s\n" "$LINENO" "$REPORT_DIR" >&2' ERR

go -C scripts build -o "$REPORT_DIR/envrun" ./envrun
go -C scripts build -o "$REPORT_DIR/releasecheck" ./releasecheck
ENV_RUN=("$REPORT_DIR/envrun" -env-file "$REPO_ROOT/.env")
MODULES=(. docs examples examples/chat scripts)

test_mode() {
	local mode="$1" folder
	local flags=()
	if [[ "$mode" == portable ]]; then export CGO_ENABLED=0; fi
	if [[ "$mode" == race ]]; then flags+=(-race); fi
	for folder in "${MODULES[@]}"; do
		printf 'Testing %s (%s)\n' "$folder" "$mode"
		"${ENV_RUN[@]}" -- go -C "$folder" test "${flags[@]}" ./... -count=1 -timeout 300s
	done
}

# Each mode has its own log. Collect every result before proceeding or reporting a failure.
pids=()
for mode in native portable race; do
	test_mode "$mode" >"$REPORT_DIR/$mode.log" 2>&1 &
	pids+=("$!")
done
failed=0
index=0
for mode in native portable race; do
	if wait "${pids[$index]}"; then
		printf 'PASS %s suites\n' "$mode"
	else
		cat "$REPORT_DIR/$mode.log"
		failed=1
	fi
	index=$((index + 1))
done
[[ "$failed" == 0 ]] || exit 1

npm ci --prefix docs >"$REPORT_DIR/npm-install.log" 2>&1
npm --prefix docs audit --audit-level=high >"$REPORT_DIR/npm-audit.log" 2>&1
npm --prefix docs test >"$REPORT_DIR/docs-tests.log" 2>&1
npm --prefix docs run build >"$REPORT_DIR/docs-build.log" 2>&1
"${ENV_RUN[@]}" -- go -C docs test ./examples/... -count=1 -timeout 300s \
	-coverprofile="$REPORT_DIR/docs.cover" >"$REPORT_DIR/docs-coverage.log" 2>&1
npm --prefix docs run check:examples -- "$REPORT_DIR/docs.cover" \
	>"$REPORT_DIR/docs-example-check.log" 2>&1
npm --prefix docs run browser >"$REPORT_DIR/docs-browser.log" 2>&1
node --test examples/chat/tests/*.test.mjs >"$REPORT_DIR/chat-tests.log" 2>&1
"${ENV_RUN[@]}" -- node examples/chat/tests/browser.mjs >"$REPORT_DIR/chat-browser.log" 2>&1
printf 'PASS documentation and chat browser checks\n'

"$REPORT_DIR/releasecheck" -root "$REPO_ROOT" -version "$VERSION" \
	-out "$REPORT_DIR/gent-module.zip"
GOBIN="$REPORT_DIR/bin" go install ./cmd/gent
"$REPORT_DIR/bin/gent" model list >"$REPORT_DIR/cli-models.log"
printf 'PASS release packages and CLI\n'

"${ENV_RUN[@]}" -list >"$REPORT_DIR/providers.txt" 2>"$REPORT_DIR/unconfigured.txt"
cat "$REPORT_DIR/unconfigured.txt"
mapfile -t providers <"$REPORT_DIR/providers.txt"
[[ "${#providers[@]}" -gt 0 ]] || { printf 'No live providers configured.\n' >&2; exit 1; }

# A provider can take several minutes to finish all features. Each feature keeps its own
# 300-second test deadline; individual request contexts retain their shorter limits.
live_list="$("${ENV_RUN[@]}" -- go test ./models -list '^TestLive(Providers|Images|Provider[A-Z])')"
live_tests=()
while IFS= read -r name; do
	case "$name" in TestLive*) live_tests+=("$name") ;; esac
done <<<"$live_list"
[[ "${#live_tests[@]}" -gt 0 ]] || { printf 'No live tests found.\n' >&2; exit 1; }

live_provider() {
	local provider="$1" pattern="${1//./\\.}" name
	for name in "${live_tests[@]}"; do
		printf 'Checking %s: %s\n' "$provider" "$name"
		"${ENV_RUN[@]}" -provider "$provider" -- go test ./models \
			-run "^${name}$/^${pattern}$" -count=1 -timeout 300s
	done
	if [[ "$provider" == xAI ]]; then
		"${ENV_RUN[@]}" -provider xAI -- go test ./models \
			-run '^TestStreaming' -count=1 -timeout 300s
		"${ENV_RUN[@]}" -provider xAI -- go test ./integrationtest/airline \
			./integrationtest/ecommerce -count=1 -timeout 300s
	fi
}

pids=()
for provider in "${providers[@]}"; do
	printf 'Testing live provider: %s\n' "$provider"
	live_provider "$provider" >"$REPORT_DIR/live-$provider.log" 2>&1 &
	pids+=("$!")
done
failed=0
for index in "${!providers[@]}"; do
	provider="${providers[$index]}"
	if wait "${pids[$index]}"; then
		printf 'PASS live provider: %s\n' "$provider"
	else
		cat "$REPORT_DIR/live-$provider.log"
		failed=1
	fi
done
[[ "$failed" == 0 ]] || exit 1

browser_provider=""
for provider in "${providers[@]}"; do
	case "$provider" in
		OpenAI|Anthropic|Gemini|xAI) browser_provider="$provider"; break ;;
	esac
done
[[ -n "$browser_provider" ]] || {
	printf 'The live chat browser needs OpenAI, Anthropic, Gemini, or xAI configuration.\n' >&2
	exit 1
}
CHAT_TEST_PROVIDER="${browser_provider,,}" "${ENV_RUN[@]}" -provider "$browser_provider" \
	-- node examples/chat/tests/browser.mjs >"$REPORT_DIR/live-chat.log" 2>&1
printf 'PASS live chat browser: %s\n' "$browser_provider"
printf 'PASS all release checks. Logs: %s\n' "$REPORT_DIR"
