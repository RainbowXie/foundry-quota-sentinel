package web

import (
	"strings"
	"testing"
)

func TestSidebarDoesNotRenderProviderEmptyStateCards(t *testing.T) {
	html, err := webAssets.ReadFile("static/sidebar.html")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(html), "未配置账户") {
		t.Fatal("provider-specific empty state cards hide the account-add flow")
	}
}
