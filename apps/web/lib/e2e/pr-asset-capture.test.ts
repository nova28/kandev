import type { Page } from "@playwright/test";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it } from "vitest";

import { PrAssetCapture } from "../../e2e/helpers/pr-asset-capture";

const originalCaptureSetting = process.env.CAPTURE_PR_ASSETS;

afterEach(() => {
  if (originalCaptureSetting === undefined) {
    delete process.env.CAPTURE_PR_ASSETS;
    return;
  }
  process.env.CAPTURE_PR_ASSETS = originalCaptureSetting;
});

describe("PrAssetCapture", () => {
  it("does not remove assets created by another worker", () => {
    process.env.CAPTURE_PR_ASSETS = "1";
    const outputDir = fs.mkdtempSync(path.join(os.tmpdir(), "kandev-pr-assets-"));
    const existingAsset = path.join(outputDir, "desktop.png");
    fs.writeFileSync(existingAsset, "existing asset");

    new PrAssetCapture({} as Page, "/tmp/mobile-capture.spec.ts", { outputDir });

    expect(fs.existsSync(existingAsset)).toBe(true);
    fs.rmSync(outputDir, { recursive: true, force: true });
  });
});
