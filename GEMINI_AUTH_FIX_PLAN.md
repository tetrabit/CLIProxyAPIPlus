# Gemini Authentication Fix Plan

## Goal
Fix Gemini authentication so credentials created by all login paths are valid and usable by the `gemini-cli` runtime without manual repair.

## Problem Summary
- Gemini auth records can be created with incomplete onboarding state (notably missing/empty `project_id`).
- Runtime requests depend on `project_id` for Gemini CLI traffic, so login may appear successful but requests fail later.
- Credential naming and metadata handling are not fully normalized across SDK/CLI/management login paths.

## Plan
1. Add regression tests first.
- Add a failing test for Gemini login records missing required metadata (`project_id`, token fields).
- Add a failing test for canonical Gemini credential filename generation.

2. Unify Gemini login behavior.
- Extract shared Gemini onboarding/project resolution logic used by CLI and management flows.
- Reuse that shared logic in the SDK Gemini authenticator path.

3. Normalize persisted auth records.
- Use canonical Gemini credential filename generation everywhere.
- Ensure metadata always includes `email`, `project_id`, `auto`, and `checked`.
- Keep token map structure consistent for refresh and executor usage.

4. Add runtime guardrails.
- Add explicit validation in Gemini CLI execution to return a clear error when `project_id` is missing for generation calls.
- Keep `countTokens` behavior compatible where project may be omitted.

5. Validate end-to-end.
- Run targeted unit tests for auth/login/executor behavior.
- Smoke test CLI login and management login for:
  - explicit project,
  - auto-selected project,
  - multi-project (`ALL`) cases.

## Acceptance Criteria
- Newly generated Gemini credentials always include a usable `project_id` for generation calls.
- SDK, CLI, and management login paths produce equivalent credential quality and metadata shape.
- No regression in token refresh behavior.
- Error messages are actionable when credentials are incomplete.

## Rollout Notes
- This is backward-compatible for existing valid credentials.
- Existing invalid credentials should surface clear remediation guidance (re-login or metadata repair).
