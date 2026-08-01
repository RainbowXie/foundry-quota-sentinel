# quota-card-template Specification

## Purpose

The shared allowance-card foundation for the sidebar: a reusable outer card
shell, standard meter-row primitive, progress track, percentage/reset
placement, design tokens, spacing, responsive behavior, state regions, and
typed provider extension slots. OpenCode Go, Ollama, and Kimi adapt their
DTOs into this view model; future allowance providers add an adapter plus
only the extension content they need.

## Requirements

### Requirement: Allowance providers use a shared quota-card foundation
The sidebar SHALL render allowance-style provider cards from a shared foundation consisting of an outer card shell, standard metric-row primitive, progress track, percentage/reset placement, design tokens, spacing, responsive behavior, state regions, and declared extension slots. OpenCode Go, Ollama, and Kimi MUST reuse these common primitives rather than duplicating them, but provider cards MUST be allowed to retain genuinely distinct labels, row counts, detail blocks, and actions through the extension contract.

#### Scenario: Render cards from three allowance providers
- **WHEN** OpenCode Go, Ollama, and Kimi accounts are displayed together
- **THEN** their standard portions use the same outer structure, row alignment, progress geometry, percentage column, reset column, spacing, and responsive grid behavior while their declared provider-specific content remains visible

#### Scenario: A provider has fewer quota rows
- **WHEN** an allowance provider supplies only Rolling and Weekly rows
- **THEN** the shared template omits Monthly without leaving an empty placeholder or changing the styling of the remaining rows

#### Scenario: A provider has secondary row details
- **WHEN** a provider supplies secondary details for one metric, such as Kimi and Code values below Monthly
- **THEN** the shared foundation renders those provider-specific labels and values through its row-detail slot immediately after that metric without forcing other providers to render the same block

#### Scenario: Providers require different card content
- **WHEN** two providers use the same standard meter rows but one also needs a header badge, row detail, or footer action
- **THEN** both reuse the shared shell and rows while only the provider that declares the extension renders the additional content

### Requirement: Provider adapters supply data rather than card markup
Each allowance-style provider SHALL adapt its API DTO into a shared quota-card view model containing provider identity, account name, success/error state, ordered standard metric rows, numeric progress values, percentage precision, remaining reset seconds, and typed optional extensions such as header badges, row details, body blocks, and footer/account actions. A new allowance provider MAY add namespaced rendering or styles for genuinely unique extension content, but MUST NOT add another complete card renderer or duplicate shared shell/metric-row CSS.

#### Scenario: Add a future allowance provider
- **WHEN** a new provider exposes percentage-based quota windows compatible with the shared row model
- **THEN** implementation requires a provider-to-view-model adapter and may declare provider-specific extensions, but requires no new outer-card, metric-row, progress-bar, percentage-column, or reset-column markup

#### Scenario: Provider percentages use different precision
- **WHEN** one provider supplies integer percentages and Kimi supplies decimal percentages
- **THEN** the shared template applies the view model's declared precision while retaining the same row structure and without changing either provider's underlying percentage value

#### Scenario: Kimi declares its Monthly detail extension
- **WHEN** the Kimi adapter supplies separate Kimi and Code percentages for Monthly
- **THEN** the view model carries both labeled values in Monthly's typed detail slot and the rendered Kimi card preserves them even though other providers do not have those labels

#### Scenario: Untrusted provider text is rendered
- **WHEN** a provider or account supplies text containing HTML-significant characters
- **THEN** the shared template escapes that text and does not interpret it as markup

### Requirement: Reset times use one compact relative-duration formatter
Every shared quota-card metric row SHALL display the time remaining until reset from its `reset_in_sec` value using one formatter and the compact units `s`, `m`, `h`, and `d`. The formatter SHALL use non-negative whole units, SHALL select the largest applicable single unit, and SHALL floor partial units consistently with the existing Go `FormatDurationCompact` behavior.

#### Scenario: Render representative relative resets
- **WHEN** rows have `reset_in_sec` values of 18,000 seconds, 172,800 seconds, and 1,987,200 seconds
- **THEN** their reset text is respectively `5h`, `2d`, and `23d`

#### Scenario: Render unit boundaries
- **WHEN** remaining time crosses 60 seconds, 3,600 seconds, or 86,400 seconds
- **THEN** the formatter switches respectively from seconds to minutes, minutes to hours, or hours to days using floored whole values

#### Scenario: Remaining time is zero or negative
- **WHEN** a row's effective remaining time is zero or below zero
- **THEN** the template renders `0s` and never displays a negative duration

### Requirement: Shared rendering preserves provider-specific behavior
The shared foundation and its extension slots SHALL preserve each migrated provider's metric order and meaning, progress value, percentage precision, optional details and blocks, loading/error state, re-login action where present, refresh replacement, deletion, account identity, and responsive sizing. It MUST NOT merge provider DTOs, erase provider-specific labels, change quota calculations, or fabricate missing rows.

#### Scenario: One provider card fails
- **WHEN** one allowance provider account returns an error while other accounts succeed
- **THEN** the shared template renders the error only on that account card and leaves all successful cards and their values unchanged

#### Scenario: Refreshed data replaces a card
- **WHEN** a provider adapter receives new quota data for an existing account
- **THEN** the shared template updates that card's rows, progress fills, percentages, reset durations, and details without changing its provider or account identity

#### Scenario: A provider requires a distinct visualization
- **WHEN** a provider uses a chart, balance summary, or other data that cannot be represented as allowance rows
- **THEN** it may use a specialized content renderer, while shared outer-card design tokens are reused where compatible and the allowance template is not distorted with provider-specific branches
