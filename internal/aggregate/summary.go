package aggregate

import (
	"strings"

	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/ticketbutler"
)

// SchemaVersion identifies the shape of Summary. Apps Script checks it, so bump it
// only when a change would break a client that has not been updated.
const SchemaVersion = 1

// The two synthetic group names the old script used. A ticket that cost nothing, or
// less than its list price, is reported under one of these instead of under its own
// ticket type, because that is how the REALIZED sheet is laid out.
const (
	GroupFree       = "Sponsors and other free tickets"
	GroupDiscounted = "Discounted"
)

// TShirtQuestion is the exact question heading whose answer feeds the t-shirt tally.
// Compared after trimming, as the old script did.
const TShirtQuestion = "T-Shirt Size"

// Summary is everything the spreadsheets need, computed once per refresh.
type Summary struct {
	SchemaVersion int    `json:"schema_version"`
	EventUUID     string `json:"event_uuid"`

	Counts        Counts         `json:"counts"`
	OrdersByState map[string]int `json:"orders_by_state"`

	// TicketGroups is keyed by the name the REALIZED sheet lists in column A —
	// either a ticket type name or one of the two synthetic groups above.
	TicketGroups map[string]TicketGroup `json:"ticket_groups"`

	// TShirtSizes counts the "T-Shirt Size" answers, which is a different thing
	// from the t-shirts sold as merchandise.
	TShirtSizes SizeCounts `json:"tshirt_sizes"`

	// Merch is keyed by merchandise name with the size suffix stripped.
	Merch map[string]SizeCounts `json:"merch"`
}

// Counts are the totals a human uses to sanity-check a refresh at a glance.
type Counts struct {
	Orders  int `json:"orders"`
	Tickets int `json:"tickets"`
}

// TicketGroup is one row of the REALIZED sheet: how many were sold and what they
// earned, excluding VAT.
type TicketGroup struct {
	Count       int     `json:"count"`
	IncomeExVAT float64 `json:"income_ex_vat"`
}

// Options configures the parts of the aggregation that are worth changing without a
// code change.
type Options struct {
	// MerchNamePatterns are lower-case substrings that mark a ticket type as
	// merchandise. The old script hardcoded the single pattern "hoodie".
	MerchNamePatterns []string

	// SponsorTicketTypePKs are the ticket type ids treated as community sponsors.
	SponsorTicketTypePKs []int
}

// Summarise computes the summary for one orders payload.
//
// Every ticket of every order is counted, whatever the order's state. That mirrors
// the old script, which never looked at `state`, so a cancelled or refunded order
// would inflate the sheet. Live data has so far been entirely PAID. OrdersByState is
// reported precisely so that assumption stops being invisible.
func Summarise(resp ticketbutler.OrdersResponse, opts Options) Summary {
	s := Summary{
		SchemaVersion: SchemaVersion,
		EventUUID:     resp.UUID,
		OrdersByState: map[string]int{},
		TicketGroups:  map[string]TicketGroup{},
		Merch:         map[string]SizeCounts{},
	}

	for _, order := range resp.Orders {
		s.Counts.Orders++
		s.OrdersByState[order.State]++

		for _, ticket := range order.Tickets {
			s.Counts.Tickets++

			group := ticket.TicketTypeName

			// Merchandise ticket types carry the size in the name, as
			// "Hoodie Zip Black - 2XL". Strip it so the sheet row matches, and tally
			// the size separately.
			if name, size, isMerch := splitMerch(group, opts.MerchNamePatterns); isMerch {
				group = name
				if size != "" {
					counts := s.Merch[name]
					// Add ignores a size it does not recognise, mirroring the old
					// script's `!== undefined` guard. The product is recorded either
					// way, so an odd size does not make the whole product vanish.
					counts.Add(size)
					s.Merch[name] = counts
				}
			}

			if answer, ok := ticket.AnswerFor(TShirtQuestion); ok {
				s.TShirtSizes.Add(NormalizeSize(answer.FirstChoice()))
			}

			income := ticket.PriceTotal.Float()
			price := ticket.Price.Float()

			// Regrouping happens after the merch name has been cleaned up, so a
			// complimentary hoodie is reported as a free ticket, exactly as before.
			switch {
			case income == 0:
				group = GroupFree
			case income < price:
				group = GroupDiscounted
			}

			// The sheet tracks income excluding VAT. VAT-exempt orders were never
			// charged it, so only the others are divided out.
			if !order.VATExempt {
				income /= 1 + order.VATRate
			}

			row := s.TicketGroups[group]
			row.Count++
			row.IncomeExVAT += income
			s.TicketGroups[group] = row
		}
	}

	return s
}

// splitMerch reports whether a ticket type name is merchandise and, if so, splits it
// into the product name and its normalised size.
//
// Detection is a substring match, defaulting to "hoodie" alone, because that is what
// the old script did. One consequence is worth stating: a line item named
// "T-shirt Green - XL" is not merchandise by this rule, so it keeps its full
// name-with-size and shows up as its own row of ticket groups. Extending
// MerchNamePatterns changes that, which is a decision about the sheet layout rather
// than about this code.
func splitMerch(name string, patterns []string) (product, size string, isMerch bool) {
	lower := strings.ToLower(name)
	for _, pattern := range patterns {
		if pattern != "" && strings.Contains(lower, pattern) {
			isMerch = true
			break
		}
	}
	if !isMerch {
		return name, "", false
	}

	parts := strings.Split(name, " - ")
	if len(parts) < 2 {
		// Merchandise with no size in the name: keep the name whole, tally nothing.
		return name, "", true
	}
	product = strings.TrimSpace(strings.Join(parts[:len(parts)-1], " - "))
	return product, NormalizeSize(parts[len(parts)-1]), true
}
