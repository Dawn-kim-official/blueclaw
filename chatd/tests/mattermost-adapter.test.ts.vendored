import { afterEach, describe, expect, it, vi } from "vitest";
import { MattermostAdapter } from "./adapter";
import type { MattermostPost, MattermostUser } from "./types";

function createAdapter(withCallback = false) {
	return new MattermostAdapter({
		baseUrl: "https://mattermost.example.com",
		botToken: "test-token",
		userName: "mattermost-bot",
		callbackUrl: withCallback ? "https://bot.example.com/webhooks/mattermost" : undefined,
	});
}

function createPost(overrides: Partial<MattermostPost> = {}): MattermostPost {
	return {
		id: "post-1",
		channel_id: "channel-1",
		user_id: "user-1",
		message: "hello world",
		type: "",
		create_at: 1,
		update_at: 1,
		edit_at: 0,
		delete_at: 0,
		is_pinned: false,
		...overrides,
	};
}

function createUser(overrides: Partial<MattermostUser> = {}): MattermostUser {
	return {
		id: "user-1",
		username: "alice",
		...overrides,
	};
}

afterEach(() => {
	vi.unstubAllGlobals();
	vi.restoreAllMocks();
});

describe("MattermostAdapter", () => {
	it("roundtrips thread IDs", () => {
		const adapter = createAdapter();
		const encoded = adapter.encodeThreadId({
			channelId: "channel-123",
			rootPostId: "root-456",
		});

		expect(adapter.decodeThreadId(encoded)).toEqual({
			channelId: "channel-123",
			rootPostId: "root-456",
		});
	});

	it("returns 200 from handleWebhook", async () => {
		const adapter = createAdapter();
		const response = await adapter.handleWebhook(
			new Request("https://example.com/webhook", { method: "POST" }),
		);

		expect(response.status).toBe(200);
		expect(await response.text()).toBe("OK");
	});

	it("marks mentions from websocket mention payloads", async () => {
		const adapter = createAdapter() as MattermostAdapter & {
			chat: { processMessage: ReturnType<typeof vi.fn> };
			botUserId: string;
			users: Map<string, MattermostUser>;
			handlePostedEvent: (payload: unknown) => Promise<void>;
		};
		const processMessage = vi.fn();
		adapter.chat = { processMessage };
		adapter.botUserId = "bot-user";
		adapter.users.set("user-1", createUser());

		await adapter.handlePostedEvent({
			data: {
				post: JSON.stringify(createPost()),
				mentions: JSON.stringify(["bot-user"]),
			},
		});

		expect(processMessage).toHaveBeenCalledTimes(1);
		const messageFactory = processMessage.mock.calls[0][2] as () => Promise<{
			isMention?: boolean;
		}>;
		const message = await messageFactory();

		expect(message.isMention).toBe(true);
	});

	it("accepts websocket embedded objects for edited posts", async () => {
		const adapter = createAdapter() as MattermostAdapter & {
			chat: { processMessage: ReturnType<typeof vi.fn> };
			users: Map<string, MattermostUser>;
			handleWebSocketPayload: (payload: unknown) => Promise<void>;
		};
		const processMessage = vi.fn();
		adapter.chat = { processMessage };
		adapter.users.set("user-1", createUser());

		await adapter.handleWebSocketPayload({
			event: "post_edited",
			data: {
				post: createPost({ message: "edited" }),
			},
		});

		expect(processMessage).toHaveBeenCalledTimes(1);
	});

	it("parses binary websocket payloads", () => {
		const adapter = createAdapter() as MattermostAdapter & {
			parseWebSocketPayload: (data: unknown) => unknown;
		};
		const payload = { event: "posted", data: { post: JSON.stringify(createPost()) } };
		const binary = new TextEncoder().encode(JSON.stringify(payload));

		expect(adapter.parseWebSocketPayload(binary)).toEqual(payload);
	});

	it("returns null when fetchMessage gets a 404", async () => {
		const adapter = createAdapter();
		vi.stubGlobal(
			"fetch",
			vi.fn().mockResolvedValue(
				new Response(JSON.stringify({ message: "missing" }), {
					status: 404,
					headers: { "Content-Type": "application/json" },
				}),
			),
		);

		const result = await adapter.fetchMessage(
			adapter.encodeThreadId({ channelId: "channel-1", rootPostId: "root-1" }),
			"missing",
		);

		expect(result).toBeNull();
	});

	it("bounds user and channel caches", () => {
		const adapter = createAdapter() as MattermostAdapter & {
			users: Map<string, MattermostUser>;
			channels: Map<string, { id: string; name: string; type: "O" }>;
			setCachedValue: <TKey, TValue>(
				cache: Map<TKey, TValue>,
				key: TKey,
				value: TValue,
				maxSize: number,
			) => void;
		};

		for (let index = 0; index < 3; index += 1) {
			adapter.setCachedValue(
				adapter.users,
				`user-${index}`,
				createUser({ id: `user-${index}`, username: `user${index}` }),
				2,
			);
			adapter.setCachedValue(
				adapter.channels,
				`channel-${index}`,
				{ id: `channel-${index}`, name: `channel-${index}`, type: "O" },
				2,
			);
		}

		expect(adapter.users.size).toBe(2);
		expect(adapter.channels.size).toBe(2);
		expect(adapter.users.has("user-0")).toBe(false);
		expect(adapter.channels.has("channel-0")).toBe(false);
	});
});

