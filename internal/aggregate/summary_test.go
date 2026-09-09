package aggregate_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/aggregate"
	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/ticketbutler"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func defaultOptions() aggregate.Options {
	return aggregate.Options{
		MerchNamePatterns:    []string{"hoodie"},
		SponsorTicketTypePKs: []int{183067},
	}
}

func loadFixture(t *testing.T) ticketbutler.OrdersResponse {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "testdata", "orders.sample.json"))
	if err != nil {
		t.Fatalf("opening the fixture: %v", err)
	}
	defer f.Close()

	var resp ticketbutler.OrdersResponse
	if err := json.NewDecoder(f).Decode(&resp); err != nil {
		t.Fatalf("decoding the fixture: %v", err)
	}
	return resp
}

// TestSummariseGolden pins the whole aggregation against a recorded result.
//
// The fixture is an anonymised copy of a real payload whose numbers were verified to
// match the old Apps Script exactly (see testdata/parity), so a diff here means the
// numbers the spreadsheets show would have changed.
func TestSummariseGolden(t *testing.T) {
	got := aggregate.Summarise(loadFixture(t), defaultOptions())

	encoded, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatalf("encoding the summary: %v", err)
	}
	encoded = append(encoded, '\n')

	golden := filepath.Join("..", "..", "testdata", "summary.golden.json")
	if *update {
		if err := os.WriteFile(golden, encoded, 0o644); err != nil {
			t.Fatalf("writing the golden file: %v", err)
		}
		t.Log("golden file rewritten")
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("reading the golden file (run: go test ./internal/aggregate -update): %v", err)
	}
	if string(encoded) != string(want) {
		t.Errorf("the summary changed.\n--- want\n%s\n--- got\n%s", want, encoded)
	}
}

// TestSummariseFixtureTotals states the fixture's expected numbers outright, so a
// reader can see what the aggregation produces without opening the golden file, and
// so a regenerated golden file cannot quietly bless a wrong result.
func TestSummariseFixtureTotals(t *testing.T) {
	got := aggregate.Summarise(loadFixture(t), defaultOptions())

	if got.Counts.Orders != 63 || got.Counts.Tickets != 130 {
		t.Errorf("counts = %+v, want 63 orders and 130 tickets", got.Counts)
	}
	if want := map[string]int{"PAID": 63}; len(got.OrdersByState) != 1 || got.OrdersByState["PAID"] != want["PAID"] {
		t.Errorf("orders by state = %v, want %v", got.OrdersByState, want)
	}
	wantSizes := aggregate.SizeCounts{Small: 6, Medium: 26, Large: 36, XL: 29, XXL: 11, XXXL: 9}
	if got.TShirtSizes != wantSizes {
		t.Errorf("t-shirt sizes = %+v, want %+v", got.TShirtSizes, wantSizes)
	}
	// 5 answers of "Do not want a t-shirt" are counted nowhere, so the tally is
	// short of the ticket count by exactly those.
	if total := got.TShirtSizes.Total(); total != 117 {
		t.Errorf("t-shirt total = %d, want 117 (130 tickets less 8 without a t-shirt answer and 5 declining one)", total)
	}
	wantMerch := map[string]aggregate.SizeCounts{
		"Hoodie Zip Black": {Large: 1, XXL: 1},
		"Hoodie Black":     {XXL: 1},
		"Hoodie Blue Navy": {XL: 1},
	}
	if len(got.Merch) != len(wantMerch) {
		t.Fatalf("merch = %+v, want %+v", got.Merch, wantMerch)
	}
	for name, want := range wantMerch {
		if got.Merch[name] != want {
			t.Errorf("merch[%q] = %+v, want %+v", name, got.Merch[name], want)
		}
	}
	// "T-shirt Green - XL" and friends are not merchandise by the "hoodie" rule, so
	// they keep their size in the group name. This is the documented quirk.
	if _, ok := got.TicketGroups["T-shirt Green - XL"]; !ok {
		t.Error(`expected a "T-shirt Green - XL" ticket group: t-shirt merchandise is not matched by the "hoodie" pattern`)
	}
}

