// Package aggregate turns a TicketButler orders payload into the numbers the Cloud
// Native Denmark spreadsheets need.
//
// It is a deliberate, behaviour-preserving port of the Apps Script that used to do
// this work in TicketSales.gs. Where that script did something surprising, this
// package does the same surprising thing and says so in a comment: the point of the
// port was to move the work off Apps Script, not to change any published number. The
// quirks worth knowing about are listed in the repository README.
package aggregate

import "strings"

// SizeCounts is a tally per garment size.
//
// It is a struct rather than a map so the JSON field order is the order a human
// reads sizes in, and so an unrecognised size cannot silently create a new key.
type SizeCounts struct {
	Small  int `json:"small"`
	Medium int `json:"medium"`
	Large  int `json:"large"`
	XL     int `json:"xl"`
	XXL    int `json:"2xl"`
	XXXL   int `json:"3xl"`
}

// Add increments the count for a normalised size and reports whether it was a size
// this tally knows. Unknown sizes are ignored, matching the old script: its
// `tshirtSizes[normalized] !== undefined` guard meant an answer such as
// "Do not want a t-shirt" — which the real data does contain — was counted nowhere.
func (s *SizeCounts) Add(size string) bool {
	switch size {
	case "small":
		s.Small++
	case "medium":
		s.Medium++
	case "large":
		s.Large++
	case "xl":
		s.XL++
	case "2xl":
		s.XXL++
	case "3xl":
		s.XXXL++
	default:
		return false
	}
	return true
}

// Total is the number of recognised sizes counted.
func (s SizeCounts) Total() int {
	return s.Small + s.Medium + s.Large + s.XL + s.XXL + s.XXXL
}

// NormalizeSize maps the many spellings of a garment size onto one canonical form.
//
// A port of normalizeSize from TicketSales.gs, including its fallthrough: an input
// it does not recognise comes back trimmed and lower-cased rather than rejected, so
// the caller decides what to do with it.
func NormalizeSize(size string) string {
	switch s := strings.ToLower(strings.TrimSpace(size)); s {
	case "s", "small":
		return "small"
	case "m", "medium":
		return "medium"
	case "l", "large":
		return "large"
	case "xl", "extra large":
		return "xl"
	case "2xl", "xxl":
		return "2xl"
	case "3xl", "xxxl":
		return "3xl"
	default:
		return s
	}
}
