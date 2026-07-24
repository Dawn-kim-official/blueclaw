import {
	BaseFormatConverter,
	Message,
	parseMarkdown,
	stringifyMarkdown,
	type Adapter,
	type AdapterPostableMessage,
	type Author,
	type ChatInstance,
	type FetchOptions,
	type FetchResult,
	type RawMessage,
	type Root,
	type ThreadInfo,
	type UserInfo,
	type WebhookOptions,
} from "chat";
import { createBuzzRelayClient, type BuzzRelayClient } from "./relay-client.ts";
import {
	BUZZ_ADAPTER_NAME,
	firstTagValue,
	threadTagsOf,
	type BuzzAdapterConfig,
	type BuzzChannel,
	type BuzzEvent,
	type BuzzThreadId,
} from "./types.ts";

const STREAM_MESSAGE_KIND = 9;
const TYPING_INDICATOR_KIND = 20002;
const REACTION_KIND = 7;
const PROFILE_KIND = 0;
const GROUP_METADATA_KIND = 39000;
const GROUP_MEMBERS_KIND = 39002;

class BuzzFormatConverter extends BaseFormatConverter {
	toAst(platformText: string): Root {
		return parseMarkdown(platformText);
	}

	fromAst(ast: Root): string {
		return stringifyMarkdown(ast);
	}
}

export class BuzzAdapter implements Adapter<BuzzThreadId, BuzzEvent> {
	readonly name = BUZZ_ADAPTER_NAME;
	readonly userName: string;
	private readonly config: BuzzAdapterConfig;
	private readonly relay: BuzzRelayClient;
	private readonly converter = new BuzzFormatConverter();
	private chat: ChatInstance | null = null;
	private channelsById = new Map<string, BuzzChannel>();
	private subscribedChannelIds = new Set<string>();
	private profileByPubkey = new Map<string, { name?: string; nip05?: string; picture?: string }>();

	constructor(config: BuzzAdapterConfig) {
		this.config = config;
		this.userName = config.botDisplayName;
		this.relay = createBuzzRelayClient(config.relayURL, config.privateKeyHex, config.authTagJSON);
	}

	renderFormatted(content: Root): string {
		return this.converter.fromAst(content);
	}

	get botPubkey(): string {
		return this.relay.pubkeyHex;
	}

	channelIdFromThreadId(threadId: string): string {
		return this.decodeThreadId(threadId).channelId;
	}

	historyThreadId(threadId: string): string {
		return this.encodeThreadId({ channelId: this.decodeThreadId(threadId).channelId });
	}

	encodeThreadId(data: BuzzThreadId): string {
		if (!data.rootEventId) return `${BUZZ_ADAPTER_NAME}:${data.channelId}`;
		return `${BUZZ_ADAPTER_NAME}:${data.channelId}:${data.rootEventId}`;
	}

	decodeThreadId(threadId: string): BuzzThreadId {
		const parts = threadId.split(":");
		const channelId = parts[1];
		if (parts[0] !== BUZZ_ADAPTER_NAME || !channelId) {
			throw new Error(`invalid buzz thread id: ${threadId}`);
		}
		return { channelId, rootEventId: parts[2] || undefined };
	}

	async initialize(chat: ChatInstance): Promise<void> {
		this.chat = chat;
		await this.relay.connect();
		await this.refreshChannels();
		this.subscribeToChannels();
	}

	async disconnect(): Promise<void> {
		this.relay.disconnect();
	}

	async handleWebhook(_request: Request, _options?: WebhookOptions): Promise<Response> {
		return new Response("OK", { status: 200 });
	}

	private async refreshChannels(): Promise<void> {
		const memberships = await this.relay.query({
			kinds: [GROUP_MEMBERS_KIND],
			"#p": [this.relay.pubkeyHex],
		});
		const channelIds = [
			...new Set(memberships.map((event) => firstTagValue(event, "d")).filter((id): id is string => Boolean(id))),
		];
		if (channelIds.length === 0) return;
		const metadataEvents = await this.relay.query({ kinds: [GROUP_METADATA_KIND], "#d": channelIds });
		const latestMetadata = new Map<string, BuzzEvent>();
		for (const event of metadataEvents) {
			const channelId = firstTagValue(event, "d");
			if (!channelId) continue;
			const known = latestMetadata.get(channelId);
			if (!known || event.created_at > known.created_at) latestMetadata.set(channelId, event);
		}
		for (const channelId of channelIds) {
			const metadata = latestMetadata.get(channelId);
			this.channelsById.set(channelId, {
				channelId,
				name: metadata ? (firstTagValue(metadata, "name") ?? "") : "",
				isDM: metadata ? firstTagValue(metadata, "t") === "dm" : false,
			});
		}
	}

