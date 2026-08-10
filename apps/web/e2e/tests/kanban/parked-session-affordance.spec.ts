import { test, expect } from "../../fixtures/test-base";
import { KanbanPage } from "../../pages/kanban-page";
import { SessionPage } from "../../pages/session-page";

/**
 * Parked-session affordance (AC-23, AC-58, AC-73a, AC-52).
 *
 * A session that settled to WAITING_FOR_INPUT while a background shell
 * workload is still live is "parked". The board card, the sidebar task list,
 * and the task-list row must render `data-testid="task-state-background-running"`
 * rather than the plain WAITING_FOR_INPUT question-mark icon.
 *
 * Backend plumbing is not exercised here — deterministically driving a real
 * detached background process through the launch recogniser and liveness
 * probe is out of scope for this fixture. Instead each surface is fed the
 * `parked_on_background_work` projection at the point it actually reads it:
 * the board reads `state.kanbanMulti.snapshots[workflowId].tasks`
 * (components/kanban-board.tsx), so the board test mutates that store slice
 * via the `__KANDEV_E2E_STORE__` bridge. The `/tasks` list has no live store
 * subscription at all — `app/tasks/tasks-page-client.tsx` holds its own
 * `useState` seeded from the `GET /api/v1/workspaces/:id/tasks` response — so
 * the list-row test intercepts that response instead.
 */
type E2EStoreState = {
  kanban: { tasks: Array<Record<string, unknown>> };
  kanbanMulti: { snapshots: Record<string, { tasks: Array<Record<string, unknown>> }> };
};

type E2EStoreWindow = Window & {
  __KANDEV_E2E_STORE__?: {
    getState: () => E2EStoreState;
    setState: (updater: (state: E2EStoreState) => Partial<E2EStoreState> | void) => void;
  };
};

/**
 * Immutably updates the task in state.kanbanMulti.snapshots[workflowId].tasks
 * (what the board card reads) AND its counterpart in state.kanban.tasks (the
 * "active workflow" slice). Both are necessary for the sidebar:
 * aggregateSidebarTasks (task-session-sidebar-aggregate.ts) overlays
 * state.kanban.tasks on top of the snapshot as an "active kanban fallback",
 * preferring whichever entry is newer by statusSummary.revision/updatedAt —
 * so a snapshot-only injection is silently overwritten by the unparked
 * active-slice entry the moment the sidebar aggregates. Builds fresh array
 * and object references at every level touched (rather than mutating items
 * in place) so every Zustand selector — however granular — observes the
 * change; a granular selector like `state.kanban.tasks` bails out on an
 * unchanged reference even when the object it points at was mutated in place.
 *
 * Also sets parkedRevision to a sentinel far above anything a real backend
 * would produce (the session was never actually parked server-side, so any
 * background snapshot refetch — useAllWorkflowSnapshots polls / refocuses —
 * carries the real parkedRevision, still 0). Without this, resolveParkedTriple
 * (lib/kanban/parked-projection.ts)'s D1 discard rule sees the refetch's
 * revision (0) as >= this injection's own unset revision (0) and the "fresh"
 * unparked value silently wins the race, discarding the injection entirely.
 */
async function injectParkedBoardTask(
  page: import("@playwright/test").Page,
  workflowId: string,
  taskId: string,
) {
  await page.evaluate(
    ({ workflowId, taskId }) => {
      const store = (window as E2EStoreWindow).__KANDEV_E2E_STORE__;
      if (!store) throw new Error("E2E store bridge missing");
      const parkTask = (t: Record<string, unknown>) =>
        t.id === taskId
          ? {
              ...t,
              state: "WAITING_FOR_INPUT",
              parkedOnBackgroundWork: true,
              parkedRevision: Number.MAX_SAFE_INTEGER,
            }
          : t;
      store.setState((state) => {
        const snapshot = state.kanbanMulti.snapshots[workflowId];
        if (!snapshot) throw new Error(`No kanbanMulti snapshot for workflow ${workflowId}`);
        if (!snapshot.tasks.some((t) => t.id === taskId)) {
          throw new Error(`Task ${taskId} not found in kanbanMulti snapshot`);
        }
        return {
          kanban: { ...state.kanban, tasks: state.kanban.tasks.map(parkTask) },
          kanbanMulti: {
            ...state.kanbanMulti,
            snapshots: {
              ...state.kanbanMulti.snapshots,
              [workflowId]: { ...snapshot, tasks: snapshot.tasks.map(parkTask) },
            },
          },
        };
      });
    },
    { workflowId, taskId },
  );
}

