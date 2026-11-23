package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Run the real gate with command fixtures. This tests discovery, scheduling, and process failure
// propagation together without making paid provider calls from the fixture suite.
func TestReleaseGateNativeLive(t *testing.T) {
	type input struct{ failNative bool }
	type expected struct {
		exit  int
		calls []string
	}
	type scenario struct {
		name     string
		input    input
		expected expected
	}
	cases := []scenario{
		{"success", input{false}, expected{0, []string{
			"test ./models -run ^TestLiveProviders$/^OpenAI$ -count=1 -timeout 300s",
			"test ./models -run ^TestNativeLive$/^OpenAI$ -count=1 -timeout 300s",
			"-C examples/chat test -run ^TestLiveChatScenarios$/^openai$/^airline$ -count=1 -timeout 300s",
			"-C examples/chat test -run ^TestLiveChatScenarios$/^openai$/^ecommerce$ -count=1 -timeout 300s",
		}}},
		{"native failure", input{true}, expected{1, []string{
			"test ./models -run ^TestLiveProviders$/^OpenAI$ -count=1 -timeout 300s",
			"test ./models -run ^TestNativeLive$/^OpenAI$ -count=1 -timeout 300s",
		}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			bin := filepath.Join(root, "bin")
			assert.NoError(t, os.MkdirAll(filepath.Join(root, "scripts"), 0o755))
			assert.NoError(t, os.MkdirAll(bin, 0o755))
			data, err := os.ReadFile("verify-release.sh")
			if !assert.NoError(t, err) {
				return
			}
			assert.NoError(t, os.WriteFile(filepath.Join(root, "scripts/verify-release.sh"), data, 0o755))
			for name, script := range map[string]string{
				"go": gateGoFixture, "npm": gateNoopFixture, "node": gateNoopFixture,
			} {
				assert.NoError(t, os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755))
			}
			log := filepath.Join(root, "calls")
			command := exec.Command("bash", filepath.Join(root, "scripts/verify-release.sh"))
			command.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"),
				"RELEASE_REPORT_DIR="+filepath.Join(root, "report"), "GATE_CALLS="+log)
			if test.input.failNative {
				command.Env = append(command.Env, "GATE_FAIL_NATIVE=1")
			}
			output, err := command.CombinedOutput()
			exit := 0
			if err != nil {
				failure, ok := err.(*exec.ExitError)
				if !assert.True(t, ok, "%v", err) {
					return
				}
				exit = failure.ExitCode()
			}
			assert.Equal(t, test.expected.exit, exit, "%s", output)
			calls, err := os.ReadFile(log)
			assert.NoError(t, err)
			assert.Equal(t, test.expected.calls, strings.Split(strings.TrimSpace(string(calls)), "\n"))
		})
	}
}

const gateGoFixture = `#!/usr/bin/env bash
set -eu
if [[ "$*" == '-C scripts build -o '* ]]; then
  if [[ "${@: -1}" == ./envrun ]]; then
    cat >"$5" <<'ENV'
#!/usr/bin/env bash
set -eu
while [[ "$#" -gt 0 ]]; do
  case "$1" in
    -list) printf 'OpenAI\n'; exit 0 ;;
    --) shift; exec "$@" ;;
    *) shift ;;
  esac
done
ENV
  else
    printf '#!/bin/sh\nexit 0\n' >"$5"
  fi
  chmod +x "$5"
elif [[ "$1" == install ]]; then
  mkdir -p "$GOBIN"
  printf '#!/bin/sh\nexit 0\n' >"$GOBIN/gent"
  chmod +x "$GOBIN/gent"
elif [[ "$*" == *' -list '* ]]; then
  # Apply the actual expression from the gate, including a fixture name it must not schedule.
  for name in TestLiveProviders TestNativeLive TestNativeFixture; do
    if [[ "$name" =~ ${@: -1} ]]; then printf '%s\n' "$name"; fi
  done
elif [[ "$*" == *' -run '* ]]; then
  printf '%s\n' "$*" >>"$GATE_CALLS"
  if [[ "$*" == *'^TestNativeLive$/'* && "${GATE_FAIL_NATIVE:-}" == 1 ]]; then exit 7; fi
fi
`

const gateNoopFixture = `#!/bin/sh
exit 0
`
