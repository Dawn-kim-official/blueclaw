import type { AdapterPostableMessage, CardElement, FileUpload } from "chat";
import { BuzzAdapter } from "./adapters/buzz/adapter.ts";
import type { MattermostAdapter } from "./adapters/mattermost/adapter.ts";
import type { ChatdConfiguration } from "./configuration.ts";
import { importAttachmentToDirectory } from "./outbound-attachments.ts";
import { buildVisibleContext, decodeHistoryCursor } from "./visible-context.ts";
import {
	parseAttachmentImportRequest,
	parseHistoryFetchRequest,
	parseIdentityResolveRequest,
	parseInteractionResolveRequest,
	parseProgressRequest,
	parseReactionRequest,
	parseReplySendRequest,
} from "./outbound-parse.ts";
import type {
	AgentPartDocument,
	AttachmentImportResponse,
	HistoryFetchResponse,
	IdentityResolveResponse,
	InputAttachmentDocument,
	ReplyAttachmentDocument,
	ReplySendRequest,
	ReplySendResponse,
} from "./outbound-types.ts";

type PlatformChatAdapter = MattermostAdapter | BuzzAdapter;

type CapabilityHandler = (
	adapter: PlatformChatAdapter,
	configuration: ChatdConfiguration,
	requestDocument: unknown,
) => Promise<object>;

const OUTBOUND_ROUTE_PATTERN = /^\/v1\/platform\/([^/]+)\/([^/]+)$/;

const capabilityHandlers: Record<string, CapabilityHandler> = {
	"reply.send": handleReplySend,
	"progress.start": handleProgressStart,
	"progress.stop": handleProgressStop,
	"reaction.add": handleReactionAdd,
	"reaction.remove": handleReactionRemove,
	"history.fetch": handleHistoryFetch,
	"interaction.resolve": handleInteractionResolve,
	"attachments.import": handleAttachmentsImport,
	"identity.resolve": handleIdentityResolve,
};

export function createOutboundHandler(
	adapters: Record<string, PlatformChatAdapter>,
	configuration: ChatdConfiguration,
): (request: Request) => Promise<Response> {
	return async function handleOutboundRequest(request: Request): Promise<Response> {
		if (request.method !== "POST") {
			return jsonResponse(405, { error: "method not allowed" });
		}

		const routeMatch = OUTBOUND_ROUTE_PATTERN.exec(new URL(request.url).pathname);
		if (!routeMatch) {
			return jsonResponse(404, { error: "not found" });
		}

		const [, platform, capabilityName] = routeMatch;
		const adapter = platform ? adapters[platform] : undefined;
		if (!adapter || !capabilityName) {
			return jsonResponse(404, { error: `unknown platform ${platform}` });
		}

		const handler = capabilityHandlers[capabilityName];
		if (!handler) {
			return jsonResponse(404, { error: `unknown capability ${capabilityName}` });
		}

		try {
			const requestDocument = await request.json();
			const responseDocument = await handler(adapter, configuration, requestDocument);
			return jsonResponse(200, responseDocument);
		} catch (error) {
			return jsonResponse(502, { error: error instanceof Error ? error.message : String(error) });
		}
	};
}

function jsonResponse(statusCode: number, body: object): Response {
	return new Response(JSON.stringify(body), {
		status: statusCode,
		headers: { "Content-Type": "application/json" },
	});
}

async function handleReplySend(
	adapter: PlatformChatAdapter,
	_configuration: ChatdConfiguration,
	requestBody: unknown,
): Promise<ReplySendResponse> {
	const requestDocument = parseReplySendRequest(requestBody);
	const fileUploads = await buildFileUploads(requestDocument.attachments ?? []);
	const message = buildPostableMessage(requestDocument, fileUploads);
	const result = await adapter.postMessage(requestDocument.replyTargetID, message);
	return { dispatchID: result.id };
}

function buildPostableMessage(
	requestDocument: ReplySendRequest,
	fileUploads: FileUpload[],
): AdapterPostableMessage {
	const interactionOptions = requestDocument.interaction?.options ?? [];
	if (interactionOptions.length > 0) {
		return { card: buildInteractionCard(requestDocument), files: fileUploads };
	}
	if (fileUploads.length > 0) {
		return { markdown: requestDocument.message, files: fileUploads };
	}
	return requestDocument.message;
}

function buildInteractionCard(requestDocument: ReplySendRequest): CardElement {
	const interaction = requestDocument.interaction;
	const options = interaction?.options ?? [];
	const introductionText = requestDocument.message.trim();
	const children: CardElement["children"] = [];
	if (introductionText) {
		children.push({ type: "text", content: introductionText });
	}
	children.push({
		type: "actions",
		children: options.map((option) => ({
			type: "button",
			id: option.key,
			label: option.label,
			value: option.value,
		})),
	});
	return {
		type: "card",
		title: interaction?.question || interaction?.message || undefined,
		children,
	};
}

async function buildFileUploads(attachments: ReplyAttachmentDocument[]): Promise<FileUpload[]> {
	const fileUploads: FileUpload[] = [];
	for (const attachment of attachments) {
		const fileBytes = await readAttachmentBytes(attachment);
		if (!fileBytes) {
			continue;
		}
		fileUploads.push({
			data: fileBytes,
			filename: attachment.filename?.trim() || "attachment",
			mimeType: attachment.contentType,
		});
	}
	return fileUploads;
}

