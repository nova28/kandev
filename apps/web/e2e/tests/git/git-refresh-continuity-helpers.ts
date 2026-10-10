import { expect, type Locator, type Page } from "@playwright/test";
import { waitForFiniteAnimations } from "../../helpers/animations";
import { createGitEnrichmentGate, type routeGitStatusRefresh } from "./git-status-refresh-helpers";

export type DiffRenderer = "pierre-diffs" | "monaco";

export type VisibleDiffAnchor = {
  line: number;
  side: string;
  content: string;
  offset: number;
};

export type GitRefreshBridge = Awaited<ReturnType<typeof routeGitStatusRefresh>>;

export async function openAllChangesDiff(panel: Locator, page: Page) {
  const direct = panel.getByRole("button", { name: "Diff", exact: true });
  const overflow = panel.getByTestId("panel-header-overflow").first();
  await expect
    .poll(async () => (await direct.isVisible()) || (await overflow.isVisible()), {
      timeout: 15_000,
      message: "the Changes panel should expose its full diff action",
    })
    .toBe(true);
  if (await direct.isVisible()) {
    await direct.click();
    return;
  }
  await overflow.click();
  await page.getByRole("menuitem", { name: "Diff", exact: true }).click();
}

export async function setDiffRenderer(page: Page, renderer: DiffRenderer) {
  await page.addInitScript((provider) => {
    localStorage.setItem(
      "kandev-editor-providers",
      JSON.stringify({
        version: 3,
        state: {
          providers: {
            "code-editor": "monaco",
            "diff-viewer": provider,
            "chat-code-block": "shiki",
            "chat-diff": "pierre-diffs",
            "plan-editor": "tiptap",
          },
        },
      }),
    );
  }, renderer);
}

export async function scrollDiffIntoReadingPosition(
  page: Page,
  renderer: DiffRenderer,
  filePath: string,
  interaction: "programmatic" | "touch" = "programmatic",
) {