	private subscribeToChannels(): void {
		for (const channelId of this.channelsById.keys()) {
			if (this.subscribedChannelIds.has(channelId)) continue;
			this.subscribedChannelIds.add(channelId);
			this.relay.subscribe(
				[{ kinds: [STREAM_MESSAGE_KIND], "#h": [channelId], since: Math.floor(Date.now() / 1000) }],
				(event) => {
					void this.dispatchIncomingEvent(event);
				},
			);
		}
	}

	private async dispatchIncomingEvent(event: BuzzEvent): Promise<void> {
		if (!this.chat || event.pubkey === this.relay.pubkeyHex) return;
		const channelId = firstTagValue(event, "h");
		if (!channelId) return;
		if (!this.channelsById.has(channelId)) {
			await this.refreshChannels();
			this.subscribeToChannels();
		}
		const threadId = this.threadIdForEvent(event);
		await this.chat.processMessage(this, threadId, async () => await this.messageFromEvent(event));
	}

	private threadIdForEvent(event: BuzzEvent): string {
		const channelId = firstTagValue(event, "h") ?? "";
		const { rootEventId } = threadTagsOf(event);
		return this.encodeThreadId({ channelId, rootEventId: rootEventId ?? event.id });
	}

	parseMessage(raw: BuzzEvent): Message<BuzzEvent> {
		const cachedProfile = this.profileByPubkey.get(raw.pubkey);
		return this.buildMessage(raw, cachedProfile);
	}

	private async messageFromEvent(event: BuzzEvent): Promise<Message<BuzzEvent>> {
		return this.buildMessage(event, await this.fetchProfile(event.pubkey));
	}

	private buildMessage(
		event: BuzzEvent,
		profile: { name?: string; nip05?: string } | undefined,
	): Message<BuzzEvent> {
		return new Message({
			id: event.id,
			threadId: this.threadIdForEvent(event),
			text: event.content,
			formatted: this.converter.toAst(event.content),
			raw: event,
			author: this.authorForPubkey(event.pubkey, profile),
			metadata: { dateSent: new Date(event.created_at * 1000), edited: false },
			attachments: [],
		});
	}

	private authorForPubkey(pubkey: string, profile?: { name?: string; nip05?: string }): Author {
		const displayName = profile?.name?.trim() || `npub…${pubkey.slice(-6)}`;
		return {
			userId: pubkey,
			userName: displayName,
			fullName: displayName,
			isBot: pubkey === this.relay.pubkeyHex,
			isMe: pubkey === this.relay.pubkeyHex,
		};
	}

	private async fetchProfile(pubkey: string): Promise<{ name?: string; nip05?: string; picture?: string }> {
		const cached = this.profileByPubkey.get(pubkey);
		if (cached) return cached;
		const events = await this.relay.query({ kinds: [PROFILE_KIND], authors: [pubkey], limit: 1 });
		let profile: { name?: string; nip05?: string; picture?: string } = {};
		const content = events.at(-1)?.content;
		if (content) {
			try {
				const parsed = JSON.parse(content) as Record<string, unknown>;
				profile = {
					name: typeof parsed.display_name === "string" && parsed.display_name.trim() !== ""
						? parsed.display_name
						: typeof parsed.name === "string"
							? parsed.name
							: undefined,
					nip05: typeof parsed.nip05 === "string" ? parsed.nip05 : undefined,
					picture: typeof parsed.picture === "string" ? parsed.picture : undefined,
				};
			} catch {
				profile = {};
			}
		}
		this.profileByPubkey.set(pubkey, profile);
		return profile;
	}

