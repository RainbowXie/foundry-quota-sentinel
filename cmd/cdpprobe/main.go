// cdpprobe 是临时诊断工具：复用 browserauth 启动浏览器并连接 CDP，
// 然后对四类命令分别计时，用于定位"首个浏览器级 Storage.setCookies
// 阻塞 9~19s"的根因层面：
//
//  1. Browser.getVersion  —— 浏览器级、非存储域
//  2. Runtime.evaluate    —— 页面级、非存储域
//  3. Storage.setCookies  —— 浏览器级、存储域（被怀疑的命令）
//  4. Network.setCookie   —— 页面级、存储（另一条代码路径，候选替代）
//  5. Storage.setCookies  —— 再来一次（确认"暖"后是毫秒级）
//
// 用法：go run ./cmd/cdpprobe [启动URL]（默认 about:blank；传 https URL 可验证启动预热）
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"foundry-quota-sentinel/internal/browserauth"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	ctx := context.Background()

	startURL := "about:blank"
	if len(os.Args) > 1 {
		startURL = os.Args[1]
	}
	log.Printf("StartURL=%s", startURL)
	browser, err := browserauth.Launch(ctx, browserauth.LaunchOptions{StartURL: startURL})
	if err != nil {
		log.Fatalf("launch: %v", err)
	}
	defer browser.Close()

	// 与生产包装一致：100ms tick 重试直到 DevTools 就绪。
	var conn *browserauth.Connection
	connectStart := time.Now()
	for {
		conn, err = browserauth.Connect(ctx, browser.DebugAddress())
		if err == nil {
			break
		}
		if browser.Exited() {
			log.Fatalf("浏览器已退出: %v", browser.Wait())
		}
		time.Sleep(100 * time.Millisecond)
	}
	log.Printf("CONNECT 耗时 %s", time.Since(connectStart).Round(time.Millisecond))

	// 每个探针独立 60s 超时，观察真实耗时而不是被流程预算截断。
	probe := func(name string, fn func(callCtx context.Context) error) {
		callCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		start := time.Now()
		err := fn(callCtx)
		log.Printf("PROBE %-28s 耗时 %-12s err=%v", name, time.Since(start).Round(time.Millisecond), err)
	}

	probe("Browser.getVersion(browser)", func(c context.Context) error {
		_, err := conn.Browser().BrowserUserAgent(c)
		return err
	})
	probe("Runtime.evaluate(page)", func(c context.Context) error {
		_, err := conn.Page().Evaluate(c, "1+1")
		return err
	})
	// 顺序对调：Network.setCookie 先跑（冷态），验证它是否也要付初始化税。
	probe("Network.setCookie#cold(page)", func(c context.Context) error {
		_, err := conn.Page().Call(c, "Network.setCookie", map[string]any{
			"name": "probe2", "value": "x", "url": "https://example.com/",
		})
		return err
	})
	probe("Storage.setCookies(browser)", func(c context.Context) error {
		_, err := conn.Browser().Call(c, "Storage.setCookies", map[string]any{
			"cookies": []map[string]any{{
				"name": "probe1", "value": "x", "domain": ".example.com",
				"path": "/", "secure": true,
			}},
		})
		return err
	})
	probe("Storage.setCookies#2(browser)", func(c context.Context) error {
		_, err := conn.Browser().Call(c, "Storage.setCookies", map[string]any{
			"cookies": []map[string]any{{
				"name": "probe3", "value": "x", "domain": ".example.com",
				"path": "/", "secure": true,
			}},
		})
		return err
	})

	fmt.Println("done")
}
