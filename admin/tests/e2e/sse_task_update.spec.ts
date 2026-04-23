import { expect, test } from "@playwright/test";

test("task route is available for SSE consumers", async ({ page }) => {
  await page.goto("/tasks?taskSessionID=session");
  await expect(page.locator("body")).toContainText("Blueclaw Tasks");
});
