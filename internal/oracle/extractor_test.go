package oracle

import "testing"

func TestMatchFilter_NoFilter(t *testing.T) {
	f := TableFilter{}
	if !matchFilter("ANY_TABLE", f) {
		t.Error("expected match with empty filter")
	}
}

func TestMatchFilter_Include(t *testing.T) {
	f := TableFilter{Include: []string{"CUSTOMER*", "ORDER*"}}

	tests := []struct {
		name    string
		matches bool
	}{
		{"CUSTOMERS", true},
		{"CUSTOMER_ARCHIVE", true},
		{"ORDERS", true},
		{"PRODUCTS", false},
	}

	for _, tt := range tests {
		if got := matchFilter(tt.name, f); got != tt.matches {
			t.Errorf("matchFilter(%q) = %v, want %v", tt.name, got, tt.matches)
		}
	}
}

func TestMatchFilter_Exclude(t *testing.T) {
	f := TableFilter{Exclude: []string{"*_BACKUP", "*_TMP"}}

	tests := []struct {
		name    string
		matches bool
	}{
		{"CUSTOMERS", true},
		{"CUSTOMERS_BACKUP", false},
		{"DATA_TMP", false},
	}

	for _, tt := range tests {
		if got := matchFilter(tt.name, f); got != tt.matches {
			t.Errorf("matchFilter(%q) = %v, want %v", tt.name, got, tt.matches)
		}
	}
}

func TestMatchFilter_IncludeAndExclude(t *testing.T) {
	f := TableFilter{
		Include: []string{"ORDER*"},
		Exclude: []string{"*_BACKUP"},
	}

	if !matchFilter("ORDERS", f) {
		t.Error("ORDERS should match")
	}
	if matchFilter("ORDER_BACKUP", f) {
		t.Error("ORDER_BACKUP should be excluded")
	}
	if matchFilter("CUSTOMERS", f) {
		t.Error("CUSTOMERS should not match include")
	}
}

func TestIsNotNullCheck(t *testing.T) {
	tests := []struct {
		condition string
		expected  bool
	}{
		{`"CUSTOMER_ID" IS NOT NULL`, true},
		{`NAME IS NOT NULL`, true},
		{`price >= 0`, false},
		{`status IN ('A','I')`, false},
	}

	for _, tt := range tests {
		if got := isNotNullCheck(tt.condition); got != tt.expected {
			t.Errorf("isNotNullCheck(%q) = %v, want %v", tt.condition, got, tt.expected)
		}
	}
}
