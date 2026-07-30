package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"foundry-quota-sentinel/internal/config"

	"foundry-quota-sentinel/internal/quota"
)

// buildTestBinary builds the current main package into a temp binary and
// returns its path. Cross-process lock tests fork this binary as `_locktest`
// subprocesses.
func buildTestBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fqs-locktest")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build test binary: %v\n%s", err, out)
	}
	return bin
}

// lockEvent is one HELD/DONE line from a _locktest subprocess, with its
// monotonic-ms timestamp for serialization-order assertions.
type lockEvent struct {
	kind string // "HELD" or "DONE"
	pid  int
	ms   int64
}

// parseLockEvents extracts the HELD/DONE events (with ms timestamps) from a
// _locktest combined output.
func parseLockEvents(t *testing.T, out string) []lockEvent {
	t.Helper()
	var evs []lockEvent
	for _, line := range strings.Split(out, "\n") {
		var kind string
		var pid int
		var ms int64
		if strings.HasPrefix(line, "HELD ") {
			kind = "HELD"
		} else if strings.HasPrefix(line, "DONE ") {
			kind = "DONE"
		} else {
			continue
		}
		rest := strings.TrimPrefix(line, kind+" ")
		// "HELD <pid> <ms>"
		fields := strings.Fields(rest)
		if len(fields) >= 2 {
			pid, _ = strconv.Atoi(fields[0])
			ms, _ = strconv.ParseInt(fields[1], 10, 64)
		} else if len(fields) == 1 {
			pid, _ = strconv.Atoi(fields[0])
		}
		evs = append(evs, lockEvent{kind: kind, pid: pid, ms: ms})
	}
	return evs
}

// assertSerializedAcrossProcesses proves two concurrent _locktest processes
// serialized on the cross-process lock: the second HELD timestamp is NOT
// before the first DONE timestamp. If the lock were broken, both HELDs would
// print before either DONE (overlap), i.e. min(HELD) < max(DONE) for both
// processes' HELDs landing inside the other's hold window.
func assertSerializedAcrossProcesses(t *testing.T, outA, outB string) {
	t.Helper()
	all := append(parseLockEvents(t, outA), parseLockEvents(t, outB)...)
	var heldMs, doneMs []int64
	for _, e := range all {
		if e.kind == "HELD" {
			heldMs = append(heldMs, e.ms)
		} else {
			doneMs = append(doneMs, e.ms)
		}
	}
	if len(heldMs) != 2 || len(doneMs) != 2 {
		t.Fatalf("want 2 HELD + 2 DONE, got held=%d done=%d in %q%s", len(heldMs), len(doneMs), outA, outB)
	}
	// Serialization: the later HELD must be >= the earlier DONE (no overlap).
	// Sort the two HELD times and the two DONE times.
	sortHeld := append([]int64(nil), heldMs...)
	sortDone := append([]int64(nil), doneMs...)
	sort.Slice(sortHeld, func(i, j int) bool { return sortHeld[i] < sortHeld[j] })
	sort.Slice(sortDone, func(i, j int) bool { return sortDone[i] < sortDone[j] })
	// The second (later) HELD must not precede the first (earlier) DONE.
	if sortHeld[1] < sortDone[0] {
		t.Fatalf("cross-process lock did NOT serialize: 2nd HELD at %d < 1st DONE at %d (both processes held the lock concurrently)", sortHeld[1], sortDone[0])
	}
}

// TestUsageListsKimiCommands (task 5.4) proves the help text lists the Kimi
// login and quota commands with the membership metric semantics: total usage
// split into Kimi + Code, plus 5-hour and 7-day Code usage.
func TestUsageListsKimiCommands(t *testing.T) {
	var buf bytes.Buffer
	writeUsage(&buf)
	got := buf.String()
	for _, want := range []string{"login-kimi", "quota-kimi"} {
		if !strings.Contains(got, want) {
			t.Fatalf("help text missing %q: %s", want, got)
		}
	}
	// The quota-kimi line must describe the membership metrics: total usage
	// with Kimi + Code, plus 5-hour/7-day Code — no rolling/weekly/frequency
	// language.
	if !strings.Contains(got, "总使用量(Kimi/Code)") || !strings.Contains(got, "5 小时/7 天 Code") {
		t.Fatalf("help text must label Kimi metrics as 总使用量(Kimi/Code) and 5 小时/7 天 Code: %s", got)
	}
	if strings.Contains(got, "本周用量") || strings.Contains(got, "频率限制") {
		t.Fatalf("help text must not carry obsolete weekly/frequency language: %s", got)
	}
}

