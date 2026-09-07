package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/nexora/nexora/shared/auth"
)

// IncidentReporter files an incident in the incident-service (the ops console
// view). Ledger-service keeps the dependency optional: if incident-service is
// unreachable the monitor still freezes the account and records the event —
// only the ops notification is lost.
type IncidentReporter interface {
	// FileIntegrityIncident raises a SEV1 integrity incident for the frozen
	// account and returns the created incident id.
	FileIntegrityIncident(ctx context.Context, accountID, entryID string, message string) (string, error)
}

// IncidentClient is the HTTP implementation of IncidentReporter.
type IncidentClient struct {
	baseURL string
	http    *http.Client
}

// NewIncidentClient builds the reporter from INCIDENT_SERVICE_URL.
func NewIncidentClient() *IncidentClient {
	baseURL := os.Getenv("INCIDENT_SERVICE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8097"
	}
	return &IncidentClient{baseURL: baseURL, http: &http.Client{Timeout: 5 * time.Second}}
}

// FileIntegrityIncident creates a SEV1 incident via the internal API.
func (c *IncidentClient) FileIntegrityIncident(ctx context.Context, accountID, entryID, message string) (string, error) {
	body, err := json.Marshal(map[string]interface{}{
		"title":            "Ledger integrity violation detected",
		"description":      message,
		"severity":         "SEV1",
		"affected_services": []string{"ledger-service"},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/incidents", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	auth.AddInternalToken(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("incident service unreachable: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("incident service returned %d: %s", resp.StatusCode, string(data))
	}
	var out struct {
		IncidentID string `json:"incident_id"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", err
	}
	if out.IncidentID == "" {
		return "", fmt.Errorf("incident response missing incident_id: %s", string(data))
	}
	return out.IncidentID, nil
}
