package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	"github.com/tidwall/gjson"
)

const (
	geminiCLIEndpoint       = "https://cloudcode-pa.googleapis.com"
	geminiCLIVersion        = "v1internal"
	geminiCLIUserAgent      = "google-api-nodejs-client/9.15.1"
	geminiCLIApiClient      = "gl-node/22.17.0"
	geminiCLIClientMetadata = "ideType=IDE_UNSPECIFIED,platform=PLATFORM_UNSPECIFIED,pluginType=GEMINI"
)

// ResolveAndOnboardProjects ensures Gemini CLI onboarding completes and project IDs are resolved.
// It supports three request modes:
// - explicit project ID
// - empty project ID (auto-select first available)
// - special IDs: ALL, GOOGLE_ONE
func ResolveAndOnboardProjects(ctx context.Context, httpClient *http.Client, storage *GeminiTokenStorage, requestedProject string) ([]string, error) {
	if storage == nil {
		return nil, fmt.Errorf("gemini storage is nil")
	}
	if httpClient == nil {
		return nil, fmt.Errorf("gemini http client is nil")
	}

	trimmedRequest := strings.TrimSpace(requestedProject)

	switch {
	case strings.EqualFold(trimmedRequest, "ALL"):
		storage.Auto = false
		projects, err := fetchGCPProjects(ctx, httpClient)
		if err != nil {
			return nil, fmt.Errorf("fetch project list: %w", err)
		}
		if len(projects) == 0 {
			return nil, fmt.Errorf("no Google Cloud projects available for this account")
		}

		activated := make([]string, 0, len(projects))
		seen := make(map[string]struct{}, len(projects))
		for _, project := range projects {
			candidate := strings.TrimSpace(project.ProjectID)
			if candidate == "" {
				continue
			}
			if _, dup := seen[candidate]; dup {
				continue
			}
			if err := performGeminiCLISetup(ctx, httpClient, storage, candidate); err != nil {
				return nil, fmt.Errorf("onboard project %s: %w", candidate, err)
			}
			finalID := strings.TrimSpace(storage.ProjectID)
			if finalID == "" {
				finalID = candidate
			}
			if finalID == "" {
				continue
			}
			if _, dup := seen[finalID]; dup {
				continue
			}
			seen[candidate] = struct{}{}
			seen[finalID] = struct{}{}
			activated = append(activated, finalID)
		}
		if len(activated) == 0 {
			return nil, fmt.Errorf("no Google Cloud projects available for this account")
		}
		storage.ProjectID = strings.Join(activated, ",")
		return activated, nil
	case strings.EqualFold(trimmedRequest, "GOOGLE_ONE"):
		storage.Auto = false
		if err := performGeminiCLISetup(ctx, httpClient, storage, ""); err != nil {
			return nil, fmt.Errorf("google one auto-discovery: %w", err)
		}
		projectID := strings.TrimSpace(storage.ProjectID)
		if projectID == "" {
			return nil, fmt.Errorf("google one auto-discovery returned empty project id")
		}
		storage.ProjectID = projectID
		return []string{projectID}, nil
	default:
		if err := ensureGeminiProjectAndOnboard(ctx, httpClient, storage, trimmedRequest); err != nil {
			return nil, err
		}
		projectID := strings.TrimSpace(storage.ProjectID)
		if projectID == "" {
			return nil, fmt.Errorf("onboarding did not return a project id")
		}
		storage.ProjectID = projectID
		return []string{projectID}, nil
	}
}

func ensureGeminiProjectAndOnboard(ctx context.Context, httpClient *http.Client, storage *GeminiTokenStorage, requestedProject string) error {
	if storage == nil {
		return fmt.Errorf("gemini storage is nil")
	}

	trimmedRequest := strings.TrimSpace(requestedProject)
	if trimmedRequest == "" {
		projects, errProjects := fetchGCPProjects(ctx, httpClient)
		if errProjects != nil {
			return fmt.Errorf("fetch project list: %w", errProjects)
		}
		if len(projects) == 0 {
			return fmt.Errorf("no Google Cloud projects available for this account")
		}
		for _, project := range projects {
			if id := strings.TrimSpace(project.ProjectID); id != "" {
				trimmedRequest = id
				break
			}
		}
		if trimmedRequest == "" {
			return fmt.Errorf("resolved project id is empty")
		}
		storage.Auto = true
	} else {
		storage.Auto = false
	}

	if err := performGeminiCLISetup(ctx, httpClient, storage, trimmedRequest); err != nil {
		return err
	}
	if strings.TrimSpace(storage.ProjectID) == "" {
		storage.ProjectID = trimmedRequest
	}
	return nil
}

