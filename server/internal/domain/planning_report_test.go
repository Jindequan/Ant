package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExecutionReportOutOfRangeFieldIsBackwardCompatible(t *testing.T) {
	legacy := `{"ReportID":"report-old","AttemptID":"attempt-old","Metrics":[{"MetricID":"tokens","Unit":"token","Actual":"7"}]}`
	var report ExecutionReport
	if err := json.Unmarshal([]byte(legacy), &report); err != nil {
		t.Fatalf("decode legacy report without out-of-range field: %v", err)
	}
	if len(report.Metrics) != 1 || report.Metrics[0].Actual == nil || *report.Metrics[0].Actual != 7 || report.Metrics[0].ActualOutOfRangeDecimal != "" {
		t.Fatalf("legacy report decoded with changed facts: %+v", report)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "ActualOutOfRangeDecimal") {
		t.Fatalf("ordinary reports must preserve the old JSON shape, got %s", raw)
	}

	report.Metrics[0].Actual = nil
	report.Metrics[0].ActualOutOfRangeDecimal = "9223372036854775808"
	raw, err = json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var replay ExecutionReport
	if err := json.Unmarshal(raw, &replay); err != nil || replay.Metrics[0].ActualOutOfRangeDecimal != report.Metrics[0].ActualOutOfRangeDecimal {
		t.Fatalf("exact out-of-range value did not survive report JSON: %+v err=%v", replay, err)
	}
}
