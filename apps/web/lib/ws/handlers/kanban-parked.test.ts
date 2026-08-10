import { describe, it, expect } from "vitest";
import type { StoreApi } from "zustand";
import type { AppState } from "@/lib/state/store";
import { registerKanbanHandlers } from "./kanban";

// AC-58a: kanban.update never carries the parked triple (see the comment on
// buildKanbanTaskFromUpdate in kanban.ts), so both projections must preserve
// it from whatever the existing row already had — the structurally
// identical preserve-on-omission path kanban.test.ts already covers for
// foregroundActivity. Split into its own file (rather than kanban.test.ts)
// to keep that file under its 600-line limit, mirroring tasks-parked.test.ts.

const WORKFLOW_ID = "wf1";
const TASK_ID = "t1";
const STEP_ID = "s1";
const TASK_TITLE = "T1";

function makeStore(initial: Partial<AppState> = {}) {
  let state = {
    kanban: { workflowId: null, steps: [], tasks: [] },
    kanbanMulti: { snapshots: {}, isLoading: false },
    ...initial,
  } as unknown as AppState;

  return {
    getState: () => state,
    setState: (updater: AppState | ((s: AppState) => AppState)) => {
      state =
        typeof updater === "function" ? (updater as (s: AppState) => AppState)(state) : updater;
    },
    subscribe: () => () => {},
    destroy: () => {},
    getInitialState: () => state,
  } as unknown as StoreApi<AppState>;
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function makeUpdateMessage(workflowId: string, tasks: unknown[], steps: unknown[] = []): any {
  return {
    id: "msg-1",
    type: "notification",
    action: "kanban.update",
    payload: { workflowId, tasks, steps },
  };
}

describe("kanban.update handler — parked triple preservation (AC-58a)", () => {
  it("preserves parkedOnBackgroundWork/parkedEpoch/parkedRevision from existing tasks in both projections", () => {
    const store = makeStore({
      kanban: {
        workflowId: WORKFLOW_ID,
        steps: [],
        tasks: [
          {
            id: TASK_ID,
            workflowStepId: STEP_ID,
            title: TASK_TITLE,
            position: 0,
            parkedOnBackgroundWork: true,
            parkedEpoch: 42,
            parkedRevision: 3,
          },
        ],
      },
      kanbanMulti: {
        isLoading: false,
        snapshots: {
          [WORKFLOW_ID]: {
            workflowId: WORKFLOW_ID,
            workflowName: "WF1",
            steps: [],
            tasks: [
              {
                id: TASK_ID,
                workflowStepId: STEP_ID,
                title: TASK_TITLE,
                position: 0,
                parkedOnBackgroundWork: true,
                parkedEpoch: 42,
                parkedRevision: 3,
              },
            ],
          },
        },
      },
    } as Partial<AppState>);

    const handler = registerKanbanHandlers(store)["kanban.update"]!;
    handler(
      makeUpdateMessage(WORKFLOW_ID, [
        { id: TASK_ID, workflowStepId: STEP_ID, title: TASK_TITLE, position: 0 },
      ]),
    );

    const task = store.getState().kanban.tasks.find((t) => t.id === TASK_ID);
    expect(task?.parkedOnBackgroundWork).toBe(true);
    expect(task?.parkedEpoch).toBe(42);
    expect(task?.parkedRevision).toBe(3);

    const snapshotTask = store
      .getState()
      .kanbanMulti.snapshots[WORKFLOW_ID]?.tasks.find((t) => t.id === TASK_ID);
    expect(snapshotTask?.parkedOnBackgroundWork).toBe(true);
    expect(snapshotTask?.parkedEpoch).toBe(42);
    expect(snapshotTask?.parkedRevision).toBe(3);
  });

  it("does not fabricate a parked triple for a task that never had one", () => {
    const store = makeStore({
      kanban: {
        workflowId: WORKFLOW_ID,
        steps: [],
        tasks: [{ id: TASK_ID, workflowStepId: STEP_ID, title: TASK_TITLE, position: 0 }],
      },
    } as Partial<AppState>);

    const handler = registerKanbanHandlers(store)["kanban.update"]!;
    handler(
      makeUpdateMessage(WORKFLOW_ID, [
        { id: TASK_ID, workflowStepId: STEP_ID, title: TASK_TITLE, position: 0 },
      ]),
    );

    const task = store.getState().kanban.tasks.find((t) => t.id === TASK_ID);
    expect(task?.parkedOnBackgroundWork).toBeUndefined();
    expect(task?.parkedEpoch).toBeUndefined();
    expect(task?.parkedRevision).toBeUndefined();
  });
});