/**
 * Immutably marks the task as actively background-running (NOT parked) —
 * `foregroundActivity: "background"`, `parkedOnBackgroundWork` left unset.
 * Regression coverage for AC-59 (Review round 7, F1): this is the
 * pre-existing signal that must render byte-identically to before the
 * parked feature shipped — a bare IconLoader with no `data-testid` — and
 * must NOT show `task-state-background-running`, which is reserved for the
 * distinct parked condition. Same store-mutation shape as
 * injectParkedBoardTask, minus the parked fields.
 */
async function injectBackgroundActivityBoardTask(
  page: import("@playwright/test").Page,
  workflowId: string,
  taskId: string,
) {
  await page.evaluate(
    ({ workflowId, taskId }) => {
      const store = (window as E2EStoreWindow).__KANDEV_E2E_STORE__;
      if (!store) throw new Error("E2E store bridge missing");
      const markBackground = (t: Record<string, unknown>) =>
        t.id === taskId ? { ...t, foregroundActivity: "background" } : t;
      store.setState((state) => {
        const snapshot = state.kanbanMulti.snapshots[workflowId];
        if (!snapshot) throw new Error(`No kanbanMulti snapshot for workflow ${workflowId}`);
        if (!snapshot.tasks.some((t) => t.id === taskId)) {
          throw new Error(`Task ${taskId} not found in kanbanMulti snapshot`);
        }
        return {
          kanban: { ...state.kanban, tasks: state.kanban.tasks.map(markBackground) },
          kanbanMulti: {
            ...state.kanbanMulti,
            snapshots: {
              ...state.kanbanMulti.snapshots,
              [workflowId]: { ...snapshot, tasks: snapshot.tasks.map(markBackground) },
            },
          },
        };
      });
    },
    { workflowId, taskId },
  );
}

/**
 * Intercept the tasks-list fetch and mark `taskId` as parked in the response
 * body. Must be registered before navigation: `app/tasks/tasks-page-client.tsx`
 * fetches this endpoint on mount with no Vite-side SSR to bypass, so Playwright
 * observes it on first load like any other client fetch.
 */
async function interceptParkedTaskListResponse(
  page: import("@playwright/test").Page,
  taskId: string,
) {
  await page.route("**/api/v1/workspaces/*/tasks*", async (route) => {
    if (route.request().method() !== "GET") {
      await route.continue();
      return;
    }
    const response = await route.fetch();
    const body = (await response.json()) as {
      tasks: Array<Record<string, unknown>>;
      total: number;
    };
    await route.fulfill({
      response,
      json: {
        ...body,
        tasks: body.tasks.map((task) =>
          task.id === taskId ? { ...task, parked_on_background_work: true } : task,
        ),
      },
    });
  });
}

