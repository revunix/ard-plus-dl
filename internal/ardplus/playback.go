// Package ardplus is the ARD Plus API client: the GraphQL metadata API, the
// playback authorization API and the Tatort category pages.
package ardplus

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
)

type playbackRequest struct {
	ContentID   string      `json:"contentId"`
	ContentType string      `json:"contentType"`
	Download    bool        `json:"download"`
	AppInfo     appInfo     `json:"appInfo"`
	DeviceInfo  deviceInfo  `json:"deviceInfo"`
	Extra       interface{} `json:"-"`
}

type appInfo struct {
	Platform         string `json:"platform"`
	AppVersion       string `json:"appVersion"`
	Build            string `json:"build"`
	BundleIdentifier string `json:"bundleIdentifier"`
}

type deviceInfo struct {
	IsTouchDevice  bool   `json:"isTouchDevice"`
	IsTablet       bool   `json:"isTablet"`
	IsFireOS       bool   `json:"isFireOS"`
	AppPlatform    string `json:"appPlatform"`
	IsIOS          bool   `json:"isIOS"`
	IsCastReceiver bool   `json:"isCastReceiver"`
	IsSafari       bool   `json:"isSafari"`
	IsFirefox      bool   `json:"isFirefox"`
}

// PlaybackAuth requests the playback URL parameters for one content item.
func (c *Client) PlaybackAuth(ctx context.Context, contentID, contentType string) (string, error) {
	if contentID == "" || contentType == "" {
		return "", nil
	}
	payload := playbackRequest{
		ContentID:   contentID,
		ContentType: contentType,
		Download:    false,
		AppInfo: appInfo{
			Platform:         "web",
			AppVersion:       "1.0.0",
			Build:            "web",
			BundleIdentifier: "web",
		},
		DeviceInfo: deviceInfo{AppPlatform: "web"},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL()+"/token/session", strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	c.applyHeaders(req)
	// The Accept-Encoding header is deliberately not set: Go only decompresses
	// a gzip response transparently when it added the header itself. The
	// response is therefore unpacked by hand below.

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, err := readResponseBody(resp)
	if err != nil {
		return "", err
	}
	var parsed struct {
		AuthorizationParams *string `json:"authorizationParams"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", err
	}
	if c.Debugf != nil {
		var shape map[string]json.RawMessage
		_ = json.Unmarshal(data, &shape)
		keys := make([]string, 0, len(shape))
		for key := range shape {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		c.Debugf("token/session: HTTP %d, response keys: %s", resp.StatusCode, strings.Join(keys, ","))
	}
	if parsed.AuthorizationParams == nil {
		return "", nil
	}
	return *parsed.AuthorizationParams, nil
}

// DeletePlayback ends the tracked playback session for one content item.
func (c *Client) DeletePlayback(ctx context.Context, contentID, contentType string) error {
	if contentID == "" || contentType == "" {
		return nil
	}
	payload, err := json.Marshal(map[string]string{"contentId": contentID, "contentType": contentType})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL()+"/token/session/playback/delete", strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	c.applyHeaders(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}
