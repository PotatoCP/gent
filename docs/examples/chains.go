package examples

import (
	"time"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/toolchain"
)

func discovery(tool any) (*toolchain.SearchChain, error) {
	// docs:start tools-search
	chain := toolchain.NewSearch(toolchain.SearchHintDomainCategories).
		WithPageSize(5).
		RegisterEngine(toolchain.NewBleveToolSearcher()).
		RegisterEngine(toolchain.NewRegexToolSearcher())
	chain.RegisterTool(tool)
	if err := chain.Initialize(); err != nil {
		return nil, err
	}
	// Config.ToolChain can now use chain. Search does not add native target definitions.
	// docs:end tools-search
	return chain, nil
}

func programmatic(chain gent.ToolChain) *toolchain.JsToolChainWrapper {
	// docs:start tools-javascript
	wrapped := toolchain.NewJsToolChainWrapper(chain).
		WithCodeTimeout(10 * time.Second).
		WithIntegerMode(gent.PTCIntegerBigInt)
	// Use wrapped as Config.ToolChain. Inner tool effects use managed execution.
	// docs:end tools-javascript
	return wrapped
}

// docs:start chain-custom
type SelectedTools struct {
	*toolchain.Registry
	Allowed map[string]bool
}

func (chain *SelectedTools) NativeTools(execCtx *gent.ExecutionContext) (
	[]*gent.ToolDefinition, error,
) {
	definitions, err := chain.Registry.NativeTools(execCtx)
	if err != nil {
		return nil, err
	}
	var selected []*gent.ToolDefinition
	for _, definition := range definitions {
		if chain.Allowed[definition.Function.Name] {
			selected = append(selected, definition)
		}
	}
	return selected, nil
}

// docs:end chain-custom

var _ gent.ToolChain = (*SelectedTools)(nil)
