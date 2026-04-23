import { expect, test } from "@playwright/test";

test("task detail route renders", async ({ page }) => {
  await page.goto("/tasks/example-task?taskSessionID=session");
  await expect(page).toHaveURL(/tasks\/example-task/);
});
