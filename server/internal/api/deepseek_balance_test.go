package api

import (
	"net/http/httptest"
	"strings"
	"testing"

	"ant/server/internal/application"
)

func TestDeepSeekBalanceCapacityErrorIsRetryableRateLimit(t *testing.T) {
	response := httptest.NewRecorder()
	writeDeepSeekBalanceError(response, application.ErrDeepSeekBalanceBusy)
	if response.Code != 429 {
		t.Fatalf("status = %d, want 429", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, `"code":"Busy"`) || !strings.Contains(body, `"retryable":true`) || !strings.Contains(body, `"action":"retry_later"`) {
		t.Fatalf("capacity response is not a retryable busy response: %s", body)
	}
}
