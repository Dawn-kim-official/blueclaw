import { describe, expect, test } from "bun:test";
import { normalizePlatformAdapter, type ContextCapableAdapter } from "../src/visible-context.ts";

function adapterWithoutChatdHooks(): ContextCapableAdapter {
	return {
		name: "somewhere",
		async fetchMessages() {
			return { messages: [] };
		},
		async fetchThread() {
			return { channelId: "c1", channelName: "general", isDM: false } as never;
		},
		async getUser() {
			return null;
		},
	};
}

describe("a Chat SDK adapter is usable without writing chatd-specific hooks", () => {
	test("history stays on the thread the message arrived in", () => {
		const adapter = normalizePlatformAdapter(adapterWithoutChatdHooks());

		expect(adapter.historyScopeThreadId("somewhere:c1:root", "m1")).toBe("somewhere:c1:root");
	});

	test("an adapter that cannot report mentions claims none", () => {
		const adapter = normalizePlatformAdapter(adapterWithoutChatdHooks());

		expect(adapter.addressingOf({ anything: true })).toEqual({
			botMentioned: false,
			otherPersonMentioned: false,
		});
	});

	test("an adapter that reports its own addressing keeps it", () => {
		const adapter = normalizePlatformAdapter({
			...adapterWithoutChatdHooks(),
			historyScopeThreadId: (threadId: string) => `${threadId}:scoped`,
			addressingOf: () => ({ botMentioned: true, otherPersonMentioned: false }),
		} as ContextCapableAdapter);

		expect(adapter.historyScopeThreadId("somewhere:c1", "m1")).toBe("somewhere:c1:scoped");
		expect(adapter.addressingOf(null).botMentioned).toBe(true);
	});

	test("normalizing keeps the adapter's own methods reachable", async () => {
		const adapter = normalizePlatformAdapter(adapterWithoutChatdHooks());

		expect(adapter.name).toBe("somewhere");
		expect(await adapter.fetchMessages("somewhere:c1")).toEqual({ messages: [] });
	});
});