describe("MattermostAdapter actions - card rendering", () => {
	it("converts card with buttons to Mattermost attachments", async () => {
		const adapter = createAdapter(true);
		const threadId = adapter.encodeThreadId({
			channelId: "channel-1",
			rootPostId: "root-1",
		});

		const card = {
			type: "card" as const,
			title: "Order #1234",
			children: [
				{ type: "text" as const, content: "Total: $50.00" },
				{
					type: "actions" as const,
					children: [
						{
							type: "button" as const,
							id: "approve",
							label: "Approve",
							style: "primary" as const,
						},
						{
							type: "button" as const,
							id: "reject",
							label: "Reject",
							style: "danger" as const,
						},
					],
				},
			],
		};

		vi.stubGlobal(
			"fetch",
			vi.fn().mockResolvedValue(
				new Response(
					JSON.stringify(
						createPost({
							id: "new-post-1",
							channel_id: "channel-1",
							root_id: "root-1",
						}),
					),
					{ status: 201, headers: { "Content-Type": "application/json" } },
				),
			),
		);

		const result = await adapter.postMessage(threadId, { card });
		expect(result.id).toBe("new-post-1");

		const fetchCall = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0];
		const body = JSON.parse(fetchCall[1].body as string);

		expect(body.props.attachments).toHaveLength(1);
		expect(body.props.attachments[0].title).toBe("Order #1234");
		expect(body.props.attachments[0].text).toBe("Total: $50.00");
		expect(body.props.attachments[0].actions).toHaveLength(2);
		expect(body.props.attachments[0].actions[0]).toEqual({
			id: "approve",
			name: "Approve",
			type: "button",
			style: "primary",
			integration: {
				url: "https://bot.example.com/webhooks/mattermost",
				context: { action_id: "approve" },
			},
		});
		expect(body.props.attachments[0].actions[1]).toEqual({
			id: "reject",
			name: "Reject",
			type: "button",
			style: "danger",
			integration: {
				url: "https://bot.example.com/webhooks/mattermost",
				context: { action_id: "reject" },
			},
		});
	});

	it("converts card with select to Mattermost attachment", async () => {
		const adapter = createAdapter(true);
		const threadId = adapter.encodeThreadId({ channelId: "channel-1" });

		const card = {
			type: "card" as const,
			children: [
				{
					type: "actions" as const,
					children: [
						{
							type: "select" as const,
							id: "color",
							label: "Pick a color",
							placeholder: "Choose...",
							options: [
								{ label: "Red", value: "red" },
								{ label: "Blue", value: "blue" },
							],
						},
					],
				},
			],
		};

		vi.stubGlobal(
			"fetch",
			vi
				.fn()
				.mockResolvedValue(
					new Response(
						JSON.stringify(createPost({ id: "new-post-2", channel_id: "channel-1" })),
						{ status: 201, headers: { "Content-Type": "application/json" } },
					),
				),
		);

		await adapter.postMessage(threadId, { card });

		const fetchCall = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0];
		const body = JSON.parse(fetchCall[1].body as string);

		expect(body.props.attachments[0].actions).toHaveLength(1);
		expect(body.props.attachments[0].actions[0]).toEqual({
			id: "color",
			name: "Choose...",
			type: "select",
			options: [
				{ text: "Red", value: "red" },
				{ text: "Blue", value: "blue" },
			],
			integration: {
				url: "https://bot.example.com/webhooks/mattermost",
				context: { action_id: "color" },
			},
		});
	});

	it("converts radio select to individual buttons", async () => {
		const adapter = createAdapter(true);
		const threadId = adapter.encodeThreadId({ channelId: "channel-1" });

		const card = {
			type: "card" as const,
			children: [
				{
					type: "actions" as const,
					children: [
						{
							type: "radio_select" as const,
							id: "priority",
							label: "Priority",
							options: [
								{ label: "High", value: "high" },
								{ label: "Low", value: "low" },
							],
						},
					],
				},
			],
		};

		vi.stubGlobal(
			"fetch",
			vi
				.fn()
				.mockResolvedValue(
					new Response(
						JSON.stringify(createPost({ id: "new-post-3", channel_id: "channel-1" })),
						{ status: 201, headers: { "Content-Type": "application/json" } },
					),
				),
		);

		await adapter.postMessage(threadId, { card });

		const fetchCall = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0];
		const body = JSON.parse(fetchCall[1].body as string);

		expect(body.props.attachments[0].actions).toHaveLength(2);
		expect(body.props.attachments[0].actions[0]).toEqual({
			id: "priority_high",
			name: "High",
			type: "button",
			integration: {
				url: "https://bot.example.com/webhooks/mattermost",
				context: { action_id: "priority", action_value: "high" },
			},
		});
		expect(body.props.attachments[0].actions[1]).toEqual({
			id: "priority_low",
			name: "Low",
			type: "button",
			integration: {
				url: "https://bot.example.com/webhooks/mattermost",
				context: { action_id: "priority", action_value: "low" },
			},
		});
	});

	it("omits attachments when no callbackUrl is configured", async () => {
		const adapter = createAdapter(false);
		const threadId = adapter.encodeThreadId({ channelId: "channel-1" });

		const card = {
			type: "card" as const,
			title: "No callback",
			children: [
				{
					type: "actions" as const,
					children: [{ type: "button" as const, id: "ok", label: "OK" }],
				},
			],
		};

		vi.stubGlobal(
			"fetch",
			vi
				.fn()
				.mockResolvedValue(
					new Response(
						JSON.stringify(createPost({ id: "new-post-4", channel_id: "channel-1" })),
						{ status: 201, headers: { "Content-Type": "application/json" } },
					),
				),
		);

		await adapter.postMessage(threadId, { card });

		const fetchCall = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0];
		const body = JSON.parse(fetchCall[1].body as string);

		expect(body.props).toBeUndefined();
	});

	it("includes button value in integration context", async () => {
		const adapter = createAdapter(true);
		const threadId = adapter.encodeThreadId({ channelId: "channel-1" });

		const card = {
			type: "card" as const,
			children: [
				{
					type: "actions" as const,
					children: [
						{
							type: "button" as const,
							id: "vote",
							label: "Vote",
							value: "yes",
						},
					],
				},
			],
		};

		vi.stubGlobal(
			"fetch",
			vi
				.fn()
				.mockResolvedValue(
					new Response(
						JSON.stringify(createPost({ id: "new-post-5", channel_id: "channel-1" })),
						{ status: 201, headers: { "Content-Type": "application/json" } },
					),
				),
		);

		await adapter.postMessage(threadId, { card });

		const fetchCall = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[0];
		const body = JSON.parse(fetchCall[1].body as string);

		expect(body.props.attachments[0].actions[0].integration.context).toEqual({
			action_id: "vote",
			action_value: "yes",
		});
	});
});

