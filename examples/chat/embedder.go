package main

import (
	"context"
	"fmt"
	"hash/fnv"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/rickchristie/gent/common"
	"github.com/rickchristie/gent/search"
)

func scenarioEmbedder(provider string) (search.Embedder, error) {
	if provider == "demo" {
		return demoEmbedder{}, nil
	}
	config := common.ConfigsForModel("multilingual-e5-small")[0]
	if !common.ModelDownloaded(&config.Model) {
		return nil, fmt.Errorf("scenario policy search needs multilingual-e5-small; " +
			"run: gent setup onnx && gent model download multilingual-e5-small")
	}
	directory, err := common.ModelDir(config.Model.Name)
	if err != nil {
		return nil, err
	}
	return search.NewOnnxEmbedder(config, search.OnnxOptions{
		ModelPath:     filepath.Join(directory, config.Model.ModelFile),
		TokenizerPath: filepath.Join(directory, "tokenizer.json"), NumThreads: 2, MaxConcurrency: 2,
	})
}

// Demo mode uses word counts in fixed hash buckets. This supplies a deterministic lexical signal
// for the shared policy tools without a native model or download. It does not model semantic
// similarity. Real providers use the same multilingual ONNX model as the integration tests.
type demoEmbedder struct{}

func (demoEmbedder) TokenCount(text string) int { return len(strings.Fields(text)) }
func (demoEmbedder) MaxTokens() int             { return 512 }
func (demoEmbedder) Dimensions() int            { return 256 }
func (demoEmbedder) Close() error               { return nil }

func (embedder demoEmbedder) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	return embedder.EmbedText(ctx, text)
}

func (embedder demoEmbedder) EmbedText(ctx context.Context, text string) ([]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	vector := make([]float32, embedder.Dimensions())
	for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	}) {
		hash := fnv.New32a()
		_, _ = hash.Write([]byte(word)) // hash.Hash.Write cannot return an error.
		vector[int(hash.Sum32())%len(vector)]++
	}
	return vector, nil
}

func (embedder demoEmbedder) EmbedTextBatch(ctx context.Context, texts []string) (
	[][]float32, error,
) {
	vectors := make([][]float32, len(texts))
	for index, text := range texts {
		vector, err := embedder.EmbedText(ctx, text)
		if err != nil {
			return nil, err
		}
		vectors[index] = vector
	}
	return vectors, nil
}