async function readAttachmentBytes(attachment: ReplyAttachmentDocument): Promise<Buffer | null> {
	if (attachment.contentBase64) {
		return Buffer.from(attachment.contentBase64, "base64");
	}
	if (attachment.devicePath) {
		const file = Bun.file(attachment.devicePath);
		if (await file.exists()) {
			return Buffer.from(await file.arrayBuffer());
		}
	}
	return null;
}

async function handleProgressStart(
	adapter: PlatformChatAdapter,
	_configuration: ChatdConfiguration,
	requestBody: unknown,
): Promise<Record<string, never>> {
	const requestDocument = parseProgressRequest(requestBody);
	await adapter.startTyping(requestDocument.replyTargetID);
	return {};
}

async function handleProgressStop(): Promise<Record<string, never>> {
	return {};
}

async function handleReactionAdd(
	adapter: PlatformChatAdapter,
	_configuration: ChatdConfiguration,
	requestBody: unknown,
): Promise<Record<string, never>> {
	const requestDocument = parseReactionRequest(requestBody);
	await adapter.addReaction("", requestDocument.messageID, requestDocument.emojiName);
	return {};
}

async function handleReactionRemove(
	adapter: PlatformChatAdapter,
	_configuration: ChatdConfiguration,
	requestBody: unknown,
): Promise<Record<string, never>> {
	const requestDocument = parseReactionRequest(requestBody);
	await adapter.removeReaction("", requestDocument.messageID, requestDocument.emojiName);
	return {};
}

async function handleHistoryFetch(
	adapter: PlatformChatAdapter,
	_configuration: ChatdConfiguration,
	requestBody: unknown,
): Promise<HistoryFetchResponse> {
	const requestDocument = parseHistoryFetchRequest(requestBody);
	const { threadId, cursor } = decodeHistoryCursor(requestDocument.historyCursor);
	const context = await buildVisibleContext(adapter, threadId, { cursor, limit: requestDocument.limit });
	return {
		messages: context.messages,
		hasMoreBefore: context.hasMoreBefore,
		historyCursor: context.historyCursor,
		channelID: context.channelID,
		channelName: context.channelName,
		conversationType: context.conversationType,
	};
}

async function handleInteractionResolve(
	adapter: PlatformChatAdapter,
	_configuration: ChatdConfiguration,
	requestBody: unknown,
): Promise<Record<string, never>> {
	const requestDocument = parseInteractionResolveRequest(requestBody);
	if (adapter instanceof BuzzAdapter || !adapter.fetchMessage) {
		return {};
	}
	const existingMessage = await adapter.fetchMessage("", requestDocument.dispatchID);
	if (!existingMessage) {
		return {};
	}

	const threadId = adapter.encodeThreadId({
		channelId: existingMessage.raw.channel_id,
		rootPostId: existingMessage.raw.root_id || undefined,
	});
	const frozenCard: CardElement = {
		type: "card",
		children: existingMessage.text ? [{ type: "text", content: existingMessage.text }] : [],
	};
	await adapter.editMessage(threadId, requestDocument.dispatchID, { card: frozenCard });
	return {};
}

async function handleAttachmentsImport(
	adapter: PlatformChatAdapter,
	configuration: ChatdConfiguration,
	requestBody: unknown,
): Promise<AttachmentImportResponse> {
	const requestDocument = parseAttachmentImportRequest(requestBody);
	if (adapter instanceof BuzzAdapter) {
		return { inputParts: [], inputAttachments: [] };
	}
	const importedAttachments = await Promise.all(
		requestDocument.inputAttachments.map((attachment) =>
			importAttachmentToDirectory(configuration, requestDocument.targetDirectoryPath, attachment),
		),
	);

	return {
		inputAttachments: importedAttachments,
		inputParts: importedAttachments
			.filter((attachment) => attachment.isAvailable && attachment.path)
			.map((attachment) => agentPartForAttachment(attachment, requestDocument.messageID)),
	};
}

function agentPartForAttachment(attachment: InputAttachmentDocument, messageID: string): AgentPartDocument {
	const isImage = (attachment.contentType ?? "").startsWith("image/");
	const source = { platform: "mattermost", messageID, fileID: attachment.fileID };
	if (isImage) {
		return {
			type: "image",
			image: { path: attachment.path ?? "", filename: attachment.filename, mimeType: attachment.contentType },
			source,
		};
	}
	return {
		type: "file",
		file: {
			path: attachment.path ?? "",
			filename: attachment.filename,
			contentType: attachment.contentType,
			sizeBytes: attachment.sizeBytes,
		},
		source,
	};
}

async function handleIdentityResolve(
	adapter: PlatformChatAdapter,
	_configuration: ChatdConfiguration,
	requestBody: unknown,
): Promise<IdentityResolveResponse> {
	const requestDocument = parseIdentityResolveRequest(requestBody);
	if (!adapter.getUser) {
		return {};
	}
	const user = await adapter.getUser(requestDocument.senderID);
	if (!user) {
		return {};
	}
	return { displayName: user.fullName, email: user.email };
}
