import { expect, test } from "@playwright/test";

test("task list route renders", async ({ page }) => {
  await page.goto("/tasks?taskSessionID=session");
  await expect(page).toHaveURL(/tasks/);
});