describe("MattermostAdapter actions - webhook handling", () => {
	it("processes button action callback via handleWebhook", async () => {
		const adapter = createAdapter(true) as MattermostAdapter & {
			chat: {
				processAction: ReturnType<typeof vi.fn>;
			};
			botUserId: string;
			users: Map<string, MattermostUser>;
		};
		const processAction = vi.fn().mockResolvedValue(undefined);
		adapter.chat = { processAction };
		adapter.botUserId = "bot-user";
		adapter.users.set("user-1", createUser());

		vi.stubGlobal(
			"fetch",
			vi.fn().mockResolvedValue(
				new Response(
					JSON.stringify(
						createPost({
							id: "post-1",
							channel_id: "channel-1",
							root_id: "root-1",
						}),
					),
					{ status: 200, headers: { "Content-Type": "application/json" } },
				),
			),
		);

		const response = await adapter.handleWebhook(
			new Request("https://example.com/webhook", {
				method: "POST",
				headers: { "Content-Type": "application/json" },
				body: JSON.stringify({
					user_id: "user-1",
					post_id: "post-1",
					channel_id: "channel-1",
					team_id: "team-1",
					context: {
						action_id: "approve",
					},
				}),
			}),
		);

		expect(response.status).toBe(200);
		expect(processAction).toHaveBeenCalledTimes(1);

		const event = processAction.mock.calls[0][0];
		expect(event.actionId).toBe("approve");
		expect(event.messageId).toBe("post-1");
		expect(event.user.userId).toBe("user-1");
		expect(event.value).toBeUndefined();
	});

	it("processes action with value from context", async () => {
		const adapter = createAdapter(true) as MattermostAdapter & {
			chat: {
				processAction: ReturnType<typeof vi.fn>;
			};
			botUserId: string;
			users: Map<string, MattermostUser>;
		};
		const processAction = vi.fn().mockResolvedValue(undefined);
		adapter.chat = { processAction };
		adapter.botUserId = "bot-user";
		adapter.users.set("user-1", createUser());

		vi.stubGlobal(
			"fetch",
			vi
				.fn()
				.mockResolvedValue(
					new Response(
						JSON.stringify(createPost({ id: "post-2", channel_id: "channel-1" })),
						{ status: 200, headers: { "Content-Type": "application/json" } },
					),
				),
		);

		await adapter.handleWebhook(
			new Request("https://example.com/webhook", {
				method: "POST",
				headers: { "Content-Type": "application/json" },
				body: JSON.stringify({
					user_id: "user-1",
					post_id: "post-2",
					channel_id: "channel-1",
					context: {
						action_id: "priority",
						action_value: "high",
					},
				}),
			}),
		);

		expect(adapter.chat.processAction).toHaveBeenCalledTimes(1);
		const event = adapter.chat.processAction.mock.calls[0][0];
		expect(event.actionId).toBe("priority");
		expect(event.value).toBe("high");
	});

	it("ignores webhook without action_id in context", async () => {
		const adapter = createAdapter(true) as MattermostAdapter & {
			chat: {
				processAction: ReturnType<typeof vi.fn>;
			};
		};
		const processAction = vi.fn();
		adapter.chat = { processAction };

		const response = await adapter.handleWebhook(
			new Request("https://example.com/webhook", {
				method: "POST",
				headers: { "Content-Type": "application/json" },
				body: JSON.stringify({
					user_id: "user-1",
					channel_id: "channel-1",
					context: {},
				}),
			}),
		);

		expect(response.status).toBe(200);
		expect(processAction).not.toHaveBeenCalled();
	});

	it("handles malformed webhook body gracefully", async () => {
		const adapter = createAdapter(true) as MattermostAdapter & {
			chat: {
				processAction: ReturnType<typeof vi.fn>;
			};
		};
		adapter.chat = { processAction: vi.fn() };

		const response = await adapter.handleWebhook(
			new Request("https://example.com/webhook", {
				method: "POST",
				body: "not json",
			}),
		);

		expect(response.status).toBe(200);
	});
});