func TestSummariseGrouping(t *testing.T) {
	// One order, VAT 25%, three tickets: full price, discounted, and free.
	orders := ticketbutler.OrdersResponse{
		UUID: "event",
		Orders: []ticketbutler.Order{{
			State:   "PAID",
			VATRate: 0.25,
			Tickets: []ticketbutler.Ticket{
				{TicketTypeName: "Standard", Price: 1000, PriceTotal: 1000},
				{TicketTypeName: "Standard", Price: 1000, PriceTotal: 500},
				{TicketTypeName: "Standard", Price: 1000, PriceTotal: 0},
			},
		}},
	}
	got := aggregate.Summarise(orders, defaultOptions())

	for _, tc := range []struct {
		group  string
		count  int
		income float64
	}{
		{"Standard", 1, 800},
		{aggregate.GroupDiscounted, 1, 400},
		{aggregate.GroupFree, 1, 0},
	} {
		row, ok := got.TicketGroups[tc.group]
		if !ok {
			t.Errorf("group %q missing from %v", tc.group, got.TicketGroups)
			continue
		}
		if row.Count != tc.count || row.IncomeExVAT != tc.income {
			t.Errorf("group %q = %+v, want count %d income %v", tc.group, row, tc.count, tc.income)
		}
	}
}

func TestSummariseVATExemptIsNotDivided(t *testing.T) {
	orders := ticketbutler.OrdersResponse{Orders: []ticketbutler.Order{{
		VATExempt: true,
		VATRate:   0.25,
		Tickets:   []ticketbutler.Ticket{{TicketTypeName: "Standard", Price: 1000, PriceTotal: 1000}},
	}}}
	got := aggregate.Summarise(orders, defaultOptions())
	if income := got.TicketGroups["Standard"].IncomeExVAT; income != 1000 {
		t.Errorf("income = %v, want 1000: a VAT-exempt order was never charged VAT, so none is divided out", income)
	}
}

func TestSummariseFreeMerchIsReportedAsFree(t *testing.T) {
	// A complimentary hoodie: the size still counts as merchandise, but the money
	// row is the free-tickets row. This ordering is inherited from the old script.
	orders := ticketbutler.OrdersResponse{Orders: []ticketbutler.Order{{
		Tickets: []ticketbutler.Ticket{{TicketTypeName: "Hoodie Black - L", Price: 300, PriceTotal: 0}},
	}}}
	got := aggregate.Summarise(orders, defaultOptions())

	if want := (aggregate.SizeCounts{Large: 1}); got.Merch["Hoodie Black"] != want {
		t.Errorf("merch = %+v, want %+v", got.Merch, want)
	}
	if _, ok := got.TicketGroups[aggregate.GroupFree]; !ok {
		t.Errorf("ticket groups = %v, want the free group", got.TicketGroups)
	}
	if _, ok := got.TicketGroups["Hoodie Black"]; ok {
		t.Error("a free hoodie should not also appear as its own ticket group")
	}
}

func TestSummariseMerchWithoutSize(t *testing.T) {
	orders := ticketbutler.OrdersResponse{Orders: []ticketbutler.Order{{
		Tickets: []ticketbutler.Ticket{{TicketTypeName: "Hoodie Black", Price: 300, PriceTotal: 300}},
	}}}
	got := aggregate.Summarise(orders, defaultOptions())
	if len(got.Merch) != 0 {
		t.Errorf("merch = %+v, want nothing: the name carries no size to tally", got.Merch)
	}
	if row := got.TicketGroups["Hoodie Black"]; row.Count != 1 {
		t.Errorf("ticket groups = %v, want one Hoodie Black", got.TicketGroups)
	}
}

func TestSummariseUnknownMerchSizeStillRecordsTheProduct(t *testing.T) {
	orders := ticketbutler.OrdersResponse{Orders: []ticketbutler.Order{{
		Tickets: []ticketbutler.Ticket{{TicketTypeName: "Hoodie Black - 7XL", Price: 300, PriceTotal: 300}},
	}}}
	got := aggregate.Summarise(orders, defaultOptions())
	counts, ok := got.Merch["Hoodie Black"]
	if !ok {
		t.Fatalf("merch = %+v, want the product recorded even with an unrecognised size", got.Merch)
	}
	if counts.Total() != 0 {
		t.Errorf("merch counts = %+v, want all zero: 7XL is not a size this tally knows", counts)
	}
}