test.describe("Parked-session affordance", () => {
  test("board card shows background-running icon when task is parked (AC-58)", async ({
    testPage,
    apiClient,
    seedData,
  }) => {
    const task = await apiClient.createTask(seedData.workspaceId, "Parked Board Card Test", {
      workflow_id: seedData.workflowId,
      workflow_step_id: seedData.startStepId,
    });

    const kanban = new KanbanPage(testPage);
    await kanban.goto();

    const card = kanban.taskCard(task.id);
    await expect(card).toBeVisible({ timeout: 10_000 });

    await injectParkedBoardTask(testPage, seedData.workflowId, task.id);

    // The board card must show the violet background-running spinner (AC-58),
    // not the question-mark icon.
    await expect(card.getByTestId("task-state-background-running")).toBeVisible({ timeout: 5_000 });
    await expect(card.getByTestId("task-state-waiting-for-input")).not.toBeVisible();
  });

  // AC-59 regression guard (Review round 7, F1): before the fix,
  // getTaskStateIconConfig merged the pre-existing foregroundActivity ===
  // "background" branch and the new parked branch into the same sentinel, so
  // BOTH rendered task-state-background-running in the live DOM — silently
  // breaking the spec's "byte-identical to before this feature" requirement
  // for a task that is actively running background work but is NOT parked.
  // No unit test caught this because both branches produced the same output;
  // this asserts the real, distinct rendering on a live board.
  test("board card keeps the pre-existing plain spinner for background activity that is NOT parked (AC-59)", async ({
    testPage,
    apiClient,
    seedData,
  }) => {
    const task = await apiClient.createTask(seedData.workspaceId, "Background Not Parked Test", {
      workflow_id: seedData.workflowId,
      workflow_step_id: seedData.startStepId,
    });

    const kanban = new KanbanPage(testPage);
    await kanban.goto();

    const card = kanban.taskCard(task.id);
    await expect(card).toBeVisible({ timeout: 10_000 });

    await injectBackgroundActivityBoardTask(testPage, seedData.workflowId, task.id);

    // Must render SOME icon (the pre-existing bare IconLoader — no
    // data-testid, identified by its tabler icon class, same selector the
    // frontend unit tests use)...
    await expect(card.locator(".tabler-icon-loader")).toBeVisible({ timeout: 5_000 });
    // ...and must NOT render the parked-specific affordance, which is
    // reserved for the distinct parked condition (AC-59's "byte-identical"
    // requirement — this is the exact regression that survived 6 review
    // rounds and 5 rewritten unit tests before being caught here).
    await expect(card.getByTestId("task-state-background-running")).not.toBeVisible();
  });

  // AC-23: TaskRowItem's <TaskItem> call is the app's only production call
  // site for the sidebar/mobile task switcher's icon resolver — Review round
  // 6, Build round 9 fixed it never passing `parkedOnBackgroundWork` through,
  // which a component test rendering TaskItem directly (task-item.test.tsx)
  // could not catch because it bypasses TaskRowItem entirely. This is the
  // live-DOM regression guard for that exact wiring path.
  test("sidebar task list shows background-running icon when task is parked (AC-23)", async ({
    testPage,
    apiClient,
    seedData,
  }) => {
    const task = await apiClient.createTask(seedData.workspaceId, "Parked Sidebar Test", {
      workflow_id: seedData.workflowId,
      workflow_step_id: seedData.startStepId,
    });

    const kanban = new KanbanPage(testPage);
    await kanban.goto();

    const session = new SessionPage(testPage);
    const sidebarRow = session.sidebarTaskItem("Parked Sidebar Test");
    await expect(sidebarRow).toBeVisible({ timeout: 10_000 });

    // Same store slice the sidebar's useWorkspaceSidebarTasks aggregates from
    // (state.kanbanMulti.snapshots), so this injection reaches both the board
    // card and the sidebar row.
    await injectParkedBoardTask(testPage, seedData.workflowId, task.id);

    await expect(sidebarRow.getByTestId("task-state-background-running")).toBeVisible({
      timeout: 5_000,
    });
    await expect(sidebarRow.getByTestId("task-state-waiting-for-input")).not.toBeVisible();
  });

  test("task list row shows background-running icon when task is parked (AC-73a)", async ({
    testPage,
    apiClient,
    seedData,
  }) => {
    const task = await apiClient.createTask(seedData.workspaceId, "Parked Task List Test", {
      workflow_id: seedData.workflowId,
      workflow_step_id: seedData.startStepId,
    });

    await interceptParkedTaskListResponse(testPage, task.id);

    await testPage.goto("/tasks");
    await testPage.waitForSelector('[data-testid="tasks-list-row-title"]', { timeout: 15_000 });

    // Find the row and check for the parked icon (AC-73a).
    const row = testPage.locator(`[data-testid="tasks-list-row-title"]`, {
      hasText: "Parked Task List Test",
    });
    await expect(row).toBeVisible({ timeout: 5_000 });
    // The icon is a sibling of the title, both inside a flex container.
    const rowContainer = row.locator("..");
    await expect(rowContainer.getByTestId("task-state-background-running")).toBeVisible({
      timeout: 5_000,
    });
  });
});