	async postMessage(threadId: string, message: AdapterPostableMessage): Promise<RawMessage<BuzzEvent>> {
		const decoded = this.decodeThreadId(threadId);
		const text = this.converter.renderPostable(message);
		const tags: string[][] = [["h", decoded.channelId]];
		if (decoded.rootEventId) {
			tags.push(["e", decoded.rootEventId, "", "root"]);
		}
		const event = await this.relay.publish(STREAM_MESSAGE_KIND, text, tags);
		return { id: event.id, threadId, raw: event };
	}

	async postChannelMessage(channelId: string, message: AdapterPostableMessage): Promise<RawMessage<BuzzEvent>> {
		return this.postMessage(this.encodeThreadId({ channelId }), message);
	}

	async editMessage(): Promise<RawMessage<BuzzEvent>> {
		throw new Error("buzz message edits are not supported");
	}

	async deleteMessage(): Promise<void> {
		throw new Error("buzz message deletion is not supported");
	}

	async addReaction(threadId: string, messageId: string, emoji: string): Promise<void> {
		const tags: string[][] = [["e", messageId]];
		if (threadId) {
			tags.push(["h", this.decodeThreadId(threadId).channelId]);
		}
		await this.relay.publish(REACTION_KIND, String(emoji), tags);
	}

	async removeReaction(): Promise<void> {}

	async startTyping(threadId: string, _status?: string): Promise<void> {
		const decoded = this.decodeThreadId(threadId);
		const tags: string[][] = [["h", decoded.channelId]];
		if (decoded.rootEventId) tags.push(["e", decoded.rootEventId, "", "root"]);
		await this.relay.publish(TYPING_INDICATOR_KIND, "", tags).catch(() => void 0);
	}

	async fetchMessages(threadId: string, options?: FetchOptions): Promise<FetchResult<BuzzEvent>> {
		const decoded = this.decodeThreadId(threadId);
		const limit = options?.limit && options.limit > 0 ? options.limit : 20;
		const events = await this.relay.query({
			kinds: [STREAM_MESSAGE_KIND],
			"#h": [decoded.channelId],
			limit: Math.max(limit * 3, limit),
		});
		const chronological = events.sort((first, second) => first.created_at - second.created_at);
		const relevant = decoded.rootEventId
			? chronological.filter((event) => {
					const { rootEventId } = threadTagsOf(event);
					return event.id === decoded.rootEventId || rootEventId === decoded.rootEventId;
				})
			: chronological;
		const window = relevant.slice(-limit);
		const messages: Message<BuzzEvent>[] = [];
		for (const event of window) {
			messages.push(this.buildMessage(event, await this.fetchProfile(event.pubkey)));
		}
		return { messages, nextCursor: undefined };
	}

	async fetchThread(threadId: string): Promise<ThreadInfo> {
		const decoded = this.decodeThreadId(threadId);
		const channel = this.channelsById.get(decoded.channelId);
		return {
			id: threadId,
			channelId: decoded.channelId,
			channelName: channel?.name,
			isDM: channel?.isDM ?? false,
			metadata: {},
		};
	}

	isDM(threadId: string): boolean {
		const decoded = this.decodeThreadId(threadId);
		return this.channelsById.get(decoded.channelId)?.isDM ?? false;
	}

	async getUser(userId: string): Promise<UserInfo | null> {
		const profile = await this.fetchProfile(userId);
		const linkedEmail = await this.linkedAccountEmail(userId);
		return {
			userId,
			userName: profile.name ?? userId.slice(0, 8),
			fullName: profile.name ?? userId.slice(0, 8),
			email: linkedEmail ?? profile.nip05,
			isBot: userId === this.relay.pubkeyHex,
		};
	}

	private async linkedAccountEmail(pubkey: string): Promise<string | undefined> {
		const linksPath = this.config.accountLinksPath;
		if (!linksPath) return undefined;
		const linksFile = Bun.file(linksPath);
		if (!(await linksFile.exists())) return undefined;
		try {
			const links = (await linksFile.json()) as Record<string, unknown>;
			const email = links[pubkey.toLowerCase()];
			return typeof email === "string" && email.trim() !== "" ? email.trim().toLowerCase() : undefined;
		} catch {
			return undefined;
		}
	}
}
