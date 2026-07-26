package browserauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// PageURL returns the current page URL by evaluating location.href. The
// result is rejected if the URL is not HTTPS or on the caller's allow-list.
func (c *Client) PageURL(ctx context.Context, allowedHosts ...string) (string, error) {
	raw, err := c.Call(ctx, "Runtime.evaluate", map[string]any{
		"expression":    "location.href",
		"returnByValue": true,
	})
	if err != nil {
		return "", err
	}
	var result struct {
		Result struct {
			Value string `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return "", fmt.Errorf("解析 location.href 失败: %w", err)
	}
	if result.Result.Value == "" {
		return "", fmt.Errorf("页面 URL 为空")
	}
	if err := parseHTTPSURL(result.Result.Value, allowedHosts); err != nil {
		return "", err
	}
	return result.Result.Value, nil
}

// Navigate sends Page.navigate. The destination URL must be HTTPS and on
// the provider's allow-list.
func (c *Client) Navigate(ctx context.Context, pageURL string, allowedHosts ...string) error {
	if err := parseHTTPSURL(pageURL, allowedHosts); err != nil {
		return err
	}
	if _, err := c.Call(ctx, "Page.navigate", map[string]any{"url": pageURL}); err != nil {
		return err
	}
	return nil
}

// NavigateWithLoader sends Page.navigate and returns the navigation's
// loaderId (from the result). A coordinator uses this loaderId to
// associate only the requests/responses that belong to THIS navigation,
// rather than guessing from the first requestWillBeSent.
func (c *Client) NavigateWithLoader(ctx context.Context, pageURL string, allowedHosts ...string) (string, error) {
	if err := parseHTTPSURL(pageURL, allowedHosts); err != nil {
		return "", err
	}
	raw, err := c.Call(ctx, "Page.navigate", map[string]any{"url": pageURL})
	if err != nil {
		return "", err
	}
	var res struct {
		LoaderID string `json:"loaderId"`
		Error    string `json:"errorText"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", fmt.Errorf("解析 Page.navigate 响应失败: %w", err)
	}
	if res.Error != "" {
		return "", fmt.Errorf("导航失败: %s", res.Error)
	}
	return res.LoaderID, nil
}

// Evaluate runs the given JavaScript on the page and returns the raw
// DevTools result. Callers that need the typed value should parse it from
// JSON themselves; the shared package only handles transport.
func (c *Client) Evaluate(ctx context.Context, expression string) (json.RawMessage, error) {
	return c.Call(ctx, "Runtime.evaluate", map[string]any{
		"expression":    expression,
		"returnByValue": true,
		"awaitPromise":  true,
	})
}

// AddScriptOnNewDocument installs a document-start script via
// Page.addScriptToEvaluateOnNewDocument. The script is re-injected on every
// navigation in the page target.
func (c *Client) AddScriptOnNewDocument(ctx context.Context, script string) error {
	if script == "" {
		return fmt.Errorf("脚本为空")
	}
	if _, err := c.Call(ctx, "Page.addScriptToEvaluateOnNewDocument", map[string]any{
		"source": script,
	}); err != nil {
		return err
	}
	return nil
}

// EnableNetwork turns on Network domain events so the caller can subscribe
// to requestWillBeSent / requestWillBeSentExtraInfo through Events().
func (c *Client) EnableNetwork(ctx context.Context) error {
	if _, err := c.Call(ctx, "Network.enable", nil); err != nil {
		return err
	}
	return nil
}

// EnablePage turns on Page domain events (loadEventFired,
// domContentEventFired, frameStoppedLoading) so a coordinator can wait
// for observable document-load signals rather than polling.
func (c *Client) EnablePage(ctx context.Context) error {
	if _, err := c.Call(ctx, "Page.enable", nil); err != nil {
		return err
	}
	return nil
}

// IsLoadEventFired reports whether a CDP event is Page.loadEventFired.
func IsLoadEventFired(event Event) bool {
	return event.Method == "Page.loadEventFired"
}

// IsDomContentEventFired reports whether a CDP event is
// Page.domContentEventFired.
func IsDomContentEventFired(event Event) bool {
	return event.Method == "Page.domContentEventFired"
}

// FrameStoppedLoadingEvent is the decoded form of
// Page.frameStoppedLoading. It carries a frameId, which (unlike
// loadEventFired) identifies the frame that finished loading — so a
// coordinator can associate it with a specific navigation.
type FrameStoppedLoadingEvent struct {
	FrameID string
}

// DecodeFrameStoppedLoadingEvent returns the typed view of a
// Page.frameStoppedLoading event.
func DecodeFrameStoppedLoadingEvent(event Event) (FrameStoppedLoadingEvent, bool) {
	if event.Method != "Page.frameStoppedLoading" {
		return FrameStoppedLoadingEvent{}, false
	}
	if len(event.Params) == 0 {
		return FrameStoppedLoadingEvent{}, false
	}
	var payload struct {
		FrameID string `json:"frameId"`
	}
	if err := json.Unmarshal(event.Params, &payload); err != nil {
		return FrameStoppedLoadingEvent{}, false
	}
	return FrameStoppedLoadingEvent{FrameID: payload.FrameID}, true
}

// FrameNavigatedEvent is the decoded form of Page.frameNavigated. It
// carries a frameId, the URL the frame navigated to, and the loaderId
// that initiated the navigation, so a coordinator can associate it
// with a specific Page.navigate call and reject events from a previous
// navigation. The frame also has a name/type; "OutermostFrame" marks
// the main frame.
type FrameNavigatedEvent struct {
	FrameID  string
	LoaderID string
	URL      string
	Name     string
}

// IsMainFrame reports whether this frameNavigated event is for the
// main (outermost) frame — sub-frame navigations (iframes, etc.) must
// not be treated as the SPA's auth-decision signal.
func (e FrameNavigatedEvent) IsMainFrame() bool {
	return e.Name == "" || strings.Contains(e.Name, "OutermostFrame")
}

// DecodeFrameNavigatedEvent returns the typed view of a
// Page.frameNavigated event.
func DecodeFrameNavigatedEvent(event Event) (FrameNavigatedEvent, bool) {
	if event.Method != "Page.frameNavigated" {
		return FrameNavigatedEvent{}, false
	}
	if len(event.Params) == 0 {
		return FrameNavigatedEvent{}, false
	}
	var payload struct {
		Frame struct {
			ID       string `json:"id"`
			LoaderID string `json:"loaderId"`
			URL      string `json:"url"`
			Name     string `json:"name"`
		} `json:"frame"`
	}
	if err := json.Unmarshal(event.Params, &payload); err != nil {
		return FrameNavigatedEvent{}, false
	}
	return FrameNavigatedEvent{
		FrameID:  payload.Frame.ID,
		LoaderID: payload.Frame.LoaderID,
		URL:      payload.Frame.URL,
		Name:     payload.Frame.Name,
	}, true
}

// parseHTTPSURL accepts only HTTPS URLs whose host matches the caller
// allow-list. The host list is matched with cookie domain semantics so
// subdomains of the policy host also work.
func parseHTTPSURL(rawURL string, allowedHosts []string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("URL 解析失败: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("URL 必须为 https")
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host == "" {
		return fmt.Errorf("URL 缺少 host")
	}
	for _, allowed := range allowedHosts {
		if cookieDomainMatches(host, allowed) {
			return nil
		}
	}
	return fmt.Errorf("URL 域 %q 不在允许列表", host)
}
