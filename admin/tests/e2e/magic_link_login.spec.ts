import { expect, test } from "@playwright/test";

test("magic link route renders", async ({ page }) => {
  await page.goto("/login/example-token");
  await expect(page).toHaveURL(/login\/example-token/);
});
