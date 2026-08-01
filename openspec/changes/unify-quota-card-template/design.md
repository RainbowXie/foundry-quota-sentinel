## Context

The embedded sidebar currently has a common visual vocabulary but several independent renderer functions: `acard` composes OpenCode Go, `olcard` composes Ollama, and `kcard`/`krow`/`ktotal` compose Kimi. They repeat header/error/row markup and select their own reset display fields. This allowed Kimi to retain absolute page-oriented reset strings while other allowance cards use compact relative durations, and it makes every new provider another opportunity for visual drift.

The existing DTOs already expose the required normalized inputs. Standard `QuotaUsage` contains `usage_percent`, `reset_in_sec`, and `reset_display`; Kimi's decimal usage types also contain `usage_percent`/`total_percent`, `reset_in_sec`, and absolute `reset_display`. Therefore the sidebar can standardize presentation without changing parsers or server APIs. Kimi percentages must remain decimal; only its reset text changes.

The Add Account dialog has the same duplication problem at a smaller scale: four manually written `.prov` blocks combine the provider name with four inconsistent subtitles, and `justify-content: space-between` makes each choice visually different. The user wants the selection step to communicate only provider identity.

## Goals / Non-Goals

**Goals:**

- Define one reusable view-model foundation and renderer primitives for the common parts of percentage-based allowance cards.
- Migrate OpenCode Go, Ollama, and Kimi to shared outer-card and metric-row markup while retaining explicit provider-specific extensions.
- Format every shared row's reset from `reset_in_sec` as a compact relative duration.
- Preserve provider-specific metric mappings, numeric values, precision, optional rows, details, and actions through declarative adapter data.
- Make a future compatible provider require an adapter plus only the extension content it genuinely needs, rather than another full renderer and CSS family.
- Render Add Account provider choices from one name/type definition with centered name-only tiles and no provider descriptions.

**Non-Goals:**

- Rounding or changing Kimi percentages; `28.21%` remains `28.21%`.
- Changing Kimi's API/parser `ResetAt` or absolute `ResetDisplay`, which may remain useful to CLI and diagnostics.
- Forcing DeepSeek's balance and per-model chart data into a percentage-row template.
- Rewriting sidebar networking, authentication, refresh, deletion, or account-page routing.
- Removing provider-specific labels from quota cards; the name-only rule applies only to the Add Account provider selection step.
- Introducing a client framework or external template dependency.

## Decisions

### Normalize common data while exposing typed provider extension slots

Introduce a renderer conceptually equivalent to:

```text
QuotaCardView {
  provider, providerLabel, accountName
  success, error
  rows: QuotaRowView[]
  errorAction?: CardAction
  headerExtension?: HeaderExtension
  bodyExtensions?: CardExtension[]
  footerExtension?: CardExtension
}

QuotaRowView {
  label
  percent
  percentPrecision
  resetInSec
  tone
  details?: QuotaDetail[]
}
```

`renderQuotaCard(view)` owns the outer shell, standard header, common state regions, ordered rows, and placement of declared extensions. `renderQuotaRow(row)` owns only the standard label, track, fill, percentage, reset, and typed detail placement. Provider functions become thin adapters that select and order DTO fields and declare extensions:

- OpenCode Go: Rolling, Weekly, optional Monthly; integer precision.
- Ollama: Session/Rolling-equivalent and Weekly; integer precision.
- Kimi: five-hour → Rolling, seven-day → Weekly, total → Monthly; up to two decimal places; the labeled `Kimi`/`Code` block attached to Monthly as Kimi-specific row-detail data.

Adapters do not concatenate the shared card or meter-row HTML. They provide normalized common data plus typed provider-specific extension data. Shared renderers place extensions but do not contain branches such as `if provider === "kimi"`; the Kimi adapter is responsible for declaring its labels and values. Provider-specific actions are declarative action records handled by shared event delegation.

This is deliberately not a universal identical-card template. The invariant is reuse of the shell, meter primitive, layout, states, and tokens; the extension contract is the supported place for real provider differences. A provider may add a namespaced extension renderer/style when the typed built-in slots are insufficient, provided it does not duplicate the common primitives.

Alternatives considered:

- Keep fully separate `acard`/`olcard`/`kcard` and extract only CSS. Rejected because common markup, reset selection, accessibility, and state behavior would still drift.
- Force every provider into identical rows and details. Rejected because Kimi's `Kimi`/`Code` Monthly detail and future provider-specific content are legitimate domain differences.
- Convert every server DTO into one Go type. Rejected because it couples unrelated provider contracts and would erase Kimi decimal/group semantics.
- Adopt a frontend framework. Rejected because the embedded page is small and a framework adds dependency/build complexity unrelated to the goal.

### Centralize compact reset formatting in the sidebar

Add one pure `formatDurationCompact(seconds)` function matching the existing Go formatter:

- `< 60` → floored seconds (`59s`)
- `< 3600` → floored minutes (`59m`)
- `< 86400` → floored hours (`23h`)
- otherwise → floored days (`23d`)
- negative/non-finite input → `0s` or a safe unavailable state according to validation; never a negative string

Every shared row renders from `resetInSec`; adapters must not pass provider-specific `reset_display` into the template. Representative Kimi values therefore become `5h`, `2d`, and `23d`, while their percentages remain untouched. Centralizing in the UI keeps CLI/API absolute reset fields backward compatible and gives every card identical boundary behavior.