func TestSummariseTShirtAnswers(t *testing.T) {
	ticket := func(choice string) ticketbutler.Ticket {
		return ticketbutler.Ticket{
			TicketTypeName: "Standard", Price: 100, PriceTotal: 100,
			AnswerCollection: &ticketbutler.AnswerCollection{Answers: []ticketbutler.Answer{{
				// Padded, because the old script trimmed the heading before comparing.
				QuestionHeading: "  T-Shirt Size  ",
				AnsweredChoices: []ticketbutler.Choice{{ChoiceHeading: choice}},
			}}},
		}
	}
	orders := ticketbutler.OrdersResponse{Orders: []ticketbutler.Order{{Tickets: []ticketbutler.Ticket{
		ticket("Medium"), ticket("XXL"), ticket("Do not want a t-shirt"), ticket(""),
	}}}}

	got := aggregate.Summarise(orders, defaultOptions())
	want := aggregate.SizeCounts{Medium: 1, XXL: 1}
	if got.TShirtSizes != want {
		t.Errorf("t-shirt sizes = %+v, want %+v: declining a t-shirt and answering nothing are both counted nowhere", got.TShirtSizes, want)
	}
}

func TestSummariseCountsEveryOrderState(t *testing.T) {
	// Documents the inherited behaviour: state is not consulted, so a cancelled
	// order still counts. OrdersByState is what makes that visible.
	orders := ticketbutler.OrdersResponse{Orders: []ticketbutler.Order{
		{State: "PAID", Tickets: []ticketbutler.Ticket{{TicketTypeName: "Standard", Price: 100, PriceTotal: 100}}},
		{State: "CANCELLED", Tickets: []ticketbutler.Ticket{{TicketTypeName: "Standard", Price: 100, PriceTotal: 100}}},
	}}
	got := aggregate.Summarise(orders, defaultOptions())
	if got.TicketGroups["Standard"].Count != 2 {
		t.Errorf("count = %d, want 2: the old script never filtered on order state", got.TicketGroups["Standard"].Count)
	}
	if got.OrdersByState["CANCELLED"] != 1 {
		t.Errorf("orders by state = %v, want a CANCELLED entry", got.OrdersByState)
	}
}

func TestNormalizeSize(t *testing.T) {
	for input, want := range map[string]string{
		"S": "small", "small": "small", " Small ": "small",
		"M": "medium", "Medium": "medium",
		"L": "large", "LARGE": "large",
		"XL": "xl", "Extra Large": "xl",
		"2XL": "2xl", "XXL": "2xl",
		"3XL": "3xl", "XXXL": "3xl",
		"": "", "Do not want a t-shirt": "do not want a t-shirt",
	} {
		if got := aggregate.NormalizeSize(input); got != want {
			t.Errorf("NormalizeSize(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestMerchPatternsAreConfigurable(t *testing.T) {
	orders := ticketbutler.OrdersResponse{Orders: []ticketbutler.Order{{
		Tickets: []ticketbutler.Ticket{{TicketTypeName: "T-shirt Green - XL", Price: 140, PriceTotal: 140}},
	}}}

	// With the default patterns this is not merchandise, which is the quirk.
	if got := aggregate.Summarise(orders, defaultOptions()); len(got.Merch) != 0 {
		t.Errorf("merch = %+v, want nothing with the default patterns", got.Merch)
	}
	// Adding a pattern is all it takes to change that.
	opts := defaultOptions()
	opts.MerchNamePatterns = append(opts.MerchNamePatterns, "t-shirt")
	got := aggregate.Summarise(orders, opts)
	if want := (aggregate.SizeCounts{XL: 1}); got.Merch["T-shirt Green"] != want {
		t.Errorf("merch = %+v, want T-shirt Green %+v", got.Merch, want)
	}
}
