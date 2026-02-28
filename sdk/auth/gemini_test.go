package auth

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/auth/gemini"
)

func TestBuildGeminiAuthRecord_UsesCanonicalFileNameAndMetadata(t *testing.T) {
	t.Parallel()

	storage := &gemini.GeminiTokenStorage{
		Email:     "user@example.com",
		ProjectID: "proj-a,proj-b",
		Auto:      true,
		Checked:   true,
	}

	record := buildGeminiAuthRecord("gemini", storage)
	if record == nil {
		t.Fatal("buildGeminiAuthRecord() returned nil")
	}
	if record.FileName != "gemini-user@example.com-all.json" {
		t.Fatalf("unexpected file name: got %q", record.FileName)
	}
	if record.ID != record.FileName {
		t.Fatalf("expected ID %q, got %q", record.FileName, record.ID)
	}
	if record.Provider != "gemini" {
		t.Fatalf("unexpected provider: got %q", record.Provider)
	}

	if got, _ := record.Metadata["email"].(string); got != "user@example.com" {
		t.Fatalf("unexpected metadata email: got %q", got)
	}
	if got, _ := record.Metadata["project_id"].(string); got != "proj-a,proj-b" {
		t.Fatalf("unexpected metadata project_id: got %q", got)
	}
	if got, _ := record.Metadata["auto"].(bool); !got {
		t.Fatalf("unexpected metadata auto: got %v", got)
	}
	if got, _ := record.Metadata["checked"].(bool); !got {
		t.Fatalf("unexpected metadata checked: got %v", got)
	}
}

func TestShouldDeferGeminiOnboarding(t *testing.T) {
	t.Parallel()

	if shouldDeferGeminiOnboarding(nil) {
		t.Fatal("expected false for nil options")
	}
	if shouldDeferGeminiOnboarding(&LoginOptions{}) {
		t.Fatal("expected false when metadata is absent")
	}

	tests := []struct {
		name     string
		value    string
		expected bool
	}{
		{name: "true", value: "true", expected: true},
		{name: "yes", value: "yes", expected: true},
		{name: "one", value: "1", expected: true},
		{name: "on", value: "on", expected: true},
		{name: "false", value: "false", expected: false},
		{name: "empty", value: "", expected: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			opts := &LoginOptions{
				Metadata: map[string]string{
					"defer_onboarding": tt.value,
				},
			}
			got := shouldDeferGeminiOnboarding(opts)
			if got != tt.expected {
				t.Fatalf("shouldDeferGeminiOnboarding() = %v, want %v", got, tt.expected)
			}
		})
	}
}
