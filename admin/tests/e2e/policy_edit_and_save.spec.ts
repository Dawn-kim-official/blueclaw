import { expect, test } from "@playwright/test";

test("policy page renders", async ({ page }) => {
  await page.goto("/admin/policy");
  await expect(page).toHaveURL(/admin\/policy/);
});
