package executor

import (
	"net/http"
	"strings"
	"testing"
)

func TestValidateGeminiCLIProjectID(t *testing.T) {
	t.Parallel()

	if err := validateGeminiCLIProjectID("project-123"); err != nil {
		t.Fatalf("expected nil for valid project id, got %v", err)
	}

	err := validateGeminiCLIProjectID("   ")
	if err == nil {
		t.Fatal("expected error for empty project id")
	}

	status, ok := err.(statusErr)
	if !ok {
		t.Fatalf("expected statusErr, got %T", err)
	}
	if status.code != http.StatusBadRequest {
		t.Fatalf("unexpected status code: got %d", status.code)
	}
	if !strings.Contains(err.Error(), "project_id") {
		t.Fatalf("unexpected error message: %q", err.Error())
	}
}
