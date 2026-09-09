// Package ticketbutler models the parts of the TicketButler v3 API this service
// consumes, and fetches them with a timeout long enough to actually succeed.
//
// The field set here follows the real payload of
// GET /api/v3/events/{event}/orders/. Monetary amounts arrive as decimal strings,
// so they are kept as strings and parsed on demand — see Amount.
package ticketbutler

import (
	"encoding/json"
	"strconv"
	"strings"
)

// OrdersResponse is the whole body of the orders endpoint. The endpoint has no
// pagination, filtering or field selection, so this is always every order for the
// event in a single response.
type OrdersResponse struct {
	UUID   string  `json:"uuid"`
	Orders []Order `json:"orders"`
}

// Order is one purchase, which may cover several tickets.
type Order struct {
	UUID          string          `json:"uuid"`
	OrderID       string          `json:"order_id"`
	Currency      string          `json:"currency"`
	State         string          `json:"state"`
	Date          string          `json:"date"`
	VATExempt     bool            `json:"vat_exempt"`
	VATRate       float64         `json:"vat_rate"`
	NewsLetter    bool            `json:"news_letter"`
	PaymentMethod string          `json:"payment_method"`
	Address       Address         `json:"address"`
	Discount      json.RawMessage `json:"discount,omitempty"`
	OrderLines    []OrderLine     `json:"order_lines"`
	Tickets       []Ticket        `json:"tickets"`
}

// Address is the billing address of the buyer, not of the attendees.
type Address struct {
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	Email        string `json:"email"`
	Phone        string `json:"phone"`
	BusinessName string `json:"business_name"`
}

// OrderLine is a basket line: a ticket type and a quantity. The per-attendee detail
// lives in Order.Tickets, so the aggregation works from those instead — order lines
// are carried through only for the raw passthrough endpoint.
type OrderLine struct {
	UUID                    string  `json:"uuid"`
	Title                   string  `json:"title"`
	Type                    string  `json:"type"`
	Quantity                int     `json:"quantity"`
	Price                   Amount  `json:"price"`
	Total                   Amount  `json:"total"`
	DiscountTotal           *Amount `json:"discount_total"`
	AmountOfRefundedTickets int     `json:"amount_of_refunded_tickets"`
	TicketDescription       *string `json:"ticket_description"`
	TicketTypeDate          string  `json:"ticket_type_date"`
	HasTicketTypeDate       bool    `json:"has_ticket_type_date"`
}

// Ticket is a single admission or merchandise item, one per attendee.
//
// Note that the API also returns a per-ticket "questions" array repeating the whole
// event question schema on every ticket, and duplicates the answers under both
// "answers" and "ticket_answer_collection". Those are deliberately not modelled:
// dropping them is most of why the stored snapshot is far smaller than the upstream
// response.
type Ticket struct {
	ID           int    `json:"id"`
	UUID         string `json:"uuid"`
	PurchaseUUID string `json:"purchase_uuid"`

	TicketTypePK   int    `json:"ticket_type_pk"`
	TicketTypeName string `json:"ticket_type_name"`

	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	FullName    string `json:"full_name"`
	Email       string `json:"email"`
	CompanyName string `json:"company_name"`

	Price      Amount `json:"price"`
	PriceTotal Amount `json:"price_total"`

	CheckedIn    bool `json:"checked_in"`
	TicketRefund bool `json:"ticket_refund"`

	AnswerCollection *AnswerCollection `json:"ticket_answer_collection"`
}

// AnswerCollection holds the answers an attendee gave to the event questions.
type AnswerCollection struct {
	UUID    string   `json:"uuid"`
	Answers []Answer `json:"answers"`
}

// Answer is one question/answer pair. Free-text answers land in AnswerValue;
// select-style answers land in AnsweredChoices.
type Answer struct {
	QuestionHeading string   `json:"question_heading"`
	QuestionUUID    string   `json:"question_uuid"`
	Variation       string   `json:"variation"`
	AnswerValue     string   `json:"answer_value"`
	AnsweredChoices []Choice `json:"answered_choices"`
}

// Choice is one selected option of a select-style question.
type Choice struct {
	ChoiceUUID    string `json:"choice_uuid"`
	ChoiceHeading string `json:"choice_heading"`
}

// FirstChoice returns the heading of the first selected option, or "" if the
// question was not answered. The Apps Script it replaces looked only at
// answered_choices[0], so this keeps that behaviour.
func (a Answer) FirstChoice() string {
	if len(a.AnsweredChoices) == 0 {
		return ""
	}
	return a.AnsweredChoices[0].ChoiceHeading
}

// Answers returns the ticket's answers, tolerating a missing answer collection.
func (t Ticket) Answers() []Answer {
	if t.AnswerCollection == nil {
		return nil
	}
	return t.AnswerCollection.Answers
}

// AnswerFor returns the answer to the question with the given heading. Headings are
// compared after trimming, matching the Apps Script that trimmed before comparing.
func (t Ticket) AnswerFor(heading string) (Answer, bool) {
	for _, a := range t.Answers() {
		if strings.TrimSpace(a.QuestionHeading) == heading {
			return a, true
		}
	}
	return Answer{}, false
}

// Amount is a monetary value. TicketButler sends these as JSON strings such as
// "1499.00"; a plain float64 field would fail to unmarshal.
type Amount float64

// UnmarshalJSON accepts either a JSON string or a bare number. An empty string or
// null decodes to zero, which is what the previous parseFloat-based code produced.
func (a *Amount) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*a = 0
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return err
	}
	*a = Amount(f)
	return nil
}

// MarshalJSON writes the amount back as a decimal string, so a raw passthrough
// response keeps the same shape the upstream API used.
func (a Amount) MarshalJSON() ([]byte, error) {
	return []byte(`"` + strconv.FormatFloat(float64(a), 'f', 2, 64) + `"`), nil
}

// Float returns the amount as a float64 for arithmetic.
func (a Amount) Float() float64 { return float64(a) }
