package ecommerce

import (
	"fmt"

	"github.com/rickchristie/gent/integrationtest/testutil"
)

// CustomerRequest is the initial task used by the integration scenario and the web chat.
const CustomerRequest = `Hi, I'm Alex Rivera and my email is alex.rivera@email.com.
I just noticed that I was charged twice for a "Mighty Mouse" purchase I made about a week ` +
	`ago. The charge shows up two times on my credit card statement, both for $79.99. Can you look ` +
	`into this and help me get the duplicate charge resolved?`

// InvestigationRequest adds reconciliation work with the same billing records and policy steps.
const InvestigationRequest = CustomerRequest + `

Reconcile the order, internal payment records, and live gateway status before choosing a remedy.
Explain any difference between those records. Follow the required resolution steps and report
what happens if a refund or adjustment fails. Give brief progress updates while you investigate,
then summarize the current case status and what I must do next.`

// NewChatConfig creates one fixture with the prompts and tools used by the server chat.
// Keep this value for the conversation. Recreating it during recovery would reset the simulated
// external service. Separate conversations need separate values so their changes stay isolated.
func NewChatConfig(config testutil.TestConfig) testutil.ChatConfig {
	fixture := NewEcommerceFixture(nil, config.Embedder)
	tp := fixture.TimeProvider()

	return testutil.ChatConfig{
		Name: "ecommerce",
		SystemPrompt: fmt.Sprintf(
			`## Task Description

You are a helpful billing support agent for TechGear `+
				`Online Store.
Your role is to assist customers with billing `+
				`inquiries, investigate payment `+
				`issues, and resolve disputes `+
				`following company procedures.

Today is %s (%s).

Always be polite and professional. When handling `+
				`billing issues:
1. Verify the customer's identity
2. Look up the relevant order and payment records
3. Search company guidance policies for the `+
				`correct procedure
4. Follow the resolution steps in order
5. Provide clear updates to the customer at each step
6. If automated resolution fails, escalate `+
				`appropriately

Important: Our internal payment records may be `+
				`out of date. Always verify with the `+
				`payment gateway for real-time status.
`, tp.Today(), tp.Weekday()),
		CriticalRules: testutil.CriticalRules(config,
			"Follow the policy steps IN ORDER "+
				"— do not skip steps",
		),

		MaxIterations:   20,
		RegisterTools:   fixture.RegisterAllTools,
		PolicySuggester: fixture.PolicySuggestionPrompt,
	}
}
