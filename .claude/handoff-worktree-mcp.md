## Resume: finish the Pipeline App opt-in for bridge (agent pushes start no CI)

Read `docs/ai-notes/2026-10-02-pipeline-app-ci-trigger.md` (bridge repo) — it holds the problem, what is done (agent.yml wiring merged in #330, `PIPELINE_APP_PRIVATE_KEY` set), and the ordered open steps.

**Next step:** fix `gh` auth first (it returned 401 on 2026-10-06), then ask the user for the `freaxnx01-pipeline` App ID and confirmation the App is installed on `freaxnx01/bridge`; set `PIPELINE_APP_ID`; then get explicit approval before cutting an agent-workflow release that ships the #430 checkout-token fix to `v2`. Verify with the next dispatched PR (author `app/freaxnx01-pipeline`, non-zero checks).

For any implementation work, use `superpowers:subagent-driven-development`.
