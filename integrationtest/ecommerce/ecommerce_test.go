// Keep the SearchChain, compaction, and JavaScript tool coverage in Go tests.
// Use examples/chat for interactive integration checks through HTTP and streaming events.
package ecommerce

import (
	"context"
	"os"
	"testing"

	"github.com/rickchristie/gent/integrationtest/testutil"
)

// TestDoubleChargeSearchSummarizationPTC tests the
// double-charge scenario with SearchChain toolchain
// wrapped in JsToolChainWrapper (programmatic tool
// calling) and summarization compaction.
func TestDoubleChargeSearchSummarizationPTC(
	t *testing.T,
) {
	if os.Getenv("GENT_TEST_XAI_KEY") == "" {
		t.Skip(
			"GENT_TEST_XAI_KEY not set, " +
				"skipping integration test",
		)
	}

	ctx := context.Background()
	config := testutil.DefaultTestConfig()
	config.ToolChain = testutil.ToolChainSearch
	config.WrapPTC = true
	config.Compaction = testutil.CompactionConfig{
		Type:              testutil.CompactionSummarization,
		TriggerIterations: 5,
		KeepRecent:        1,
	}

	if err := RunDoubleChargeScenarioSearch(
		ctx, os.Stdout, config,
	); err != nil {
		t.Fatalf(
			"Double charge Search+Summarization"+
				"+PTC failed: %v", err,
		)
	}
}
