package api

import (
	"math"
	"strconv"
	"testing"
)

// B-223: an unbounded `page` overflowed (page-1)*perPage into a negative
// OFFSET (a 500) or wrapped the int32 offsets alerts.go/users.go compute.
func TestPagination_ClampsPageAndPerPage(t *testing.T) {
	tests := []struct {
		name, page, perPage   string
		max                   int
		wantPage, wantPerPage int
	}{
		{"defaults", "", "", 100, 1, 25},
		{"normal", "3", "50", 100, 3, 50},
		{"zero and negative fall back", "0", "-5", 100, 1, 25},
		{"non-numeric falls back", "x", "y", 100, 1, 25},
		{"per_page capped", "1", "1000", 100, 1, 100},
		{"page at bound kept", "1000000", "25", 100, maxPaginationPage, 25},
		{"page above bound clamped", "1000001", "25", 100, maxPaginationPage, 25},
		{"max int64 page clamped", strconv.FormatInt(math.MaxInt64, 10), "100", 100, maxPaginationPage, 100},
		{"page beyond int64 falls back", "99999999999999999999", "25", 100, 1, 25},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, perPage := pagination(tt.page, tt.perPage, 25, tt.max)
			if page != tt.wantPage || perPage != tt.wantPerPage {
				t.Fatalf("pagination(%q,%q)=(%d,%d) want (%d,%d)", tt.page, tt.perPage, page, perPage, tt.wantPage, tt.wantPerPage)
			}
		})
	}
}

// The int32 bound is enforced by pagination() itself, not by today's callers
// happening to pass a small maxPerPage: any per_page cap keeps the worst-case
// offset int32-safe.
func TestPagination_WorstCaseOffsetFitsInt32ForAnyMaxPerPage(t *testing.T) {
	for _, maxPerPage := range []int{1, 100, 500, 2147, 5000, 100_000, math.MaxInt32} {
		per := strconv.Itoa(maxPerPage)
		page, perPage := pagination(strconv.FormatInt(math.MaxInt64, 10), per, 25, maxPerPage)
		offset := (page - 1) * perPage
		if page < 1 || offset < 0 || offset > math.MaxInt32 || int(int32(offset)) != offset {
			t.Fatalf("maxPerPage=%d: page=%d offset=%d does not fit int32", maxPerPage, page, offset)
		}
	}
}