Using each DTO's `reset_display` was considered and rejected because that field intentionally carries provider-specific formatting and is the current source of inconsistency.

### Make precision declarative but presentation structural

The row template owns percentage formatting and receives a precision policy rather than preformatted HTML. Integer providers use zero decimal places; Kimi uses up to two decimal places with trailing-zero trimming. The same numeric value drives both fill width and percentage text, clamped only for CSS safety where necessary; the underlying value is not mutated.

Allowing adapters to provide arbitrary percentage strings was considered and rejected because it would recreate formatting drift and make bar/text disagreement easier.

### Support explicit extension slots, not a rigid template or provider branches

The shared foundation supports ordered optional rows, header badges, row-attached detail items, provider body blocks, footer content, and declarative error/account actions. These cover existing allowance-card variation without conditions such as `if provider === "kimi"` inside the standard row renderer. Common CSS classes and design tokens own layout and states; namespaced extension classes own only the unique content.

Kimi's `Kimi 0.03% / Code 11.89%` line is the reference extension case: it remains visually and semantically Kimi-specific, but it is placed by the standard Monthly row's detail slot instead of requiring a second Kimi-only meter renderer.

DeepSeek remains a specialized content renderer because its charts and balance summary are structurally different. It may reuse `.acard`/header tokens, but it is not evidence that every future provider needs bespoke allowance-card markup.

### Test the template contract using distinct provider fixtures

Execute the real embedded renderer with synthetic OpenCode, Ollama, and Kimi DTOs. Tests must assert shared structural classes/geometry for common portions, correct adapters and optional rows, preserved intentional provider extensions, Kimi decimal preservation, compact reset boundary formatting, Monthly details, error isolation, escaped text, refresh replacement, and absence of duplicated common row markup/CSS.

Visual acceptance uses at least wide and narrow supported widths with multiple providers to catch alignment and wrapping regressions that string assertions cannot detect.

### Make the Add Account provider selector name-only and data-driven

Represent the provider choices as a small ordered definition containing only the stable provider type and display name:

```text
opencode → OpenCode Go
deepseek → DeepSeek
ollama   → Ollama
kimi     → Kimi Code
```

Render each definition through the same option primitive. The option uses centered alignment (`justify-content: center` and centered text), common height/padding/typography, and the existing hover/active states. Remove `.prov-d`, the subtitle nodes, and the four description strings entirely rather than hiding them with CSS.

The existing `data-type` value remains the dispatch source for step two and login. The provider name shown in the second-step account label and the existing default account-name selection remain unchanged. This keeps the change presentational and makes a future provider addition a data entry instead of another custom two-column tile.

Leaving the subtitles in the DOM but visually hiding them was considered and rejected because it leaves stale semantics and unnecessary provider-specific content. Centering only the name while retaining `space-between` was rejected because the empty second column would continue to distort alignment.

## Risks / Trade-offs

- **[Refactor changes stable cards]** → Capture existing OpenCode/Ollama/Kimi output semantics in RED characterization tests before replacing renderers, then compare DOM structure and states after migration.
- **[Foundation becomes a provider-condition switch]** → Keep provider decisions and extension declarations in adapters; reject provider-name branching inside shared shell/row renderers.
- **[Foundation erases meaningful provider differences]** → Treat typed header/body/row-detail/footer slots as part of the architecture and lock Kimi's labeled Monthly breakdown with tests.
- **[Kimi decimals are accidentally rounded]** → Use a declarative precision policy and fixtures containing `28.21`, `74.86`, and `15.72`.
- **[Reset duration becomes stale between refreshes]** → Derive from current `reset_in_sec` on each render and retain existing periodic refresh cadence; do not invent absolute timestamps in the template.
- **[Optional details break row alignment]** → Attach details to a documented row slot and verify cards with and without details at both viewport widths.
- **[Escaping regresses during template extraction]** → Centralize all text escaping at shared renderer boundaries and test hostile provider/account labels.
- **[Simplifying provider choices breaks dispatch]** → Keep stable provider type values independent of display content and test all four option → step-two mappings.

## Migration Plan

1. Record GitNexus impact and RED characterization tests for the current provider renderers, shared CSS selectors, event delegation, and reset formats.
2. Add the pure compact-duration and percentage formatters plus shared view-model/shell/row/extension-slot tests.
3. Migrate OpenCode Go and Ollama through adapters without changing their displayed semantics.
4. Migrate Kimi through its adapter, select `reset_in_sec`, preserve decimals/Monthly fill/details, and remove remaining duplicated Kimi row markup.
5. Remove only duplicated provider-specific shell/row/CSS paths after parity tests pass; retain namespaced extension content and styles that represent real provider differences.
6. Replace Add Account's subtitle-bearing options with the shared centered name-only selector while retaining provider type dispatch.
7. Run focused/full/race/nogui/vet/build/OpenSpec gates and inspect mixed-provider cards and the Add Account dialog at wide and narrow widths.

Rollback restores the previous provider renderers; no persisted data or API migration is required.

## Open Questions

None. Percentage preservation, relative reset units, shared-foundation boundaries, Kimi's Monthly detail extension, the DeepSeek exception, and the centered name-only provider selector are defined.
