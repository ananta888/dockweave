import { expect, test } from "@playwright/test";
test("renders 3D, starts, pauses, resumes and restarts without browser errors", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: "Crystal Run" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Starten", exact: true }).click();
  await expect(page.locator("#panel")).toBeHidden();
  await page.keyboard.down("KeyW");
  await page.waitForTimeout(300);
  await page.keyboard.up("KeyW");
  await page.keyboard.press("Escape");
  await expect(page.getByRole("button", { name: "Fortsetzen" })).toBeVisible();
  const paused = await page.locator("#hud").textContent();
  await page.waitForTimeout(250);
  await expect(page.locator("#hud")).toHaveText(paused!);
  await page.getByRole("button", { name: "Fortsetzen" }).click();
  await expect(page.locator("#panel")).toBeHidden();
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "Neu starten" }).click();
  await expect(page.locator("#hud")).toContainText("0 / 5");
  expect(errors).toEqual([]);
  await page.screenshot({ path: "test-results/crystal-run.png" });
});
