package browserauth

import (
	"encoding/json"
	"strings"
)

// RequestHeadersEvent is the decoded form of Network.requestWillBeSent
// and Network.requestWillBeSentExtraInfo. The two events share a
// requestId so a coordinator can pair an ExtraInfo (which carries
// only headers) with the matching requestWillBeSent (which carries
// the URL). Both requestId and URL are decoded when present.
type RequestHeadersEvent struct {
	RequestID string
	URL       string
	Headers   map[string]string
}

// DecodeRequestHeadersEvent returns the typed view of a Network event
// if the event method is one we know how to decode. Header keys are
// lower-cased to match the conventional look-up path. requestId is
// always decoded; URL is decoded only when present.
func DecodeRequestHeadersEvent(event Event) (RequestHeadersEvent, bool) {
	if !strings.HasPrefix(event.Method, "Network.requestWillBeSent") {
		return RequestHeadersEvent{}, false
	}
	var payload struct {
		RequestID string            `json:"requestId"`
		URL       string            `json:"url"`
		Headers   map[string]string `json:"headers"`
		Request   struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"request"`
	}
	if len(event.Params) == 0 {
		return RequestHeadersEvent{}, false
	}
	if err := json.Unmarshal(event.Params, &payload); err != nil {
		return RequestHeadersEvent{}, false
	}
	headers := normaliseHeaders(payload.Headers)
	if len(headers) == 0 {
		headers = normaliseHeaders(payload.Request.Headers)
	}
	if payload.URL == "" {
		payload.URL = payload.Request.URL
	}
	return RequestHeadersEvent{RequestID: payload.RequestID, URL: payload.URL, Headers: headers}, true
}

// BearerToken extracts a Bearer credential from the given request headers.
// The token is rejected if it contains characters that would never appear
// in a real Bearer token; empty / wrong-scheme values return "".
func BearerToken(headers map[string]string) string {
	raw, ok := headers["authorization"]
	if !ok {
		return ""
	}
	scheme, token, ok := strings.Cut(strings.TrimSpace(raw), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	if strings.ContainsAny(token, " \t\r\n;") {
		return ""
	}
	return token
}

func normaliseHeaders(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[strings.ToLower(key)] = value
	}
	return out
}