func performGeminiCLISetup(ctx context.Context, httpClient *http.Client, storage *GeminiTokenStorage, requestedProject string) error {
	metadata := map[string]string{
		"ideType":    "IDE_UNSPECIFIED",
		"platform":   "PLATFORM_UNSPECIFIED",
		"pluginType": "GEMINI",
	}

	trimmedRequest := strings.TrimSpace(requestedProject)
	explicitProject := trimmedRequest != ""

	loadReqBody := map[string]any{
		"metadata": metadata,
	}
	if explicitProject {
		loadReqBody["cloudaicompanionProject"] = trimmedRequest
	}

	var loadResp map[string]any
	if errLoad := callGeminiCLI(ctx, httpClient, "loadCodeAssist", loadReqBody, &loadResp); errLoad != nil {
		return fmt.Errorf("load code assist: %w", errLoad)
	}

	tierID := "legacy-tier"
	if tiers, okTiers := loadResp["allowedTiers"].([]any); okTiers {
		for _, rawTier := range tiers {
			tier, okTier := rawTier.(map[string]any)
			if !okTier {
				continue
			}
			if isDefault, okDefault := tier["isDefault"].(bool); okDefault && isDefault {
				if id, okID := tier["id"].(string); okID && strings.TrimSpace(id) != "" {
					tierID = strings.TrimSpace(id)
					break
				}
			}
		}
	}

	projectID := trimmedRequest
	if projectID == "" {
		if id, okProject := loadResp["cloudaicompanionProject"].(string); okProject {
			projectID = strings.TrimSpace(id)
		}
		if projectID == "" {
			if projectMap, okProject := loadResp["cloudaicompanionProject"].(map[string]any); okProject {
				if id, okID := projectMap["id"].(string); okID {
					projectID = strings.TrimSpace(id)
				}
			}
		}
	}
	if projectID == "" {
		autoOnboardReq := map[string]any{
			"tierId":   tierID,
			"metadata": metadata,
		}

		autoCtx, autoCancel := context.WithTimeout(ctx, 30*time.Second)
		defer autoCancel()
		for {
			var onboardResp map[string]any
			if errOnboard := callGeminiCLI(autoCtx, httpClient, "onboardUser", autoOnboardReq, &onboardResp); errOnboard != nil {
				return fmt.Errorf("auto-discovery onboardUser: %w", errOnboard)
			}

			if done, okDone := onboardResp["done"].(bool); okDone && done {
				if resp, okResp := onboardResp["response"].(map[string]any); okResp {
					switch v := resp["cloudaicompanionProject"].(type) {
					case string:
						projectID = strings.TrimSpace(v)
					case map[string]any:
						if id, okID := v["id"].(string); okID {
							projectID = strings.TrimSpace(id)
						}
					}
				}
				break
			}

			select {
			case <-autoCtx.Done():
				return fmt.Errorf("project selection required")
			case <-time.After(2 * time.Second):
			}
		}
	}
	if projectID == "" {
		return fmt.Errorf("project selection required")
	}

	onboardReqBody := map[string]any{
		"tierId":                  tierID,
		"metadata":                metadata,
		"cloudaicompanionProject": projectID,
	}

	// Keep a fallback value in case response omits the project id.
	storage.ProjectID = projectID

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		var onboardResp map[string]any
		if errOnboard := callGeminiCLI(ctx, httpClient, "onboardUser", onboardReqBody, &onboardResp); errOnboard != nil {
			return fmt.Errorf("onboard user: %w", errOnboard)
		}

		if done, okDone := onboardResp["done"].(bool); okDone && done {
			responseProjectID := ""
			if resp, okResp := onboardResp["response"].(map[string]any); okResp {
				switch projectValue := resp["cloudaicompanionProject"].(type) {
				case map[string]any:
					if id, okID := projectValue["id"].(string); okID {
						responseProjectID = strings.TrimSpace(id)
					}
				case string:
					responseProjectID = strings.TrimSpace(projectValue)
				}
			}

			finalProjectID := projectID
			if responseProjectID != "" {
				finalProjectID = responseProjectID
			}
			storage.ProjectID = strings.TrimSpace(finalProjectID)
			if storage.ProjectID == "" {
				return fmt.Errorf("onboard user completed without project id")
			}
			return nil
		}

		time.Sleep(5 * time.Second)
	}
}

