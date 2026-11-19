package examples

import (
	"context"
	"errors"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/common"
	"github.com/rickchristie/gent/policy"
	"github.com/rickchristie/gent/search"
	"github.com/rickchristie/gent/toolchain"
)

func keywordSearch(ctx context.Context) (results []search.SearchResult, err error) {
	// docs:start search-keywords
	index, err := search.NewBleveIndex(&policy.PolicyBleveAdapter{})
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, index.Close()) }()
	if err := index.Add(ctx, "refund", &policy.Policy{
		Id: "refund", FullContent: "Refunds are available within 30 days.",
	}); err != nil {
		return nil, err
	}
	return index.Search(ctx, "refund", 3)
	// docs:end search-keywords
}

func vectorSearch(ctx context.Context, embedder search.Embedder) ([]search.SearchResult, error) {
	// docs:start search-vector
	index := search.NewFlatIndex(&policy.PolicyChunkAdapter{}, embedder)
	if err := index.Add(ctx, "refund", &policy.Policy{
		Id: "refund", FullContent: "Refunds are available within 30 days.",
	}); err != nil {
		return nil, err
	}
	return index.Search(ctx, "refund", 3)
	// The caller owns the shared embedder and closes it after all users finish.
	// docs:end search-vector
}

// docs:start search-adapter
type Article struct {
	Body string
}

type ArticleChunks struct{}

func (ArticleChunks) Chunks(
	article Article, counter search.TokenCounter, maxTokens int,
) ([]search.Chunk, error) {
	chunker := search.MarkdownChunker{
		// Use the selected model's tokenizer and stay within its input allowance.
		ChunkSize:  min(384, maxTokens),
		TokenCount: counter.TokenCount,
	}
	return chunker.Chunk(article.Body), nil
}

// docs:end search-adapter

func articleSearch(ctx context.Context, embedder search.Embedder) ([]search.SearchResult, error) {
	// docs:start search-article
	index := search.NewFlatIndex(ArticleChunks{}, embedder)
	if err := index.Add(ctx, "refund", Article{
		Body: "Refunds are available within 30 days.",
	}); err != nil {
		return nil, err
	}
	return index.Search(ctx, "refund", 3)
	// docs:end search-article
}

func fusedSearch(
	first, second search.SearchIndex[*policy.Policy],
) *search.FusedIndex[*policy.Policy] {
	// docs:start search-fused
	fuser := &search.WeightedLinearFuser{
		Weights:          map[string]float64{"keyword": 0.4, "vector": 0.6},
		NormalizeSources: map[string]bool{"keyword": true},
	}
	index := search.NewFusedIndex(fuser,
		map[string]search.SearchIndex[*policy.Policy]{"keyword": first, "vector": second},
		map[string]int{"keyword": 10, "vector": 10},
	)
	// docs:end search-fused
	return index
}

func policyTools(ctx context.Context, embedder search.Embedder) (gent.ToolChain, error) {
	// docs:start policies-register
	policies := []*policy.Policy{{
		Id:          "refund",
		FullContent: "Refunds are available within 30 days.",
		Keywords:    []string{"refund", "return"},
	}}
	find, err := policy.NewPolicySearchTool(ctx, embedder, policies)
	if err != nil {
		return nil, err
	}
	find.WithResultMode(policy.SearchResultSnippet).WithTopK(3)
	chain := toolchain.New()
	chain.RegisterTool(find)
	chain.RegisterTool(policy.NewGetPolicyTool(policies))
	// Search returns guidance. The application still enforces its action rules.
	// docs:end policies-register
	return chain, nil
}

func localEmbedder(config common.ModelConfig, modelPath, tokenizerPath string) (
	search.Embedder, error,
) {
	// docs:start embedder-create
	embedder, err := search.NewOnnxEmbedder(config, search.OnnxOptions{
		ModelPath:      modelPath,
		TokenizerPath:  tokenizerPath,
		NumThreads:     2,
		MaxConcurrency: 2,
	})
	if err != nil {
		return nil, err
	}
	// Close the embedder after all index and query work has stopped.
	// docs:end embedder-create
	return embedder, nil
}

func embedText(ctx context.Context, embedder search.Embedder) ([][]float32, error) {
	// docs:start embedder-use
	vectors, err := embedder.EmbedTextBatch(ctx, []string{"A refund policy.", "A delivery policy."})
	if err != nil {
		return nil, err
	}
	// EmbedQuery applies the query-side preparation for the selected model.
	query, err := embedder.EmbedQuery(ctx, "refund")
	if err != nil {
		return nil, err
	}
	// docs:end embedder-use
	return append(vectors, query), nil
}
