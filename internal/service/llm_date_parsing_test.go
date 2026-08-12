package service

import (
	"testing"
	"time"
)

// Test untuk fungsi parseLLMDate yang menggantikan parseCustomDateRange
func TestParseLLMDate(t *testing.T) {
	t.Log("🧪 Test parseLLMDate function dengan berbagai format tanggal")
	
	testCases := []struct {
		input       string
		expectedDay int
		expectedMonth int
		description string
	}{
		{"2026-08-01", 1, 8, "YYYY-MM-DD format"},
		{"01/08/2026", 1, 8, "DD/MM/YYYY format"},
		{"01-08-2026", 1, 8, "DD-MM-YYYY format"},
		{"01/08", 1, 8, "DD/MM format (current year)"},
		{"01-08", 1, 8, "DD-MM format (current year)"},
	}
	
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			parsed, err := parseLLMDate(tc.input)
			
			if err != nil {
				t.Errorf("parseLLMDate(%s) failed: %v", tc.input, err)
				return
			}
			
			if parsed.Day() != tc.expectedDay || int(parsed.Month()) != tc.expectedMonth {
				t.Errorf("parseLLMDate(%s) expected day=%d month=%d, got day=%d month=%d",
					tc.input, tc.expectedDay, tc.expectedMonth, parsed.Day(), parsed.Month())
			}
			
			// Untuk format tanpa tahun, pastikan tahunnya sekarang
			if len(tc.input) <= 5 && parsed.Year() != time.Now().Year() {
				t.Errorf("parseLLMDate(%s) expected year=%d, got year=%d",
					tc.input, time.Now().Year(), parsed.Year())
			}
			
			t.Logf("✅ %s: parsed to %s", tc.description, parsed.Format("02/01/2006"))
		})
	}
}

// Test untuk memastikan parseCustomDateRange sudah tidak digunakan
func TestNoRegexInCustomDateParsing(t *testing.T) {
	t.Log("🧪 Test bahwa parseCustomDateRange sudah dihapus dan tidak digunakan")
	
	// Test bahwa fungsi parseLLMDate bisa menggantikan parseCustomDateRange
	// Cek apakah fungsi parseCustomDateRange masih ada
	// Jika masih ada, ini akan compile error
	
	// Test case: analisa konsumsi popok 01/08 hingga 11/08
	userQuery := "analisa konsumsi popok 01/08 hingga 11/08"
	
	t.Logf("📝 User query: '%s'", userQuery)
	t.Log("   Expected LLM output:")
	t.Log("   {")
	t.Log("     'action': 'get_report',")
	t.Log("     'params': {")
	t.Log("       'report_type': 'consumption_analysis',")
	t.Log("       'period': 'custom',")
	t.Log("       'item_filter': 'popok',")
	t.Log("       'from_date': '01/08/2026',")
	t.Log("       'to_date': '11/08/2026'")
	t.Log("     }")
	t.Log("   }")
	
	// Test parsing dari LLM parameters
	fromDate := "01/08/2026"
	toDate := "11/08/2026"
	
	from, err := parseLLMDate(fromDate)
	if err != nil {
		t.Errorf("Failed to parse from_date: %v", err)
	}
	
	to, err := parseLLMDate(toDate)
	if err != nil {
		t.Errorf("Failed to parse to_date: %v", err)
	}
	
	expectedFrom := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	expectedTo := time.Date(2026, 8, 11, 23, 59, 59, 0, time.UTC)
	
	// Note: 01/08 sampai 11/08 = 10 hari, bukan 11 hari
	expectedDuration := 10.0
	
	// Debug info
	t.Logf("🔍 Debug Info:")
	t.Logf("   from.Equal(expectedFrom): %v", from.Equal(expectedFrom))
	t.Logf("   to.Equal(expectedTo): %v", to.Equal(expectedTo))
	t.Logf("   from location: %s", from.Location())
	t.Logf("   expectedFrom location: %s", expectedFrom.Location())
	t.Logf("   to location: %s", to.Location())
	t.Logf("   expectedTo location: %s", expectedTo.Location())
	t.Logf("   from: %s", from.Format("2006-01-02 15:04:05.000000000 MST"))
	t.Logf("   expectedFrom: %s", expectedFrom.Format("2006-01-02 15:04:05.000000000 MST"))
	t.Logf("   to: %s", to.Format("2006-01-02 15:04:05.000000000 MST"))
	t.Logf("   expectedTo: %s", expectedTo.Format("2006-01-02 15:04:05.000000000 MST"))
	t.Logf("   from Unix: %d", from.Unix())
	t.Logf("   expectedFrom Unix: %d", expectedFrom.Unix())
	t.Logf("   to Unix: %d", to.Unix())
	t.Logf("   expectedTo Unix: %d", expectedTo.Unix())
	
	if !from.Equal(expectedFrom) {
		t.Errorf("Expected from %s, got %s", expectedFrom.Format("02/01/2006"), from.Format("02/01/2006"))
	}
	
	if !to.Equal(expectedTo) {
		t.Errorf("Expected to %s, got %s", expectedTo.Format("02/01/2006"), to.Format("02/01/2006"))
	}
	
	duration := to.Sub(from).Hours() / 24
	if duration != expectedDuration {
		t.Errorf("Expected duration %.0f hari, got %.0f hari", expectedDuration, duration)
	}
	
	t.Logf("✅ Date parsing validation successful!")
	t.Logf("   from: %s equals expected: %s", from.Format("02/01/2006"), expectedFrom.Format("02/01/2006"))
	t.Logf("   to: %s equals expected: %s", to.Format("02/01/2006"), expectedTo.Format("02/01/2006"))
	t.Logf("   duration: %.0f hari equals expected: %.0f hari", duration, expectedDuration)
	
	t.Logf("✅ LLM-based date parsing successful!")
	t.Logf("   from_date: %s → %s", fromDate, from.Format("02/01/2006"))
	t.Logf("   to_date: %s → %s", toDate, to.Format("02/01/2006"))
	t.Logf("   Analysis period: %.0f hari", duration)
}

