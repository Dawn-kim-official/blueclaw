import { afterEach, describe, expect, it, mock } from "bun:test";
import { MattermostAdapter } from "../src/adapters/mattermost/adapter.ts";
import type {
	MattermostChannel,
	MattermostPost,
	MattermostUser,
	MattermostWebSocketEvent,
} from "../src/adapters/mattermost/types.ts";

function adapterInternals<TInternals>(adapter: MattermostAdapter): TInternals {
	return adapter as unknown as TInternals;
}

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

const originalFetch = globalThis.fetch;

afterEach(() => {
	globalThis.fetch = originalFetch;
	mock.restore();
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
		const adapter = createAdapter();
		const processMessage = mock(
			(
				_adapter: unknown,
				_threadId: string,
				_messageFactory: () => Promise<{ isMention?: boolean }>,
			) => undefined,
		);
		const internals = adapterInternals<{
			chat: { processMessage: typeof processMessage };
			botUserId: string;
			users: Map<string, MattermostUser>;
			handlePostedEvent(payload: MattermostWebSocketEvent): Promise<void>;
		}>(adapter);
		internals.chat = { processMessage };
		internals.botUserId = "bot-user";
		internals.users.set("user-1", createUser());

		await internals.handlePostedEvent({
			data: {
				post: JSON.stringify(createPost()),
				mentions: JSON.stringify(["bot-user"]),
			},
		});

		expect(processMessage).toHaveBeenCalledTimes(1);
		const callArray = processMessage.mock.calls[0];
		if (!callArray) throw new Error("Expected processMessage to be called");
		const messageFactory = callArray[2];
		const message = await messageFactory();

		expect(message.isMention).toBe(true);
	});

	it("accepts websocket embedded objects for edited posts", async () => {
		const adapter = createAdapter();
		const processMessage = mock(() => undefined);
		const internals = adapterInternals<{
			chat: { processMessage: typeof processMessage };
			users: Map<string, MattermostUser>;
			handleWebSocketPayload(payload: MattermostWebSocketEvent): Promise<void>;
		}>(adapter);
		internals.chat = { processMessage };
		internals.users.set("user-1", createUser());

		await internals.handleWebSocketPayload({
			event: "post_edited",
			data: {
				post: createPost({ message: "edited" }),
			},
		});

		expect(processMessage).toHaveBeenCalledTimes(1);
	});

	it("parses binary websocket payloads", () => {
		const adapter = createAdapter();
		const internals = adapterInternals<{
			parseWebSocketPayload(data: unknown): MattermostWebSocketEvent | null;
		}>(adapter);
		const payload = { event: "posted", data: { post: JSON.stringify(createPost()) } };
		const binary = new TextEncoder().encode(JSON.stringify(payload));

		expect(internals.parseWebSocketPayload(binary)).toEqual(payload);
	});

	it("returns null when fetchMessage gets a 404", async () => {
		const adapter = createAdapter();
		globalThis.fetch = mock(async () =>
			new Response(JSON.stringify({ message: "missing" }), {
				status: 404,
				headers: { "Content-Type": "application/json" },
			}),
		) as never;

		const result = await adapter.fetchMessage(
			adapter.encodeThreadId({ channelId: "channel-1", rootPostId: "root-1" }),
			"missing",
		);

		expect(result).toBeNull();
	});

	it("bounds user and channel caches", () => {
		const adapter = createAdapter();
		const internals = adapterInternals<{
			users: Map<string, MattermostUser>;
			channels: Map<string, MattermostChannel>;
			setCachedValue<TKey, TValue>(
				cache: Map<TKey, TValue>,
				key: TKey,
				value: TValue,
				maxSize: number,
			): void;
		}>(adapter);

		for (let index = 0; index < 3; index += 1) {
			internals.setCachedValue(
				internals.users,
				`user-${index}`,
				createUser({ id: `user-${index}`, username: `user${index}` }),
				2,
			);
			internals.setCachedValue(
				internals.channels,
				`channel-${index}`,
				{ id: `channel-${index}`, name: `channel-${index}`, type: "O" },
				2,
			);
		}

		expect(internals.users.size).toBe(2);
		expect(internals.channels.size).toBe(2);
		expect(internals.users.has("user-0")).toBe(false);
		expect(internals.channels.has("channel-0")).toBe(false);
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

		globalThis.fetch = mock(async () =>
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
		) as never;

		const result = await adapter.postMessage(threadId, { card });
		expect(result.id).toBe("new-post-1");

		const fetchMock = globalThis.fetch as any;
		const fetchCall = fetchMock.mock.calls[0];
		if (!fetchCall) throw new Error("Expected fetch to be called");
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

		globalThis.fetch = mock(async () =>
			new Response(
				JSON.stringify(createPost({ id: "new-post-2", channel_id: "channel-1" })),
				{ status: 201, headers: { "Content-Type": "application/json" } },
			),
		) as never;

		await adapter.postMessage(threadId, { card });

		const fetchMock = globalThis.fetch as any;
		const fetchCall = fetchMock.mock.calls[0];
		if (!fetchCall) throw new Error("Expected fetch to be called");
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

		globalThis.fetch = mock(async () =>
			new Response(
				JSON.stringify(createPost({ id: "new-post-3", channel_id: "channel-1" })),
				{ status: 201, headers: { "Content-Type": "application/json" } },
			),
		) as never;

		await adapter.postMessage(threadId, { card });

		const fetchMock = globalThis.fetch as any;
		const fetchCall = fetchMock.mock.calls[0];
		if (!fetchCall) throw new Error("Expected fetch to be called");
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

		globalThis.fetch = mock(async () =>
			new Response(
				JSON.stringify(createPost({ id: "new-post-4", channel_id: "channel-1" })),
				{ status: 201, headers: { "Content-Type": "application/json" } },
			),
		) as never;

		await adapter.postMessage(threadId, { card });

		const fetchMock = globalThis.fetch as any;
		const fetchCall = fetchMock.mock.calls[0];
		if (!fetchCall) throw new Error("Expected fetch to be called");
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

		globalThis.fetch = mock(async () =>
			new Response(
				JSON.stringify(createPost({ id: "new-post-5", channel_id: "channel-1" })),
				{ status: 201, headers: { "Content-Type": "application/json" } },
			),
		) as never;

		await adapter.postMessage(threadId, { card });

		const fetchMock = globalThis.fetch as any;
		const fetchCall = fetchMock.mock.calls[0];
		if (!fetchCall) throw new Error("Expected fetch to be called");
		const body = JSON.parse(fetchCall[1].body as string);

		expect(body.props.attachments[0].actions[0].integration.context).toEqual({
			action_id: "vote",
			action_value: "yes",
		});
	});
});

