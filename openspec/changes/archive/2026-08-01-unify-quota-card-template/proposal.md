## Why

Allowance-style provider cards currently duplicate their HTML composition and can drift in labels, spacing, percentage rendering, reset-time presentation, and interaction states whenever a provider is added. Kimi exposes the problem visibly by showing absolute reset dates (`07-31 16:58`, `08-04 23:58`, `2026-08-28`) where the established card experience expects compact remaining durations such as `5h`, `2d`, and `23d`.

## What Changes

- Introduce a reusable, data-driven quota-card foundation for allowance-style providers: a shared outer shell, standard meter-row primitive, design tokens, states, and responsive behavior, plus explicit provider extension slots.
- Migrate OpenCode Go, Ollama, and Kimi cards to the shared foundation while preserving provider-specific data semantics, account actions, errors, optional rows, labels, and custom detail blocks.
- Render standard meter rows through one shared structure for label, progress track, percentage text, and compact reset duration, without requiring every provider card to have identical content.
- Change Kimi sidebar reset text from absolute date/time strings to relative remaining durations derived from `reset_in_sec`, using the same compact `s`/`m`/`h`/`d` vocabulary as other quota cards.
- Preserve Kimi's real percentages and existing decimal precision exactly: this change does not convert `28.21%` to another value or round it to an integer.
- Preserve the Kimi Monthly total fill and its provider-specific text labels `Kimi` and `Code` below Monthly through a typed row-detail extension slot.
- Allow future providers to adapt standard quota rows and add genuinely provider-specific header/body/row-detail/footer content through defined slots, while preventing them from copying the common card shell, meter row, and layout CSS.
- Simplify the first step of the Add Account dialog so every provider option uses one centered provider name only; remove per-provider subtitles such as `套餐额度`, `用量 / 余额`, `Cloud 用量`, and `Rolling / Weekly / Monthly`.

## Capabilities

### New Capabilities

- `quota-card-template`: Shared declarative sidebar foundation for allowance-style provider cards, combining reusable shell/row primitives with explicit provider-specific extension slots, compact relative reset durations, and consistent loading/error/action presentation.
- `provider-account-selector`: Uniform Add Account provider choices that display only centered provider names while preserving the provider type selected for the existing login flow.

### Modified Capabilities

- `kimi-card-presentation`: Replace Kimi's absolute reset-date presentation with shared compact relative durations while preserving its decimal percentages, exact Rolling/Weekly/Monthly mapping, truthful Monthly fill, and Kimi/Code breakdown.

## Impact

- Shared quota-card shell/row rendering functions and CSS design tokens in `internal/web/static/sidebar.html`; provider adapters and typed extension content for OpenCode Go, Ollama, and Kimi replace only duplicated common markup.
- Sidebar rendering/DOM tests for shared-foundation parity, intentional provider differences, Kimi's `Kimi`/`Code` detail block, relative-duration boundaries, optional rows, errors, refresh, and actions.
- Add Account provider-option markup/styles and tests for centered names, absent subtitles, exact provider mapping, and unchanged step-two/login behavior.
- Kimi continues using existing `reset_in_sec`, percentage, total, and breakdown DTO fields; parser, authentication, refresh, storage, API contracts, CLI absolute-time output, and quota source values remain unchanged.
- DeepSeek's chart/balance-specific content is not forced into the allowance-row template, but shared outer card tokens may be reused where compatible.
