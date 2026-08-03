## ADDED Requirements

### Requirement: OpenCode quota parsing tolerates supported seroval layout variation
The system SHALL parse OpenCode Go rolling, weekly, and optional monthly quota objects without depending on seroval reference numbers, property order, insignificant whitespace, or unrelated additional properties. It MUST preserve each parsed status, reset duration, and usage percentage under the correct quota window.

#### Scenario: Reference numbers and property order change
- **WHEN** a sanitized valid response moves each `$R[n]` reference and reorders `status`, `resetInSec`, and `usagePercent`
- **THEN** the parser returns the same rolling, weekly, and monthly values as the canonical response

#### Scenario: Usage objects contain whitespace and additional properties
- **WHEN** a valid usage object contains insignificant whitespace and unrelated primitive properties before, between, or after its required properties
- **THEN** the parser ignores the unrelated properties and preserves all required quota values

#### Scenario: Seroval object is inline
- **WHEN** a valid quota field contains its object inline without a `$R[n]=` assignment
- **THEN** the parser accepts the object and returns the same quota semantics as a referenced object

### Requirement: OpenCode quota parsing fails closed on unsupported data
Rolling and weekly usage objects SHALL be required. Every present usage object SHALL contain exactly one supported `status`, non-negative `resetInSec`, and non-negative `usagePercent`; the parser MUST return an error when required data is missing, duplicated, truncated, malformed, or embedded only in an unrelated field, and MUST NOT fabricate zero values or substitute another window.

#### Scenario: Rolling usage is missing
- **WHEN** an otherwise parseable response omits the exact `rollingUsage` field
- **THEN** parsing fails with a rolling-specific error and returns no quota result

#### Scenario: Required value is malformed or duplicated
- **WHEN** a usage object contains a negative, non-numeric, unsupported, or duplicate required property
- **THEN** parsing fails and does not choose an arbitrary occurrence or coerce it to zero

#### Scenario: Response is truncated or not a quota response
- **WHEN** the body ends inside a usage object or contains quota-shaped text only under an unrelated field name
- **THEN** parsing fails without returning partial rolling, weekly, or monthly data

#### Scenario: Monthly usage is absent or unlimited
- **WHEN** rolling and weekly are valid and monthly is absent or uses the supported `unlimited` form
- **THEN** rolling and weekly are returned and monthly remains absent according to the existing model

### Requirement: Parser evidence contains no authentication material
Parser regression fixtures, errors, logs, evidence, and commits SHALL contain only the minimum sanitized quota structure required to reproduce supported and rejected shapes. They MUST NOT contain cookies, workspace identifiers, authorization headers, raw private responses, or other account-specific material.

#### Scenario: A real failing shape is characterized
- **WHEN** a production response shape is used to create a RED regression fixture
- **THEN** the fixture retains only synthetic field structure and representative quota values and contains no authentication or account identifiers
