# wikistr-client Specification

## Purpose

Preserve legacy `wikistr.trustroots.org` GitHub Pages traffic by redirecting
visitors to the canonical Wikistr app in the Nostroots repo.

## Requirements

### Requirement: Legacy host redirects to Nostroots Wikistr

The Wrapster `apps/wikistr` GitHub Pages deployment SHALL redirect all requests
to `https://nos.trustroots.org/examples/wikistr/`.

#### Scenario: root request

- **GIVEN** a browser opens `https://wikistr.trustroots.org/`
- **WHEN** the redirect page loads
- **THEN** the browser is sent to `https://nos.trustroots.org/examples/wikistr/`

#### Scenario: hash route preserved

- **GIVEN** a browser opens `https://wikistr.trustroots.org/#hitchwiki`
- **WHEN** the redirect page loads
- **THEN** the browser is sent to `https://nos.trustroots.org/examples/wikistr/#hitchwiki`

### Requirement: Redirect page stays minimal

The legacy deployment MUST NOT ship the Wikistr client implementation and MUST
only publish redirect metadata plus a fallback link to the canonical app.

#### Scenario: static deployment contents

- **GIVEN** the GitHub Pages workflow uploads `apps/wikistr`
- **WHEN** the artifact is deployed
- **THEN** it contains only redirect assets such as `index.html`, `CNAME`, and docs
- **AND** it does not contain the Nostroots Wikistr client source
