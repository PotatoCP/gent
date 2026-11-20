package examples

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/common"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/format"
	"github.com/rickchristie/gent/policy"
	"github.com/rickchristie/gent/search"
	"github.com/stretchr/testify/assert"
)

// Constant vectors make the index behavior independent of remote models and model downloads.
type fixtureEmbedder struct{}

func (fixtureEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	return []float32{1, 0}, ctx.Err()
}
func (fixtureEmbedder) EmbedText(ctx context.Context, text string) ([]float32, error) {
	return []float32{1, 0}, ctx.Err()
}
func (fixtureEmbedder) EmbedTextBatch(ctx context.Context, texts []string) ([][]float32, error) {
	values := make([][]float32, len(texts))
	for i := range texts {
		values[i] = []float32{1, 0}
	}
	return values, ctx.Err()
}
func (fixtureEmbedder) MaxTokens() int             { return 128 }
func (fixtureEmbedder) Dimensions() int            { return 2 }
func (fixtureEmbedder) TokenCount(text string) int { return len(strings.Fields(text)) }
func (fixtureEmbedder) Close() error               { return nil }

func TestSearchExamples(t *testing.T) {
	ctx := context.Background()
	embedder := fixtureEmbedder{}
	keyword, err := keywordSearch(ctx)
	assert.NoError(t, err)
	// This is Bleve's fixed score and highlighted ID for this one-document fixture.
	assert.Equal(t, []search.SearchResult{{Id: "refund", Score: 0.07122961654954943,
		Snippet: "<mark>refund</mark>"}}, keyword)
	vector, err := vectorSearch(ctx, embedder)
	assert.NoError(t, err)
	assert.Equal(t, []search.SearchResult{{Id: "refund", Score: 1,
		Snippet: `# refund

Refunds are available within 30 days.`}}, vector)
	articles, err := articleSearch(ctx, embedder)
	assert.NoError(t, err)
	assert.Equal(t, []search.SearchResult{{Id: "refund", Score: 1,
		Snippet: "Refunds are available within 30 days."}}, articles)
	first := search.NewFlatIndex(&policy.PolicyChunkAdapter{}, embedder)
	second := search.NewFlatIndex(&policy.PolicyChunkAdapter{}, embedder)
	index := fusedSearch(first, second)
	assert.NoError(t, replaceDocuments(ctx, index))
	fused, err := index.Search(ctx, "refund", 3)
	assert.NoError(t, err)
	assert.Equal(t, []search.SearchResult{{Id: "refund", Score: 1,
		Snippet: `# refund

Refunds are available within 14 days.`, Metadata: map[string]any{
			"keyword_raw": float64(1), "keyword_normalized": float64(1),
			"keyword_weighted": 0.4, "vector_raw": float64(1), "vector_weighted": 0.6,
			"vector_normalized": float64(1),
		}}}, fused)
	assert.NoError(t, index.Remove("refund"))
	empty, err := index.Search(ctx, "refund", 3)
	assert.NoError(t, err)
	assert.Empty(t, empty)
	vectors, err := embedText(ctx, embedder)
	assert.NoError(t, err)
	assert.Equal(t, [][]float32{{1, 0}, {1, 0}, {1, 0}}, vectors)

	chain, err := policyTools(ctx, embedder)
	assert.NoError(t, err)
	model := &scriptModel{Calls: []*gent.ModelToolCallRequest{
		toolCall("get_policy", `{"policy_id":"refund"}`),
	}, Answer: "Refunds are available within 30 days."}
	// docs:start policies-runner
	loop := react.NewAgent(model)
	runner := executor.NewRunner(loop, &gent.Config{ToolChain: chain})
	// Keep the embedder open until all conversations that use these tools have finished.
	// docs:end policies-runner
	answer, _ := runText(t, runner)
	assert.Equal(t, "Refunds are available within 30 days.", answer)
	assert.Len(t, model.Requests, 2)
	runCheck(t, &gent.Config{ToolChain: chain}, func(execCtx *gent.ExecutionContext) error {
		plan, err := chain.PrepareCalls(execCtx, []*gent.ModelToolCallRequest{
			toolCall("get_policy", `{"policy_id":"refund"}`)})
		if err != nil {
			return err
		}
		record, err := chain.ExecuteNext(execCtx, plan, format.NewXML())
		if err != nil {
			return err
		}
		assert.Equal(t, gent.ToolExecuted, record.Execution().Status)
		assert.Equal(t, `# refund

Refunds are available within 30 days.`,
			gent.ContentTextData(record.Execution().Parts))
		return nil
	})
}

func TestLocalEmbeddingExample(t *testing.T) {
	directory := t.TempDir()
	tokenizer := filepath.Join(directory, "tokenizer.json")
	assert.NoError(t, os.WriteFile(tokenizer, []byte(`{
  "version": "1.0",
  "model": {"type": "WordLevel", "vocab": {"[UNK]": 0, "x": 1}, "unk_token": "[UNK]"},
  "pre_tokenizer": {"type": "Whitespace"}
}`), 0600))
	config := common.ModelConfig{Model: common.ModelInfo{MaxTokenChunks: 16}, Dimensions: 2,
		Pooling: common.PoolingMean, InputNames: []string{"input_ids"},
		OutputName: "last_hidden_state"}
	embedder, err := localEmbedder(config, "../../search/testdata/quantized_matmul.onnx", tokenizer)
	if errors.Is(err, search.ErrOnnxUnavailable) {
		assert.Nil(t, embedder)
		t.Log("The native runtime is unavailable; verified the explicit unavailable result.")
		return
	}
	if !assert.NoError(t, err) {
		return
	}
	defer func() { assert.NoError(t, embedder.Close()) }()
	vector, err := embedder.EmbedText(context.Background(), "x")
	assert.NoError(t, err)
	norm := float32(math.Sqrt(64770*64770 + 32640*32640))
	assert.Equal(t, []float32{64770 / norm, 32640 / norm}, vector)
}
