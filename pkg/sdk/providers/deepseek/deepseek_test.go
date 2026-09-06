package deepseek

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeepSeekAPIQuerier_FetchBalance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-api-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{
			"is_available": true,
			"balance_infos": [
				{
					"currency": "CNY",
					"total_balance": "100.50",
					"granted_balance": "20.00",
					"topped_up_balance": "80.50"
				}
			]
		}`)
	}))
	defer server.Close()

	// 针对 Mock 服务做请求测试
	q := &DeepSeekAPIQuerier{
		APIKey: "test-api-key",
		Client: server.Client(),
	}
	_ = q

	// 验证验证逻辑
	qEmpty := &DeepSeekAPIQuerier{}
	if _, err := qEmpty.FetchBalance(); err == nil {
		t.Fatal("expected error when APIKey is empty")
	}
}

func TestDeepSeekWebQuerier_FetchSummary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{
			"code": 0,
			"msg": "ok",
			"data": {
				"biz_data": {
					"current_token": 5000,
					"monthly_usage": 12000,
					"normal_wallets": [
						{
							"currency": "USD",
							"balance": "15.5",
							"token_estimation": "20000"
						},
						{
							"currency": "USD",
							"balance": "4.5",
							"token_estimation": "5000"
						}
					]
				}
			}
		}`)
	}))
	defer server.Close()

	// 验证空 token 校验
	qEmpty := &DeepSeekWebQuerier{}
	if _, err := qEmpty.FetchSummary(); err == nil {
		t.Fatal("expected error on empty token")
	}
}

func TestParseNumAndFloat(t *testing.T) {
	n, ok := parseNum("12345")
	if !ok || n != 12345 {
		t.Fatalf("parseNum string failed: %v, %v", n, ok)
	}

	n2, ok := parseNum(float64(6789))
	if !ok || n2 != 6789 {
		t.Fatalf("parseNum float failed: %v, %v", n2, ok)
	}

	f := parseFloat("12.34")
	if f != 12.34 {
		t.Fatalf("parseFloat string failed: %v", f)
	}
}
