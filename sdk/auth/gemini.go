package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/auth/gemini"
	// legacy client removed
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

// GeminiAuthenticator implements the login flow for Google Gemini CLI accounts.
type GeminiAuthenticator struct{}

// NewGeminiAuthenticator constructs a Gemini authenticator.
func NewGeminiAuthenticator() *GeminiAuthenticator {
	return &GeminiAuthenticator{}
}

func (a *GeminiAuthenticator) Provider() string {
	return "gemini"
}

func (a *GeminiAuthenticator) RefreshLead() *time.Duration {
	return nil
}

func (a *GeminiAuthenticator) Login(ctx context.Context, cfg *config.Config, opts *LoginOptions) (*coreauth.Auth, error) {
	if cfg == nil {
		return nil, fmt.Errorf("cliproxy auth: configuration is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if opts == nil {
		opts = &LoginOptions{}
	}

	var ts gemini.GeminiTokenStorage
	if opts.ProjectID != "" {
		ts.ProjectID = opts.ProjectID
	}

	geminiAuth := gemini.NewGeminiAuth()
	httpClient, err := geminiAuth.GetAuthenticatedClient(ctx, &ts, cfg, &gemini.WebLoginOptions{
		NoBrowser:    opts.NoBrowser,
		CallbackPort: opts.CallbackPort,
		Prompt:       opts.Prompt,
	})
	if err != nil {
		return nil, fmt.Errorf("gemini authentication failed: %w", err)
	}

	if !shouldDeferGeminiOnboarding(opts) {
		projects, errResolve := gemini.ResolveAndOnboardProjects(ctx, httpClient, &ts, opts.ProjectID)
		if errResolve != nil {
			return nil, fmt.Errorf("gemini onboarding failed: %w", errResolve)
		}

		if !ts.Checked {
			for _, projectID := range projects {
				projectID = strings.TrimSpace(projectID)
				if projectID == "" {
					continue
				}
				checked, errCheck := gemini.CheckCloudAPIIsEnabled(ctx, httpClient, projectID)
				if errCheck != nil {
					return nil, fmt.Errorf("verify cloud ai api for %s: %w", projectID, errCheck)
				}
				if !checked {
					return nil, fmt.Errorf("cloud ai api is not enabled for project %s", projectID)
				}
			}
			ts.Checked = true
		}
	}

	record := buildGeminiAuthRecord(a.Provider(), &ts)

	fmt.Println("Gemini authentication successful")
	return record, nil
}

func buildGeminiAuthRecord(provider string, storage *gemini.GeminiTokenStorage) *coreauth.Auth {
	if storage == nil {
		storage = &gemini.GeminiTokenStorage{}
	}
	fileName := gemini.CredentialFileName(storage.Email, storage.ProjectID, true)
	metadata := map[string]any{
		"email":      storage.Email,
		"project_id": storage.ProjectID,
		"auto":       storage.Auto,
		"checked":    storage.Checked,
	}
	return &coreauth.Auth{
		ID:       fileName,
		Provider: provider,
		FileName: fileName,
		Storage:  storage,
		Metadata: metadata,
	}
}

func shouldDeferGeminiOnboarding(opts *LoginOptions) bool {
	if opts == nil || opts.Metadata == nil {
		return false
	}
	value, ok := opts.Metadata["defer_onboarding"]
	if !ok {
		return false
	}
	value = strings.TrimSpace(strings.ToLower(value))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}
