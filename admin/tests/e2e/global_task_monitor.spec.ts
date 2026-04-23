import { expect, test } from "@playwright/test";

test("global task monitor route renders", async ({ page }) => {
  await page.goto("/admin/task");
  await expect(page).toHaveURL(/admin\/task/);
});