func callGeminiCLI(ctx context.Context, httpClient *http.Client, endpoint string, body any, result any) error {
	url := fmt.Sprintf("%s/%s:%s", geminiCLIEndpoint, geminiCLIVersion, endpoint)
	if strings.HasPrefix(endpoint, "operations/") {
		url = fmt.Sprintf("%s/%s", geminiCLIEndpoint, endpoint)
	}

	var reader io.Reader
	if body != nil {
		rawBody, errMarshal := json.Marshal(body)
		if errMarshal != nil {
			return fmt.Errorf("marshal request body: %w", errMarshal)
		}
		reader = bytes.NewReader(rawBody)
	}

	req, errRequest := http.NewRequestWithContext(ctx, http.MethodPost, url, reader)
	if errRequest != nil {
		return fmt.Errorf("create request: %w", errRequest)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", geminiCLIUserAgent)
	req.Header.Set("X-Goog-Api-Client", geminiCLIApiClient)
	req.Header.Set("Client-Metadata", geminiCLIClientMetadata)

	resp, errDo := httpClient.Do(req)
	if errDo != nil {
		return fmt.Errorf("execute request: %w", errDo)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("api request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	if result == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if errDecode := json.NewDecoder(resp.Body).Decode(result); errDecode != nil {
		return fmt.Errorf("decode response body: %w", errDecode)
	}
	return nil
}

func fetchGCPProjects(ctx context.Context, httpClient *http.Client) ([]interfaces.GCPProjectProjects, error) {
	req, errRequest := http.NewRequestWithContext(ctx, http.MethodGet, "https://cloudresourcemanager.googleapis.com/v1/projects", nil)
	if errRequest != nil {
		return nil, fmt.Errorf("could not create project list request: %w", errRequest)
	}

	resp, errDo := httpClient.Do(req)
	if errDo != nil {
		return nil, fmt.Errorf("failed to execute project list request: %w", errDo)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("project list request failed with status %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	var projects interfaces.GCPProject
	if errDecode := json.NewDecoder(resp.Body).Decode(&projects); errDecode != nil {
		return nil, fmt.Errorf("failed to unmarshal project list: %w", errDecode)
	}
	return projects.Projects, nil
}

// CheckCloudAPIIsEnabled verifies required Cloud services are enabled for the project.
func CheckCloudAPIIsEnabled(ctx context.Context, httpClient *http.Client, projectID string) (bool, error) {
	serviceUsageURL := "https://serviceusage.googleapis.com"
	requiredServices := []string{
		"cloudaicompanion.googleapis.com",
	}
	for _, service := range requiredServices {
		checkURL := fmt.Sprintf("%s/v1/projects/%s/services/%s", serviceUsageURL, projectID, service)
		req, errRequest := http.NewRequestWithContext(ctx, http.MethodGet, checkURL, nil)
		if errRequest != nil {
			return false, fmt.Errorf("failed to create request: %w", errRequest)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", geminiCLIUserAgent)
		resp, errDo := httpClient.Do(req)
		if errDo != nil {
			return false, fmt.Errorf("failed to execute request: %w", errDo)
		}

		if resp.StatusCode == http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if gjson.GetBytes(bodyBytes, "state").String() == "ENABLED" {
				continue
			}
		} else {
			_ = resp.Body.Close()
		}

		enableURL := fmt.Sprintf("%s/v1/projects/%s/services/%s:enable", serviceUsageURL, projectID, service)
		req, errRequest = http.NewRequestWithContext(ctx, http.MethodPost, enableURL, strings.NewReader("{}"))
		if errRequest != nil {
			return false, fmt.Errorf("failed to create request: %w", errRequest)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", geminiCLIUserAgent)
		resp, errDo = httpClient.Do(req)
		if errDo != nil {
			return false, fmt.Errorf("failed to execute request: %w", errDo)
		}

		bodyBytes, _ := io.ReadAll(resp.Body)
		errMessage := string(bodyBytes)
		errMessageResult := gjson.GetBytes(bodyBytes, "error.message")
		if errMessageResult.Exists() {
			errMessage = errMessageResult.String()
		}
		_ = resp.Body.Close()

		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
			continue
		}
		if resp.StatusCode == http.StatusBadRequest && strings.Contains(strings.ToLower(errMessage), "already enabled") {
			continue
		}
		return false, fmt.Errorf("project activation required: %s", errMessage)
	}
	return true, nil
}
