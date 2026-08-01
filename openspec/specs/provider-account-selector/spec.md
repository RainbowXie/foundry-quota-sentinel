# provider-account-selector Specification

## Purpose

The Add Account dialog's provider selection step: each provider choice is a
uniform, centered, name-only option rendered from a single data-driven
provider registry. Choosing a provider retains the existing account-name
step and login dispatch.

## Requirements

### Requirement: Add Account provider choices show only centered provider names
The first step of the Add Account dialog SHALL render each provider choice with exactly one visible provider name centered horizontally within a uniform option. It MUST NOT render a provider-specific subtitle, feature summary, quota vocabulary, or usage description beside or below the name.

#### Scenario: Open the provider selection step
- **WHEN** the user opens Add Account
- **THEN** the available choices visibly read only `OpenCode Go`, `DeepSeek`, `Ollama`, and `Kimi Code`, with each name centered in an otherwise uniform provider option

#### Scenario: Obsolete provider descriptions are absent
- **WHEN** the provider choices are rendered
- **THEN** `套餐额度`, `用量 / 余额`, `Cloud 用量`, and `Rolling / Weekly / Monthly` do not appear in the provider selection step and no empty subtitle element reserves space for them

#### Scenario: Provider names have different lengths
- **WHEN** short and long provider names are displayed together
- **THEN** every name remains centered using the same option height, padding, typography, and interaction states rather than provider-specific alignment rules

### Requirement: Simplified choices preserve provider selection behavior
Each simplified provider option SHALL retain its existing provider identifier and SHALL continue to advance to the existing account-name/login step for that provider. Removing descriptions MUST NOT change provider dispatch, default account names, modal close behavior, or login initiation.

#### Scenario: Select each provider
- **WHEN** the user activates an OpenCode Go, DeepSeek, Ollama, or Kimi Code option
- **THEN** the modal records respectively `opencode`, `deepseek`, `ollama`, or `kimi` and shows the existing second-step account-name form for that provider

#### Scenario: Add another provider later
- **WHEN** a future provider is added to the selector data
- **THEN** it receives the same centered name-only presentation without requiring a new subtitle or provider-specific option layout
