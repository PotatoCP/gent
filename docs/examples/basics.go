// Package examples contains the source used by the documentation. Region markers select the
// teaching code; function wrappers and test setup stay in the complete source.
package examples

// docs:start imports
import (
	"context"
	"fmt"
	"os"

	"github.com/rickchristie/gent"
	"github.com/rickchristie/gent/agents/react"
	"github.com/rickchristie/gent/executor"
	"github.com/rickchristie/gent/models"
)

// docs:end imports

// HelloWorld runs the first example with the configured remote model.
func HelloWorld() error {
	// docs:start hello-world
	ctx := context.Background()
	model := models.NewOpenAI(
		os.Getenv("OPENAI_API_KEY"),
		os.Getenv("GENT_MODEL"),
	)
	loop := react.NewAgent(model)
	runner := executor.NewRunner(loop, &gent.Config{
		// Limits are opt-in. An empty Config has no local execution limits.
		Limits: gent.DefaultLimits(),
	})

	// Context creation does not call the model.
	execCtx, err := runner.NewExecutionContext(ctx, "hello")
	if err != nil {
		return err
	}
	defer execCtx.Release()

	task := gent.NewTask(gent.TextPart("Hello! Who are you?"))
	answer, result, err := runner.NewTurn(execCtx, task)
	if err != nil {
		return err
	}

	// A nil error can still mean execution paused, for example at a limit.
	fmt.Println("Status:", result.Status)
	fmt.Println("Answer:", answer)
	// docs:end hello-world
	return nil
}

func taskExamples() []*gent.Task {
	// docs:start task-text
	task := gent.NewTask(gent.TextPart("Explain how a Go channel works."))
	// docs:end task-text
	// docs:start task-instructions
	withInstructions := &gent.Task{
		Instructions: []*gent.ModelMessage{
			gent.TextParts(gent.ChatMessageTypeSystem, "Use short explanations."),
		},
		Messages: gent.NewTask(gent.TextPart("What is a channel?")).Messages,
	}
	// docs:end task-instructions
	// docs:start task-prefix
	// Nil instructions keep the accepted system prefix.
	keep := gent.NewTask(gent.TextPart("Show an example."))

	// A non-nil empty slice clears that prefix on the next admitted turn.
	clear := &gent.Task{
		Instructions: []*gent.ModelMessage{},
		Messages:     gent.NewTask(gent.TextPart("Explain it another way.")).Messages,
	}
	// docs:end task-prefix
	return []*gent.Task{task, withInstructions, keep, clear}
}

func conversation(ctx context.Context, model gent.Model) ([]string, error) {
	runner := executor.NewRunner(react.NewAgent(model), &gent.Config{})
	execCtx, err := runner.NewExecutionContext(ctx, "conversation")
	if err != nil {
		return nil, err
	}
	defer execCtx.Release()
	// docs:start conversation-turns
	first, firstResult, err := runner.NewTurn(execCtx,
		gent.NewTask(gent.TextPart("My project is called River.")))
	if err != nil {
		return nil, err
	}
	if firstResult.Status != gent.ExecutionFinished {
		return nil, fmt.Errorf("first task did not finish: %s", firstResult.Status)
	}
	// The same context keeps the RunId and the accepted conversation.
	second, secondResult, err := runner.NewTurn(execCtx,
		gent.NewTask(gent.TextPart("What is my project's name?")))
	if err != nil {
		return nil, err
	}
	// docs:end conversation-turns
	if secondResult.Status != gent.ExecutionFinished {
		return nil, fmt.Errorf("second task did not finish: %s", secondResult.Status)
	}
	// docs:start conversation-result
	// This reads accepted output. It does not run the loop or its validator.
	last, result, err := runner.GetLastTurnResult(execCtx)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("no completed turn is available")
	}
	// docs:end conversation-result
	return []string{first, second, last}, nil
}
