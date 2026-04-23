import { expect, test } from "bun:test";

test("task list can be represented as an array", () => {
  const taskRuns = [{ taskRunID: "task-1", status: "planned" }];
  expect(taskRuns).toHaveLength(1);
});
