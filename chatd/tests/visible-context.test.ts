import { describe, expect, test } from "bun:test";
import {
	buildVisibleContext,
	decodeHistoryCursor,
	encodeHistoryCursor,
	type ContextCapableAdapter,
} from "../src/visible-context.ts";

function contextMessage(id: string, text: string, sentAtSecond: number) {
	return {
		id,
		text,
		author: { userId: `user-${id}`, userName: `handle-${id}`, fullName: `Name ${id}` },
		metadata: { dateSent: new Date(sentAtSecond * 1000) },
	};
}

function fakeAdapter(messages: ReturnType<typeof contextMessage>[]): ContextCapableAdapter {
	return {
		name: "fake",
		async fetchMessages() {
			return { messages };
		},
		async fetchThread(threadId: string) {
			return { id: threadId, channelId: "channel-1", channelName: "general", isDM: false, metadata: {} };
		},
		async getUser(userId: string) {
			return { userId, userName: "sender-handle", fullName: "Sender Name", email: "sender@test", isBot: false };
		},
	};
}

describe("history cursor codec", () => {
	test("round-trips thread and cursor", () => {
		const encoded = encodeHistoryCursor({ threadId: "fake:channel-1", cursor: "page-2" });
		expect(decodeHistoryCursor(encoded)).toEqual({ threadId: "fake:channel-1", cursor: "page-2" });
	});

	test("treats opaque values as bare thread ids", () => {
		expect(decodeHistoryCursor("fake:channel-1")).toEqual({ threadId: "fake:channel-1" });
	});
});

describe("buildVisibleContext", () => {
	test("returns messages before the triggering message with sender and channel info", async () => {
		const adapter = fakeAdapter([
			contextMessage("a", "first", 100),
			contextMessage("b", "second", 200),
			contextMessage("c", "trigger", 300),
		]);
		const context = await buildVisibleContext(adapter, "fake:channel-1", {
			beforeMessageId: "c",
			senderId: "user-c",
		});
		expect(context.messages.map((message) => message.text)).toEqual(["first", "second"]);
		expect(context.messages[0]).toEqual({
			speaker: "Name a",
			speakerHandle: "handle-a",
			text: "first",
			sentAt: new Date(100 * 1000).toISOString(),
		});
		expect(context.sender).toEqual({
			platform: "fake",
			senderID: "user-c",
			handle: "sender-handle",
			email: "sender@test",
			name: "Sender Name",
		});
		expect(context.channelID).toBe("channel-1");
		expect(context.channelName).toBe("general");
		expect(context.conversationType).toBe("channel");
		expect(context.hasMoreBefore).toBe(false);
	});

	test("trims to the limit and reports more history", async () => {
		const messages = Array.from({ length: 5 }, (_, index) =>
			contextMessage(`m${index}`, `message ${index}`, 100 + index),
		);
		const adapter = fakeAdapter([...messages, contextMessage("t", "trigger", 900)]);
		const context = await buildVisibleContext(adapter, "fake:channel-1", { beforeMessageId: "t", limit: 3 });
		expect(context.messages.map((message) => message.text)).toEqual(["message 2", "message 3", "message 4"]);
		expect(context.hasMoreBefore).toBe(true);
	});

	test("drops the triggering message when it is not in the window", async () => {
		const adapter = fakeAdapter([contextMessage("a", "first", 100)]);
		const context = await buildVisibleContext(adapter, "fake:channel-1", { beforeMessageId: "missing" });
		expect(context.messages.map((message) => message.text)).toEqual(["first"]);
	});
});