describe("MattermostAdapter actions - webhook handling", () => {
	it("processes button action callback via handleWebhook", async () => {
		const adapter = createAdapter(true);
		const processAction = mock(
			async (_event: {
				actionId: string;
				messageId: string;
				user: { userId: string };
				value?: string;
				threadId: string;
			}) => undefined,
		);
		const internals = adapterInternals<{
			chat: { processAction: typeof processAction };
			botUserId: string;
			users: Map<string, MattermostUser>;
		}>(adapter);
		internals.chat = { processAction };
		internals.botUserId = "bot-user";
		internals.users.set("user-1", createUser());

		globalThis.fetch = mock(async () =>
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
		) as never;

		const backgroundTasks: Promise<unknown>[] = [];
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
			{
				waitUntil: (task) => {
					backgroundTasks.push(task);
				},
			},
		);
		await Promise.all(backgroundTasks);

		expect(response.status).toBe(200);
		expect(processAction).toHaveBeenCalledTimes(1);

		const callArray = processAction.mock.calls[0];
		if (!callArray) throw new Error("Expected processAction to be called");
		const event = callArray[0];
		expect(event.actionId).toBe("approve");
		expect(event.messageId).toBe("post-1");
		expect(event.user.userId).toBe("user-1");
		expect(event.value).toBeUndefined();
	});

	it("processes action with value from context", async () => {
		const adapter = createAdapter(true);
		const processAction = mock(
			async (_event: {
				actionId: string;
				messageId: string;
				user: { userId: string };
				value?: string;
				threadId: string;
			}) => undefined,
		);
		const internals = adapterInternals<{
			chat: { processAction: typeof processAction };
			botUserId: string;
			users: Map<string, MattermostUser>;
		}>(adapter);
		internals.chat = { processAction };
		internals.botUserId = "bot-user";
		internals.users.set("user-1", createUser());

		globalThis.fetch = mock(async () =>
			new Response(
				JSON.stringify(createPost({ id: "post-2", channel_id: "channel-1" })),
				{ status: 200, headers: { "Content-Type": "application/json" } },
			),
		) as never;

		const backgroundTasks: Promise<unknown>[] = [];
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
			{
				waitUntil: (task) => {
					backgroundTasks.push(task);
				},
			},
		);
		await Promise.all(backgroundTasks);

		expect(internals.chat.processAction).toHaveBeenCalledTimes(1);
		const callArray = internals.chat.processAction.mock.calls[0];
		if (!callArray) throw new Error("Expected processAction to be called");
		const event = callArray[0];
		expect(event.actionId).toBe("priority");
		expect(event.value).toBe("high");
	});

	it("ignores webhook without action_id in context", async () => {
		const adapter = createAdapter(true);
		const processAction = mock(() => undefined);
		const internals = adapterInternals<{
			chat: { processAction: typeof processAction };
		}>(adapter);
		internals.chat = { processAction };

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
		const adapter = createAdapter(true);
		const internals = adapterInternals<{
			chat: { processAction: ReturnType<typeof mock> };
		}>(adapter);
		internals.chat = { processAction: mock(() => undefined) };

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

		globalThis.fetch = mock(() => {
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
		}) as never;

		const result = await adapter.editMessage(threadId, "post-10", { card });

		expect(result.id).toBe("post-10");

		const fetchMock = globalThis.fetch as any;
		const editCall = fetchMock.mock.calls[1];
		if (!editCall) throw new Error("Expected second fetch call");
		const body = JSON.parse(editCall[1].body as string);

		expect(body.props.some_existing).toBe("data");
		expect(body.props.attachments).toHaveLength(1);
		expect(body.props.attachments[0].title).toBe("Updated");
		expect(body.props.attachments[0].actions).toHaveLength(1);
		expect(body.props.attachments[0].actions[0].id).toBe("done");
	});
});
