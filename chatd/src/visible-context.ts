import type { FetchOptions, ThreadInfo, UserInfo } from "chat";

export type AddressingDocument = {
	botMentioned: boolean;
	otherPersonMentioned: boolean;
};

export type VisibleContextMessageDocument = {
	speaker: string;
	speakerHandle?: string;
	text: string;
	sentAt?: string;
	isBot?: boolean;
};

export type VisibleContextSenderDocument = {
	platform: string;
	senderID: string;
	handle?: string;
	email?: string;
	name?: string;
};

export type VisibleContextDocument = {
	messages: VisibleContextMessageDocument[];
	hasMoreBefore: boolean;
	historyCursor: string;
	sender?: VisibleContextSenderDocument;
	conversationType?: string;
	channelID?: string;
	channelName?: string;
};

type ContextMessage = {
	id: string;
	text: string;
	author: { userId: string; userName: string; fullName: string; isBot?: boolean | "unknown" };
	metadata: { dateSent: Date };
};

export type ContextCapableAdapter = {
	name: string;
	fetchMessages(
		threadId: string,
		options?: FetchOptions,
	): Promise<{ messages: ContextMessage[]; nextCursor?: string }>;
	fetchThread(threadId: string): Promise<ThreadInfo>;
	getUser(userId: string): Promise<UserInfo | null>;
};

export type NormalizedPlatformAdapter = ContextCapableAdapter & {
	historyScopeThreadId(threadId: string, messageId: string): string;
	addressingOf(raw: unknown): AddressingDocument;
};

export type HistoryCursorState = {
	threadId: string;
	cursor?: string;
};

export function encodeHistoryCursor(state: HistoryCursorState): string {
	return Buffer.from(JSON.stringify(state), "utf8").toString("base64url");
}

export function decodeHistoryCursor(historyCursor: string): HistoryCursorState {
	try {
		const decoded: unknown = JSON.parse(Buffer.from(historyCursor, "base64url").toString("utf8"));
		if (
			typeof decoded === "object" &&
			decoded !== null &&
			"threadId" in decoded &&
			typeof decoded.threadId === "string"
		) {
			const cursor = "cursor" in decoded && typeof decoded.cursor === "string" ? decoded.cursor : undefined;
			return { threadId: decoded.threadId, cursor };
		}
	} catch {
		return { threadId: historyCursor };
	}
	return { threadId: historyCursor };
}

const DEFAULT_HISTORY_LIMIT = 20;

export async function buildVisibleContext(
	adapter: ContextCapableAdapter,
	scopeThreadId: string,
	options: { beforeMessageId?: string; senderId?: string; cursor?: string; limit?: number } = {},
): Promise<VisibleContextDocument> {
	const limit = options.limit && options.limit > 0 ? options.limit : DEFAULT_HISTORY_LIMIT;
	const [fetchResult, threadInfo, senderInfo] = await Promise.all([
		adapter.fetchMessages(scopeThreadId, { cursor: options.cursor, limit: limit + 1, direction: "backward" }),
		adapter.fetchThread(scopeThreadId).catch(() => null),
		options.senderId ? adapter.getUser(options.senderId).catch(() => null) : Promise.resolve(null),
	]);
	let previousMessages = messagesBefore(fetchResult.messages, options.beforeMessageId);
	const hasMoreBefore = Boolean(fetchResult.nextCursor) || previousMessages.length > limit;
	if (previousMessages.length > limit) {
		previousMessages = previousMessages.slice(-limit);
	}
	return {
		messages: previousMessages.map(toVisibleContextMessage),
		hasMoreBefore,
		historyCursor: encodeHistoryCursor({ threadId: scopeThreadId, cursor: fetchResult.nextCursor }),
		sender: senderInfo ? toContextSender(adapter.name, senderInfo) : undefined,
		conversationType: threadInfo ? (threadInfo.isDM ? "direct" : "channel") : undefined,
		channelID: threadInfo?.channelId,
		channelName: threadInfo?.channelName,
	};
}

export function emptyVisibleContext(scopeThreadId: string): VisibleContextDocument {
	return {
		messages: [],
		hasMoreBefore: true,
		historyCursor: encodeHistoryCursor({ threadId: scopeThreadId }),
	};
}

function messagesBefore(messages: ContextMessage[], beforeMessageId?: string): ContextMessage[] {
	if (!beforeMessageId) return [...messages];
	const boundaryIndex = messages.findIndex((message) => message.id === beforeMessageId);
	if (boundaryIndex >= 0) return messages.slice(0, boundaryIndex);
	return messages.filter((message) => message.id !== beforeMessageId);
}

function toVisibleContextMessage(message: ContextMessage): VisibleContextMessageDocument {
	return {
		speaker: message.author.fullName || message.author.userName,
		speakerHandle: message.author.userName,
		text: message.text,
		sentAt: message.metadata.dateSent.toISOString(),
		isBot: message.author.isBot === true,
	};
}

function toContextSender(platform: string, user: UserInfo): VisibleContextSenderDocument {
	return {
		platform,
		senderID: user.userId,
		handle: user.userName,
		email: user.email,
		name: user.fullName || user.userName,
	};
}
