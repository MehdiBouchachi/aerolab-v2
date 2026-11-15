package sync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ClaimError is a refusal the cloud explained (wrong/used code, site id taken, rate limited); its
// message is safe to show to the person connecting the Pi.
type ClaimError struct {
	Status  int
	Code    string
	Message string
}

func (e *ClaimError) Error() string { return e.Code + ": " + e.Message }

// ValidateCloudURL applies the same rule as AEROLAB_CLOUD_URL: https, plain http only for loopback or
// an explicit lab opt-in.
func ValidateCloudURL(raw string, allowInsecure bool) (string, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return "", errors.New("enter the cloud address only, e.g. https://cloud.example.org")
	}
	h := u.Hostname()
	loop := h == "localhost" || h == "127.0.0.1" || h == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && (loop || allowInsecure)) {
		return "", errors.New("the cloud address must start with https://")
	}
	return u.Scheme + "://" + u.Host, nil
}

// Claim exchanges a one-time code for this site's edge token. It never follows redirects (the code
// must not be replayed to another host).
func Claim(ctx context.Context, cloudURL, code, siteID, siteName, edgeVersion string) (token, orgName string, err error) {
	body, _ := json.Marshal(map[string]string{"code": code, "site_id": siteID, "site_name": siteName, "edge_version": edgeVersion})
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", cloudURL+"/api/v1/edge/claim", bytes.NewReader(body))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("cannot reach the cloud: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != 201 {
		var e struct {
			Error struct{ Code, Message string } `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error.Code != "" {
			return "", "", &ClaimError{resp.StatusCode, e.Error.Code, e.Error.Message}
		}
		return "", "", &ClaimError{resp.StatusCode, "CLOUD_ERROR", fmt.Sprintf("the cloud answered HTTP %d", resp.StatusCode)}
	}
	var ok struct {
		Token        string `json:"token"`
		SiteID       string `json:"site_id"`
		Organization struct {
			Name string `json:"name"`
		} `json:"organization"`
	}
	if err := json.Unmarshal(raw, &ok); err != nil || !strings.HasPrefix(ok.Token, "aeroedge_") || ok.SiteID != siteID {
		return "", "", errors.New("the cloud returned an unexpected answer")
	}
	return ok.Token, ok.Organization.Name, nil
}
