import { describe, expect, test } from "bun:test";
import { canonicalChannelName, supportsChannelProvisioning } from "../src/channels.ts";

describe("canonicalChannelName", () => {
	test("strips leading hashes and surrounding whitespace", () => {
		expect(canonicalChannelName("  #attendance  ")).toBe("attendance");
		expect(canonicalChannelName("##flow")).toBe("flow");
		expect(canonicalChannelName("calendar")).toBe("calendar");
	});
});

describe("supportsChannelProvisioning", () => {
	test("detects adapters that can ensure channels", () => {
		expect(supportsChannelProvisioning({ ensureChannel: async () => ({}) })).toBe(true);
		expect(supportsChannelProvisioning({ name: "mattermost" })).toBe(false);
	});
});
