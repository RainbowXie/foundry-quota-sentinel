//go:build !nogui

package main

// locktestBinaryTags is empty when the parent test binary was built without
// the nogui tag, so the forked `_locktest` binary is built plainly (the
// environment already satisfied the GUI/webview build requirements for the
// test binary itself).
const locktestBinaryTags = ""
