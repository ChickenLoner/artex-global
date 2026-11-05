<!--
  This PR template is for artex-global (the ARTEX global edition) only.
  Please read CONTRIBUTING.md first for the contribution policy and localization principles.
-->

## Summary

<!-- Describe what you are changing and why in one or two sentences. -->

## Type of change

<!-- Put an x in the items that apply. -->

- [ ] Bug fix (`fix`)
- [ ] New feature (`feat`)
- [ ] Documentation (`docs`)
- [ ] Localization / translation (`i18n`)
- [ ] Refactoring / cleanup (`refactor` / `chore`)
- [ ] Other:

## Related issues

<!-- e.g. Closes #123 -->

## How it was verified

<!-- Describe which commands you ran yourself and what you checked. Attaching result logs helps. -->

- [ ] Go changes: `go build ./...` · `go vet ./agent/` · `go test ./agent/` pass
- [ ] web changes: `npm run check` · `npm run build` pass
- [ ] UI changes: screenshots attached

## Localization checklist

<!-- If the PR is unrelated to localization, you may leave this section empty. -->

- [ ] I did **not translate the agent's internal reasoning prompts (the behavior-instruction text, `agent/promptcatalog.go` / `agent_prompts`).** (Preserves performance.)
- [ ] I localized into Korean only the user-facing output (detection results, summaries, reports, chat replies).
- [ ] I left commands, payloads, code, URLs, and raw log text untranslated.
- [ ] I did not hardcode UI strings; I added them as `web/messages/ko.json` keys and kept the originals in `web/messages/zh.json`.

## Authorized-use confirmation

- [ ] This change does not encourage use outside the [authorized scope](../README.md#️-read-first--authorized-use-and-legal-notice)
      or applicable law. I verified it only against targets I own or am authorized to test,
      or in a local isolated environment.
- [ ] I did not include test or scan output in the commit.
