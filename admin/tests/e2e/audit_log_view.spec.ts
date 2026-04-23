import { expect, test } from "@playwright/test";

test("audit page renders", async ({ page }) => {
  await page.goto("/admin/audit");
  await expect(page).toHaveURL(/admin\/audit/);
});
