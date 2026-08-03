## ADDED Requirements

### Requirement: Sidebar clock updates independently every second
The sidebar SHALL render the visible current time immediately on startup and once every 1,000 milliseconds without initiating any provider quota request. Provider latency or failure MUST NOT delay, skip, or reorder clock updates.

#### Scenario: Provider request remains pending
- **WHEN** an automatic quota refresh remains unresolved for several seconds
- **THEN** the visible clock continues updating once per second while no extra request is created by the clock

#### Scenario: Sidebar starts
- **WHEN** the sidebar script initializes
- **THEN** it renders the clock immediately and schedules subsequent updates at one-second intervals

### Requirement: All providers use non-overlapping thirty-second automatic polling
OpenCode Go, DeepSeek, Ollama, and Kimi SHALL each perform one immediate initial quota refresh and SHALL schedule the next automatic refresh 30 seconds after that provider's preceding attempt settles. The scheduler MUST NOT start a second refresh for a provider while its previous refresh remains in flight, and one provider's state MUST NOT delay another provider.

#### Scenario: Normal automatic polling
- **WHEN** a provider's initial refresh settles at time T
- **THEN** no automatic request for that provider starts before T plus 30 seconds and one request starts when that delay elapses

#### Scenario: Refresh exceeds the polling delay
- **WHEN** a provider refresh remains pending for more than 30 seconds
- **THEN** no overlapping request for that provider is started and the next 30-second delay begins only after the pending request settles

#### Scenario: One provider is slow
- **WHEN** Kimi has a pending refresh while the OpenCode, DeepSeek, and Ollama schedules become due
- **THEN** the other three providers refresh independently without starting another Kimi request

#### Scenario: Refresh attempt fails
- **WHEN** a provider refresh rejects or renders its existing error state
- **THEN** the scheduler clears its in-flight state and schedules the next automatic attempt 30 seconds later

### Requirement: Explicit refreshes share the provider single-flight boundary
Login completion, account deletion, manual actions, and other explicit provider refresh triggers SHALL run immediately when that provider is idle, SHALL join the existing in-flight refresh when it is busy, and SHALL reset the next automatic deadline to 30 seconds after the joined or newly started attempt settles. Every public provider refresh handler used by the provider registry MUST pass through this boundary.

#### Scenario: Explicit refresh occurs while idle
- **WHEN** an account action requests an immediate OpenCode refresh while OpenCode has no request in flight
- **THEN** one OpenCode request starts immediately and its next automatic refresh is due 30 seconds after it settles

#### Scenario: Explicit refresh occurs while busy
- **WHEN** an account action requests a provider refresh while that provider's automatic refresh is still pending
- **THEN** the action joins the pending refresh and no duplicate provider request is sent

#### Scenario: Different provider refresh is requested
- **WHEN** OpenCode is busy and an Ollama account action requests an explicit refresh
- **THEN** Ollama refreshes immediately without changing or duplicating the OpenCode request
