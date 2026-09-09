package aggregate_test

import (
	"testing"

	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/aggregate"
	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/ticketbutler"
)

func TestSponsors(t *testing.T) {
	orders := ticketbutler.OrdersResponse{Orders: []ticketbutler.Order{{
		Tickets: []ticketbutler.Ticket{
			{TicketTypePK: 183067, CompanyName: "Zebra ApS", Email: "a@zebra.example", FullName: "Alfa Testperson", TicketTypeName: "Community Sponsor"},
			{TicketTypePK: 183067, CompanyName: "Alpha A/S", Email: "b@alpha.example", FullName: "Bravo Testperson", TicketTypeName: "Community Sponsor"},
			// A second ticket for a company already seen, differing only in case.
			{TicketTypePK: 183067, CompanyName: "alpha a/s", Email: "c@alpha.example", FullName: "Charlie Testperson", TicketTypeName: "Community Sponsor"},
			// A sponsor ticket with no company cannot be placed in the sheet.
			{TicketTypePK: 183067, CompanyName: "  ", Email: "d@none.example"},
			// Not a sponsor ticket type.
			{TicketTypePK: 183090, CompanyName: "Beta GmbH", Email: "e@beta.example"},
		},
	}}}

	got := aggregate.Sponsors(orders, aggregate.Options{SponsorTicketTypePKs: []int{183067}})

	if len(got) != 2 {
		t.Fatalf("got %d sponsors (%+v), want 2", len(got), got)
	}
	// Sorted case-insensitively by company, so the sheet order is stable between runs.
	if got[0].CompanyName != "Alpha A/S" || got[1].CompanyName != "Zebra ApS" {
		t.Errorf("order = %q, %q; want Alpha A/S then Zebra ApS", got[0].CompanyName, got[1].CompanyName)
	}
	// Deduplication keeps the first ticket seen, matching the old script, which
	// appended a row only when the company was not already present.
	if got[0].Email != "b@alpha.example" {
		t.Errorf("email = %q, want the first ticket seen for the company", got[0].Email)
	}
}

func TestSponsorsWithoutConfiguredTypes(t *testing.T) {
	orders := ticketbutler.OrdersResponse{Orders: []ticketbutler.Order{{
		Tickets: []ticketbutler.Ticket{{TicketTypePK: 183067, CompanyName: "Alpha A/S"}},
	}}}
	if got := aggregate.Sponsors(orders, aggregate.Options{}); len(got) != 0 {
		t.Errorf("got %+v, want nothing when no sponsor ticket types are configured", got)
	}
}

func TestSponsorsFromFixture(t *testing.T) {
	got := aggregate.Sponsors(loadFixture(t), defaultOptions())
	if len(got) == 0 {
		t.Fatal("the fixture should contain community sponsors")
	}
	for _, s := range got {
		if s.CompanyName == "" || s.Email == "" {
			t.Errorf("incomplete sponsor: %+v", s)
		}
		if s.TicketTypePK != 183067 {
			t.Errorf("sponsor %q has ticket type %d, want only the configured 183067", s.CompanyName, s.TicketTypePK)
		}
	}
}
