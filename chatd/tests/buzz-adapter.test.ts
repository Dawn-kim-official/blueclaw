import { describe, expect, test } from "bun:test";
import { BuzzAdapter } from "../src/adapters/buzz/adapter.ts";
import { firstTagValue, threadTagsOf, type BuzzEvent } from "../src/adapters/buzz/types.ts";

const CHANNEL_UUID = "8f14e45f-ea3c-4c2d-9d4b-1a2b3c4d5e6f";
const ROOT_EVENT_ID = "a".repeat(64);
const SENDER_HEX = "c".repeat(64);
const AGENT_SECRET = "1".repeat(64);

function createAdapter(): BuzzAdapter {
	return new BuzzAdapter({
		relayURL: "ws://localhost:3000",
		privateKeyHex: AGENT_SECRET,
		botDisplayName: "김인턴",
	});
}

function createEvent(overrides: Partial<BuzzEvent> = {}): BuzzEvent {
	return {
		id: "e".repeat(64),
		pubkey: SENDER_HEX,
		created_at: 1784900000,
		kind: 9,
		tags: [["h", CHANNEL_UUID]],
		content: "@김인턴 안녕",
		sig: "f".repeat(128),
		...overrides,
	};
}

describe("buzz thread id codec", () => {
	test("round-trips channel and root", () => {
		const adapter = createAdapter();
		const threadId = adapter.encodeThreadId({ channelId: CHANNEL_UUID, rootEventId: ROOT_EVENT_ID });
		expect(adapter.decodeThreadId(threadId)).toEqual({ channelId: CHANNEL_UUID, rootEventId: ROOT_EVENT_ID });
		expect(adapter.channelIdFromThreadId(threadId)).toBe(CHANNEL_UUID);
	});

	test("channel-only thread id omits root", () => {
		const adapter = createAdapter();
		const threadId = adapter.encodeThreadId({ channelId: CHANNEL_UUID });
		expect(adapter.decodeThreadId(threadId)).toEqual({ channelId: CHANNEL_UUID, rootEventId: undefined });
	});

	test("rejects foreign thread ids", () => {
		const adapter = createAdapter();
		expect(() => adapter.decodeThreadId("mattermost:abc")).toThrow();
	});
});

describe("buzz event mapping", () => {
	test("thread tags prefer marked root", () => {
		const tags = threadTagsOf(
			createEvent({
				tags: [
					["h", CHANNEL_UUID],
					["e", ROOT_EVENT_ID, "", "root"],
					["e", "b".repeat(64), "", "reply"],
				],
			}),
		);
		expect(tags.rootEventId).toBe(ROOT_EVENT_ID);
		expect(tags.parentEventId).toBe("b".repeat(64));
	});

	test("parseMessage maps a channel event into a Message", () => {
		const adapter = createAdapter();
		const message = adapter.parseMessage(createEvent());
		expect(message.text).toBe("@김인턴 안녕");
		expect(message.author.userId).toBe(SENDER_HEX);
		expect(message.threadId).toBe(`buzz:${CHANNEL_UUID}:${"e".repeat(64)}`);
		expect(message.metadata.dateSent.toISOString()).toBe(new Date(1784900000 * 1000).toISOString());
	});

	test("reply events thread under their root", () => {
		const adapter = createAdapter();
		const message = adapter.parseMessage(
			createEvent({ tags: [["h", CHANNEL_UUID], ["e", ROOT_EVENT_ID, "", "root"]] }),
		);
		expect(message.threadId).toBe(`buzz:${CHANNEL_UUID}:${ROOT_EVENT_ID}`);
	});

	test("firstTagValue reads the channel tag", () => {
		expect(firstTagValue(createEvent(), "h")).toBe(CHANNEL_UUID);
	});
});
