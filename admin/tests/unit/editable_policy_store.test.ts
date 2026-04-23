import { expect, test } from "bun:test";

test("policy example shape stays editable", () => {
  const policyDocument = {
    people: [],
    channels: [],
    retention: {
      rawEventDays: 60
    }
  };

  expect(policyDocument.retention.rawEventDays).toBe(60);
});
