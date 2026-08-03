//go:build nogui

package main

// locktestBinaryTags propagates the parent test binary's build tags to the
// `_locktest` subprocess build. Running `go test -tags nogui .` builds the
// test binary with the nogui tag; the forked lock-test binary must use the
// same tag so it never pulls in the CGO/webview dependency (which is absent
// on CI/dev machines without webkit2gtk).
const locktestBinaryTags = "-tags nogui"
