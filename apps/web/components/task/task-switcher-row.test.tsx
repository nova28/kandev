import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { StateProvider } from "@/components/state-provider";
import { ToastProvider } from "@/components/toast-provider";
import { TooltipProvider } from "@kandev/ui/tooltip";
import { TaskSwitcher, type TaskSwitcherItem } from "./task-switcher";
import type { GroupedSidebarList } from "@/lib/sidebar/apply-view";

afterEach(() => cleanup());

function Providers({ children }: { children: React.ReactNode }) {
  return (
    <StateProvider>
      <ToastProvider>
        <TooltipProvider>{children}</TooltipProvider>
      </ToastProvider>
    </StateProvider>
  );
}

function grouped(task: TaskSwitcherItem): GroupedSidebarList {
  return {
    groups: [{ key: "__all__", label: "All", tasks: [task] }],
    subTasksByParentId: new Map(),
  };
}

function renderSwitcherWithTask(task: TaskSwitcherItem) {
  return render(
    <Providers>
      <TaskSwitcher
        grouped={grouped(task)}
        activeTaskId={null}
        selectedTaskId={null}
        onSelectTask={vi.fn()}
      />
    </Providers>,
  );
}

// AC-23 / AC-83: TaskRowItem is the only production call site of <TaskItem>,
// so its wiring is what actually puts the parked affordance on the sidebar
// and mobile task switcher. Every upstream producer (buildSidebarItem,
// toSheetItem) already carries parkedOnBackgroundWork on TaskSwitcherItem —
// this test only proves the last hop, from TaskSwitcherItem to TaskItem's
// rendered icon.
describe("TaskRow — parked-on-background-work affordance (AC-23)", () => {
  it("renders the background-running icon when the task is parked", () => {
    renderSwitcherWithTask({
      id: "task-parked",
      title: "Parked task",
      state: "REVIEW",
      parkedOnBackgroundWork: true,
    });
    expect(screen.getByTestId("task-state-background-running")).not.toBeNull();
  });

  it("does not render the background-running icon when the task is not parked", () => {
    renderSwitcherWithTask({
      id: "task-not-parked",
      title: "Not parked task",
      state: "REVIEW",
      parkedOnBackgroundWork: false,
    });
    expect(screen.queryByTestId("task-state-background-running")).toBeNull();
  });
});
