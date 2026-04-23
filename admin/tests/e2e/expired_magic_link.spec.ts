import { expect, test } from "@playwright/test";

test("expired magic link placeholder route renders", async ({ page }) => {
  await page.goto("/login/expired-token");
  await expect(page.locator("body")).toContainText("Magic link token");
});
