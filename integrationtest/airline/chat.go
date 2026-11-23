package airline

import (
	"fmt"

	"github.com/rickchristie/gent/integrationtest/testutil"
)

// CustomerRequest is the initial task used by the integration scenario and the web chat.
const CustomerRequest = `Hi, I'm John Smith and my email is john.smith@email.com.
I have a flight booked for tomorrow (flight AA100 from JFK to LAX) but my meeting is running ` +
	`late.
Can you help me reschedule to a later flight on the same day? I'd prefer an evening flight if ` +
	`possible.`

// InvestigationRequest adds a comparison task without changing the original scenario fixture.
const InvestigationRequest = CustomerRequest + `

Before making a change, compare the available later flights, arrival times, seats, and total
change cost under my fare rules. Explain the tradeoff between the lowest cost and the latest
departure. Give brief progress updates while you check the records and policies. Do not change
my booking until I choose an option.`

// NewChatConfig creates one fixture with the prompts and tools used by the server chat.
// Keep this value for the conversation. Recreating it during recovery would reset the simulated
// external service. Separate conversations need separate values so their changes stay isolated.
func NewChatConfig(config testutil.TestConfig) testutil.ChatConfig {
	fixture := NewAirlineFixture(nil, config.Embedder)
	tp := fixture.TimeProvider()

	return testutil.ChatConfig{
		Name: "airline",
		SystemPrompt: fmt.Sprintf(`## Task Description

You are a helpful airline customer service agent for SkyWings `+
			`Airlines.
Your role is to assist customers with their flight bookings, `+
			`including checking flight information,
rescheduling flights, and answering policy questions.

Today is %s (%s).

Always be polite and professional. When rescheduling, make `+
			`sure to:
1. Verify the customer's identity and booking
2. Check the airline's change policy
3. Search for available alternative flights
4. Inform the customer of any fees before making changes
5. Confirm the change and provide updated booking details

SkyWings is an international airline. Reply with customer's `+
			`language.
`, tp.Today(), tp.Weekday()),
		CriticalRules: testutil.CriticalRules(config),

		MaxIterations:   15,
		RegisterTools:   fixture.RegisterAllTools,
		PolicySuggester: fixture.PolicySuggestionPrompt,
	}
}