describe("MattermostAdapter actions - edit with attachments", () => {
	it("preserves card attachments when editing", async () => {
		const adapter = createAdapter(true);
		const threadId = adapter.encodeThreadId({
			channelId: "channel-1",
			rootPostId: "root-1",
		});

		const existingPost = createPost({
			id: "post-10",
			channel_id: "channel-1",
			root_id: "root-1",
			props: { some_existing: "data" },
		});

		const card = {
			type: "card" as const,
			title: "Updated",
			children: [
				{
					type: "actions" as const,
					children: [
						{
							type: "button" as const,
							id: "done",
							label: "Done",
							style: "primary" as const,
						},
					],
				},
			],
		};

		let fetchCallIndex = 0;

		vi.stubGlobal(
			"fetch",
			vi.fn().mockImplementation(() => {
				fetchCallIndex += 1;

				if (fetchCallIndex === 1) {
					return Promise.resolve(
						new Response(JSON.stringify(existingPost), {
							status: 200,
							headers: { "Content-Type": "application/json" },
						}),
					);
				}

				return Promise.resolve(
					new Response(
						JSON.stringify({
							...existingPost,
							message: "Updated",
							props: { some_existing: "data", attachments: [{ title: "Updated" }] },
						}),
						{ status: 200, headers: { "Content-Type": "application/json" } },
					),
				);
			}),
		);

		const result = await adapter.editMessage(threadId, "post-10", { card });

		expect(result.id).toBe("post-10");

		const editCall = (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls[1];
		const body = JSON.parse(editCall[1].body as string);

		expect(body.props.some_existing).toBe("data");
		expect(body.props.attachments).toHaveLength(1);
		expect(body.props.attachments[0].title).toBe("Updated");
		expect(body.props.attachments[0].actions).toHaveLength(1);
		expect(body.props.attachments[0].actions[0].id).toBe("done");
	});
});