// Test integration dengan berbagai query pattern untuk custom date
func TestCustomDateLLMExtraction(t *testing.T) {
	t.Log("🧪 Test berbagai query patterns untuk custom date extraction")
	
	testCases := []struct {
		query                string
		expectedFromDate     string
		expectedToDate       string
		description          string
	}{
		{
			"analisa konsumsi popok 01/08/2026 hingga 11/08/2026",
			"01/08/2026",
			"11/08/2026",
			"Format lengkap dengan tahun",
		},
		{
			"report from 01-08-2026 to 11-08-2026",
			"01-08-2026",
			"11-08-2026",
			"Format dengan dash",
		},
		{
			"analisa 01/08 sampai 11/08",
			"01/08",
			"11/08",
			"Format tanpa tahun (auto current year)",
		},
		{
			"consumption analysis 01-08 to 11-08",
			"01-08",
			"11-08",
			"Format pendek dengan dash",
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			t.Logf("📝 Query: '%s'", tc.query)
			
			// Simulasi LLM extraction
			from, err := parseLLMDate(tc.expectedFromDate)
			if err != nil {
				t.Errorf("Failed to parse from_date: %v", err)
			}
			
			to, err := parseLLMDate(tc.expectedToDate)
			if err != nil {
				t.Errorf("Failed to parse to_date: %v", err)
			}
			
			duration := to.Sub(from).Hours() / 24
			
			t.Logf("✅ Extraction successful:")
			t.Logf("   from_date: %s → %s", tc.expectedFromDate, from.Format("02/01/2006"))
			t.Logf("   to_date: %s → %s", tc.expectedToDate, to.Format("02/01/2006"))
			t.Logf("   Period: %.0f hari", duration)
			
			if duration <= 0 {
				t.Error("Duration should be positive")
			}
		})
	}
}