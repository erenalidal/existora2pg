package oracle

import (
	"log/slog"
	"testing"
)

func TestNewReader_FetchSizeDefaults(t *testing.T) {
	tests := []struct {
		name          string
		inputFetch    int
		wantFetchSize int
	}{
		{"zero defaults to 10000", 0, 10000},
		{"negative defaults to 10000", -1, 10000},
		{"negative large defaults to 10000", -999, 10000},
		{"explicit 5000 preserved", 5000, 5000},
		{"explicit 50000 preserved", 50000, 50000},
		{"explicit 1 preserved", 1, 1},
		{"explicit 10000 preserved", 10000, 10000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewReader(nil, tt.inputFetch, slog.Default())
			if r.fetchSize != tt.wantFetchSize {
				t.Errorf("NewReader(nil, %d, ...).fetchSize = %d, want %d",
					tt.inputFetch, r.fetchSize, tt.wantFetchSize)
			}
		})
	}
}