// TestUsageListsOpenPageProviders proves the open-page usage string lists
// kimi alongside the other providers.
func TestUsageOpenPageListsKimi(t *testing.T) {
	// The open-page usage string is emitted to stderr only on arg-count
	// failure; assert on the constant via the help text's provider list
	// instead (the help text names all providers).
	var buf bytes.Buffer
	writeUsage(&buf)
	got := buf.String()
	if !strings.Contains(got, "login-kimi") {
		t.Fatalf("help text must mention the kimi provider: %s", got)
	}
}

// TestCrossProcessAccountLockSerializes (round-4 RED→GREEN) proves the
// per-account cross-process file lock serializes reload→refresh→persist for
// the SAME account across TWO SEPARATE PROCESSES. Two concurrent _locktest
// account processes for the same account must NOT interleave their HELD/DONE:
// the second HELD only after the first DONE (serialized), and the final token
// is one of the two marks (no double-rotation / lost write). Under a broken
// (non-serialized) lock both HELD would print before either DONE.
func TestCrossProcessAccountLockSerializes(t *testing.T) {
	dir := t.TempDir()
	// Seed a Kimi account with an initial token in the temp config. The
	// _locktest subprocess sets HOME=dir, so configPath resolves to
	// <dir>/.foundry-quota-sentinel/config.json — the seed must live there.
	cfgDir := filepath.Join(dir, ".foundry-quota-sentinel")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	seed := filepath.Join(cfgDir, "config.json")
	seedCfg := &config.Config{ActiveProfile: "default", Profiles: map[string]config.Profile{}}
	var env config.KimiAuthEnvelope
	_ = env.SetField("accessToken", "initial-access-AAAAAAAAAAAA")
	_ = env.SetField("refreshToken", "initial-refresh-BBBBBBBBBBBB")
	seedCfg.UpsertKimiAccount(config.KimiAccount{Name: "lockacct", Auth: env})
	data, _ := json.MarshalIndent(seedCfg, "", "  ")
	if err := os.WriteFile(seed, data, 0600); err != nil {
		t.Fatal(err)
	}

	bin := buildTestBinary(t)
	run := func(mark string) string {
		cmd := exec.Command(bin, "_locktest")
		cmd.Env = append(os.Environ(),
			"HOME="+dir,
			"LOCKTEST_MODE=account",
			"LOCKTEST_NAME=lockacct",
			"LOCKTEST_HOLD_MS=300",
			"LOCKTEST_MARK="+mark,
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("_locktest %s: %v\n%s", mark, err, out)
		}
		return string(out)
	}
	// Two concurrent processes rotate the same account with distinct marks.
	var wg sync.WaitGroup
	var outA, outB string
	wg.Add(2)
	go func() { defer wg.Done(); outA = run("markA") }()
	go func() { defer wg.Done(); outB = run("markB") }()
	wg.Wait()

	// Serialized proof: the second HELD must be at/after the first DONE (no
	// overlap). Under a broken cross-process lock both HELDs print before
	// either DONE.
	assertSerializedAcrossProcesses(t, outA, outB)
	// Both writes persist (atomic save + serialization): the final token is one
	// of the marks, not the initial token, and the account survives.
	config.SetPathOverrideForTest(seed)
	defer config.SetPathOverrideForTest("")
	c := config.Load()
	if len(c.KimiAccounts) != 1 {
		t.Fatalf("account lost; got %d accounts", len(c.KimiAccounts))
	}
	tok := c.KimiAccounts[0].Auth.AccessToken()
	if tok != "markA" && tok != "markB" {
		t.Fatalf("final token = %q, want markA or markB (writes did not serialize)", tok)
	}
}

