import { expect, test } from "../../fixtures/test-base";
import { findAgentctlPid, killAgentctl } from "../../helpers/agent-runtime-availability";
import { waitForSessionDone } from "../../helpers/session";
import { KanbanPage } from "../../pages/kanban-page";

test.describe("Agent runtime relaunch after a real crash", () => {
  // pgrep-based process-tree discovery in findAgentctlPid is POSIX-only.
  test.skip(process.platform === "win32", "requires a POSIX process tree");

  test.afterEach(async ({ backend }) => {
    // The killed-and-relaunched agentctl is a fresh process the backend does
    // not know how to hand back to a clean state for the next test; restart
    // the whole backend so the next test starts from the baseline launch.
    await backend.restart();
  });

  test("recovers the killed standalone agentctl and runs a new session to completion", async ({
    testPage,
    apiClient,
    seedData,
    backend,
  }) => {
    test.setTimeout(120_000);

    const backendPid = backend.pid();
    expect(backendPid).toBeTruthy();
    const agentctlPid = findAgentctlPid(backendPid!, backend.logPath);

    const kanban = new KanbanPage(testPage);
    await kanban.goto();

    killAgentctl(agentctlPid);

    const alert = testPage.getByTestId("agent-runtime-alert");
    await expect(alert).toHaveAttribute("data-status", "recovering", { timeout: 15_000 });
    await expect(alert).toHaveCount(0, { timeout: 20_000 });

    const task = await apiClient.createTaskWithAgent(
      seedData.workspaceId,
      "Runtime relaunch recovery session",
      seedData.agentProfileId,
      {
        description: 'e2e:message("runtime relaunch recovery")',
        workflow_id: seedData.workflowId,
        workflow_step_id: seedData.startStepId,
        repository_ids: [seedData.repositoryId],
      },
    );
    if (!task.session_id) throw new Error("runtime relaunch recovery task has no session");

    await waitForSessionDone(
      apiClient,
      task.id,
      task.session_id,
      "Waiting for the recovered runtime to run a session to completion",
    );
  });
});
