package aggregate

import (
	"sort"
	"strings"

	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/ticketbutler"
)

// Sponsor is one community sponsor, taken from the ticket they registered with.
//
// This carries personal data — a name, a work email address and an employer — so the
// endpoint that serves it is authenticated and sent Cache-Control: no-store.
type Sponsor struct {
	CompanyName    string `json:"company_name"`
	Email          string `json:"email"`
	FullName       string `json:"full_name"`
	TicketTypePK   int    `json:"ticket_type_pk"`
	TicketTypeName string `json:"ticket_type_name"`
}

// Sponsors extracts the community sponsors from an orders payload.
//
// Only the ticket types in opts.SponsorTicketTypePKs count, which replaces the
// ticket type id that used to be hardcoded in Sponsors.gs. Results are deduplicated
// by company name and sorted, so the sheet gets one row per sponsor in a stable
// order rather than one row per ticket in API order.
//
// Deduplication keeps the first ticket seen for a company, matching the old script:
// it appended a row only when the company was not already in the sheet.
func Sponsors(resp ticketbutler.OrdersResponse, opts Options) []Sponsor {
	if len(opts.SponsorTicketTypePKs) == 0 {
		return nil
	}
	wanted := make(map[int]bool, len(opts.SponsorTicketTypePKs))
	for _, pk := range opts.SponsorTicketTypePKs {
		wanted[pk] = true
	}

	var sponsors []Sponsor
	seen := map[string]bool{}
	for _, order := range resp.Orders {
		for _, ticket := range order.Tickets {
			if !wanted[ticket.TicketTypePK] {
				continue
			}
			// Company name is the sheet's key, so a ticket without one cannot be
			// placed and is skipped rather than added as a blank row.
			key := strings.ToLower(strings.TrimSpace(ticket.CompanyName))
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			sponsors = append(sponsors, Sponsor{
				CompanyName:    ticket.CompanyName,
				Email:          ticket.Email,
				FullName:       ticket.FullName,
				TicketTypePK:   ticket.TicketTypePK,
				TicketTypeName: ticket.TicketTypeName,
			})
		}
	}

	sort.Slice(sponsors, func(i, j int) bool {
		return strings.ToLower(sponsors[i].CompanyName) < strings.ToLower(sponsors[j].CompanyName)
	})
	return sponsors
}