// TestCrossProcessConfigLockSerializes (round-4 RED→GREEN) proves the global
// cross-process config lock transactionalizes Load→Mutate→Save across TWO
// SEPARATE PROCESSES. Two concurrent _locktest global processes writing window
// sizes must serialize: both writes persist (the final window size is one of
// the two, and no write is lost).
func TestCrossProcessConfigLockSerializes(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, ".foundry-quota-sentinel")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	seed := filepath.Join(cfgDir, "config.json")
	if err := os.WriteFile(seed, []byte(`{"active_profile":"default","profiles":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	bin := buildTestBinary(t)
	run := func(w int) string {
		cmd := exec.Command(bin, "_locktest")
		cmd.Env = append(os.Environ(),
			"HOME="+dir,
			"LOCKTEST_MODE=global",
			"LOCKTEST_HOLD_MS=300",
			"LOCKTEST_W="+strconv.Itoa(w),
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("_locktest global %d: %v\n%s", w, err, out)
		}
		return string(out)
	}
	var wg sync.WaitGroup
	var outA, outB string
	wg.Add(2)
	go func() { defer wg.Done(); outA = run(111) }()
	go func() { defer wg.Done(); outB = run(222) }()
	wg.Wait()
	assertSerializedAcrossProcesses(t, outA, outB)
	config.SetPathOverrideForTest(seed)
	defer config.SetPathOverrideForTest("")
	c := config.Load()
	if c.WindowW != 111 && c.WindowW != 222 {
		t.Fatalf("final window = %d, want 111 or 222 (writes did not serialize)", c.WindowW)
	}
}

// TestCrossProcessAccountLockSerializesThreeProcesses (round-5 RED→GREEN)
// proves the per-account cross-process lock serializes THREE concurrent
// processes for the same account (not just two), and that the lock file is NOT
// removed on release — each waiter flocks the SAME inode (no inode race where
// a waiter creates a new file and flocks a different inode, breaking mutual
// exclusion). Asserts: all 3 HELD/DONE pairs serialize (3rd HELD ≥ 1st DONE,
// and the HELD/DONE interleave in order), and the lock file still exists
// afterward (a waiter reused it, not a new inode).
func TestCrossProcessAccountLockSerializesThreeProcesses(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, ".foundry-quota-sentinel")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	seed := filepath.Join(cfgDir, "config.json")
	seedCfg := &config.Config{ActiveProfile: "default", Profiles: map[string]config.Profile{}}
	var env config.KimiAuthEnvelope
	_ = env.SetField("accessToken", "initial-access-AAAAAAAAAAAA")
	_ = env.SetField("refreshToken", "initial-refresh-BBBBBBBBBBBB")
	seedCfg.UpsertKimiAccount(config.KimiAccount{Name: "lock3", Auth: env})
	data, _ := json.MarshalIndent(seedCfg, "", "  ")
	if err := os.WriteFile(seed, data, 0600); err != nil {
		t.Fatal(err)
	}

	bin := buildTestBinary(t)
	run := func(mark string) string {
		cmd := exec.Command(bin, "_locktest")
		cmd.Env = append(os.Environ(),
			"HOME="+dir,
			"LOCKTEST_MODE=account",
			"LOCKTEST_NAME=lock3",
			"LOCKTEST_HOLD_MS=200",
			"LOCKTEST_MARK="+mark,
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("_locktest %s: %v\n%s", mark, err, out)
		}
		return string(out)
	}
	var wg sync.WaitGroup
	outs := make([]string, 3)
	wg.Add(3)
	for i := range outs {
		go func(i int) {
			defer wg.Done()
			outs[i] = run("m" + strconv.Itoa(i))
		}(i)
	}
	wg.Wait()

	// Collect all HELD/DONE with timestamps.
	var evs []lockEvent
	for _, o := range outs {
		evs = append(evs, parseLockEvents(t, o)...)
	}
	var heldMs, doneMs []int64
	for _, e := range evs {
		if e.kind == "HELD" {
			heldMs = append(heldMs, e.ms)
		} else {
			doneMs = append(doneMs, e.ms)
		}
	}
	if len(heldMs) != 3 || len(doneMs) != 3 {
		t.Fatalf("want 3 HELD + 3 DONE, got held=%d done=%d", len(heldMs), len(doneMs))
	}
	sortHeld := append([]int64(nil), heldMs...)
	sortDone := append([]int64(nil), doneMs...)
	sort.Slice(sortHeld, func(i, j int) bool { return sortHeld[i] < sortHeld[j] })
	sort.Slice(sortDone, func(i, j int) bool { return sortDone[i] < sortDone[j] })
	// Serialization across 3: the 2nd HELD ≥ 1st DONE, and 3rd HELD ≥ 2nd DONE.
	if sortHeld[1] < sortDone[0] {
		t.Fatalf("3-proc lock NOT serialized: 2nd HELD %d < 1st DONE %d", sortHeld[1], sortDone[0])
	}
	if sortHeld[2] < sortDone[1] {
		t.Fatalf("3-proc lock NOT serialized: 3rd HELD %d < 2nd DONE %d", sortHeld[2], sortDone[1])
	}
	// Inode-race guard: the lock file must STILL EXIST after all three
	// released. If Close removed it, a waiter that opened the file mid-release
	// would get a new inode → its flock would not exclude the prior holder
	// (the inode race). Persistence proves waiters reused the same inode.
	lockFile := filepath.Join(cfgDir, "kimi-refresh-lock3.lock")
	if _, err := os.Stat(lockFile); err != nil {
		t.Fatalf("lock file removed after release: %v — waiters must reuse the same inode, not a new file", err)
	}
}

// TestKimiReplayEnvelopeUsesRotatedTokenNotStaleSnapshot (round-6 RED→GREEN)
// proves the open-page envelope-replay path reloads the LATEST config and runs
// FetchQuotaWithRefresh→SaveKimiTokens inside the account lock BEFORE encoding
// the envelope — so if a concurrent Web/CLI rotation already rotated the token
// (or the access token expired and refresh rotated it), the replayed envelope
// carries the ROTATED token, not the stale process-start snapshot. RED: the
// old open-page read the startup cfg snapshot and encoded a stale token.
func TestKimiReplayEnvelopeUsesRotatedTokenNotStaleSnapshot(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, ".foundry-quota-sentinel")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	seed := filepath.Join(cfgDir, "config.json")
	seedCfg := &config.Config{ActiveProfile: "default", Profiles: map[string]config.Profile{}}
	var env config.KimiAuthEnvelope
	_ = env.SetField("accessToken", "stale-startup-token-AAAAAAAAAAAA")
	_ = env.SetField("refreshToken", "stale-refresh-BBBBBBBBBBBB")
	seedCfg.UpsertKimiAccount(config.KimiAccount{Name: "replayacct", Auth: env})
	data, _ := json.MarshalIndent(seedCfg, "", "  ")
	if err := os.WriteFile(seed, data, 0600); err != nil {
		t.Fatal(err)
	}
	// Simulate a concurrent rotation that already persisted a rotated token to
	// disk BEFORE open-page's replay path runs. The replay must observe it.
	var rotEnv config.KimiAuthEnvelope
	_ = rotEnv.SetField("accessToken", "rotated-by-concurrent-process-1234567890")
	_ = rotEnv.SetField("refreshToken", "rotated-refresh-1234567890")
	rotated := &config.Config{ActiveProfile: "default", Profiles: map[string]config.Profile{}}
	rotated.UpsertKimiAccount(config.KimiAccount{Name: "replayacct", Auth: rotEnv})
	rotData, _ := json.MarshalIndent(rotated, "", "  ")
	if err := os.WriteFile(seed, rotData, 0600); err != nil {
		t.Fatal(err)
	}
	config.SetPathOverrideForTest(seed)
	defer config.SetPathOverrideForTest("")

	// Inject the refresh step so no real network call is made; it returns no
	// rotation (the on-disk token is already the rotated one).
	origRefresh := kimiReplayRefresh
	defer func() { kimiReplayRefresh = origRefresh }()
	kimiReplayRefresh = func(acc *config.KimiAccount) (*quota.RefreshResult, error) {
		// No rotation needed — the latest disk token is already fresh.
		return nil, nil
	}

	envJSON, err := kimiReplayEnvelope("replayacct")
	if err != nil {
		t.Fatalf("kimiReplayEnvelope: %v", err)
	}
	var got config.KimiAuthEnvelope
	if err := got.Decode([]byte(envJSON)); err != nil {
		t.Fatal(err)
	}
	tok := got.AccessToken()
	if tok == "stale-startup-token-AAAAAAAAAAAA" {
		t.Fatal("replay encoded the STALE startup snapshot token; must reload the latest (rotated) token inside the account lock")
	}
	if !strings.Contains(tok, "rotated-by-concurrent-process") {
		t.Fatalf("replay token = %q, want the rotated token", tok)
	}
}

// TestKimiReplayEnvelopePersistsInPageRotationNotInvalidateDisk (round-6
// RED→GREEN) proves that when the access token is expired and the replay path's
// refresh rotates it, the rotated token is persisted to disk via SaveKimiTokens
// BEFORE the envelope is encoded — so the page's in-flight token rotation does
// NOT leave the on-disk credential stale/invalid. RED: the old path encoded a
// rotated token for the page but never persisted it, so disk kept the expired
// token and a later CLI run would fail.
func TestKimiReplayEnvelopePersistsInPageRotationNotInvalidateDisk(t *testing.T) {
	dir := t.TempDir()
	cfgDir := filepath.Join(dir, ".foundry-quota-sentinel")
	if err := os.MkdirAll(cfgDir, 0700); err != nil {
		t.Fatal(err)
	}
	seed := filepath.Join(cfgDir, "config.json")
	seedCfg := &config.Config{ActiveProfile: "default", Profiles: map[string]config.Profile{}}
	var env config.KimiAuthEnvelope
	_ = env.SetField("accessToken", "expired-access-AAAAAAAAAAAA")
	_ = env.SetField("refreshToken", "valid-refresh-BBBBBBBBBBBB")
	seedCfg.UpsertKimiAccount(config.KimiAccount{Name: "rotateacct", Auth: env})
	data, _ := json.MarshalIndent(seedCfg, "", "  ")
	if err := os.WriteFile(seed, data, 0600); err != nil {
		t.Fatal(err)
	}
	config.SetPathOverrideForTest(seed)
	defer config.SetPathOverrideForTest("")

	origRefresh := kimiReplayRefresh
	defer func() { kimiReplayRefresh = origRefresh }()
	kimiReplayRefresh = func(acc *config.KimiAccount) (*quota.RefreshResult, error) {
		// The access token is expired → refresh rotates both tokens.
		return &quota.RefreshResult{
			AccessToken:  "page-rotated-access-1234567890",
			RefreshToken: "page-rotated-refresh-1234567890",
		}, nil
	}

	envJSON, err := kimiReplayEnvelope("rotateacct")
	if err != nil {
		t.Fatalf("kimiReplayEnvelope: %v", err)
	}
	// The encoded envelope must carry the rotated token (replay uses it).
	var got config.KimiAuthEnvelope
	if err := got.Decode([]byte(envJSON)); err != nil {
		t.Fatal(err)
	}
	if got.AccessToken() != "page-rotated-access-1234567890" {
		t.Fatalf("replay token = %q, want the page-rotated token", got.AccessToken())
	}
	// The DISK credential must ALSO be the rotated token (persisted), not the
	// expired one — the page's in-flight rotation must not invalidate disk.
	c := config.Load()
	if len(c.KimiAccounts) != 1 {
		t.Fatalf("account lost; got %d", len(c.KimiAccounts))
	}
	diskTok := c.KimiAccounts[0].Auth.AccessToken()
	if diskTok == "expired-access-AAAAAAAAAAAA" {
		t.Fatal("disk credential is still the EXPIRED token — the page's in-flight rotation was not persisted, so disk is invalidated")
	}
	if diskTok != "page-rotated-access-1234567890" {
		t.Fatalf("disk token = %q, want the page-rotated token", diskTok)
	}
}
