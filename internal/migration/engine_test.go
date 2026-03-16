package migration

import (
	"testing"

	"github.com/erenalidal/existora2pg/internal/schema"
)

func TestParseHighValue_DateFormat(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "TO_DATE(' 2024-02-01 00:00:00', 'SYYYY-MM-DD HH24:MI:SS', 'NLS_CALENDAR=GREGORIAN')",
			expected: "'2024-02-01'",
		},
		{
			input:    "TIMESTAMP' 2024-03-01 00:00:00'",
			expected: "'2024-03-01'",
		},
		{
			input:    "MAXVALUE",
			expected: "MAXVALUE",
		},
		{
			input:    "",
			expected: "MAXVALUE",
		},
	}

	for _, tt := range tests {
		result := parseHighValue(tt.input)
		if result != tt.expected {
			t.Errorf("parseHighValue(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestBuildPartitionBounds(t *testing.T) {
	tbl := schema.Table{
		Name: "METER_READINGS",
		Partitioning: &schema.PartitionInfo{
			Strategy:   schema.PartitionRange,
			KeyColumns: []string{"READING_DATE"},
			Partitions: []schema.Partition{
				{Name: "P202401", HighValue: "TO_DATE(' 2024-02-01 00:00:00', 'SYYYY-MM-DD HH24:MI:SS')", Position: 1},
				{Name: "P202402", HighValue: "TO_DATE(' 2024-03-01 00:00:00', 'SYYYY-MM-DD HH24:MI:SS')", Position: 2},
				{Name: "P202403", HighValue: "TO_DATE(' 2024-04-01 00:00:00', 'SYYYY-MM-DD HH24:MI:SS')", Position: 3},
			},
		},
	}

	bounds := BuildPartitionBounds(tbl)

	if len(bounds) != 3 {
		t.Fatalf("expected 3 bounds, got %d", len(bounds))
	}

	// First partition: MINVALUE to '2024-02-01'
	if bounds[0].LowerBound != "MINVALUE" {
		t.Errorf("bounds[0].LowerBound = %q, want MINVALUE", bounds[0].LowerBound)
	}
	if bounds[0].UpperBound != "'2024-02-01'" {
		t.Errorf("bounds[0].UpperBound = %q, want '2024-02-01'", bounds[0].UpperBound)
	}
	if bounds[0].Name != "meter_readings_p202401" {
		t.Errorf("bounds[0].Name = %q, want meter_readings_p202401", bounds[0].Name)
	}

	// Second partition: '2024-02-01' to '2024-03-01'
	if bounds[1].LowerBound != "'2024-02-01'" {
		t.Errorf("bounds[1].LowerBound = %q, want '2024-02-01'", bounds[1].LowerBound)
	}
	if bounds[1].UpperBound != "'2024-03-01'" {
		t.Errorf("bounds[1].UpperBound = %q, want '2024-03-01'", bounds[1].UpperBound)
	}
}
