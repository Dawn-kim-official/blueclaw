export interface ReplyAttachmentDocument {
	devicePath?: string;
	filename?: string;
	contentType?: string;
	sizeBytes?: number;
	title?: string;
	contentBase64?: string;
}

export interface AskChoiceOptionDocument {
	key: string;
	label: string;
	shortLabel?: string;
	value?: string;
}

export interface AskInteractionDocument {
	interactionID?: string;
	taskRunID?: string;
	kind?: string;
	message?: string;
	question?: string;
	options?: AskChoiceOptionDocument[];
	recommendedOptionKey?: string;
	selectionMode?: string;
	responseLanguage?: string;
	targetPlatformUserID?: string;
}

export interface ReplySendRequest {
	replyTargetID: string;
	message: string;
	taskRunID?: string;
	replyKind?: string;
	rawEventID?: string;
	outboxID?: string;
	attachments?: ReplyAttachmentDocument[];
	interaction?: AskInteractionDocument;
}

export interface ReplySendResponse {
	dispatchID: string;
}

export interface ProgressRequest {
	replyTargetID: string;
}

export interface ReactionRequest {
	conversationID?: string;
	messageID: string;
	emojiName: string;
	reason?: string;
}

export interface HistoryFetchRequest {
	historyCursor: string;
	limit?: number;
	direction?: string;
}

export interface VisibleContextMessageDocument {
	speaker: string;
	speakerHandle?: string;
	text: string;
	sentAt?: string;
	isBot?: boolean;
}

export interface HistoryFetchResponse {
	messages: VisibleContextMessageDocument[];
	hasMoreBefore: boolean;
	historyCursor: string;
	channelID?: string;
	channelName?: string;
	conversationType?: string;
}

export interface ChannelEnsureRequest {
	name: string;
	displayName?: string;
	description?: string;
	topic?: string;
}

export interface ChannelEnsureResponse {
	channelID: string;
	replyTargetID: string;
	created: boolean;
}

export interface DirectMessageSendRequest {
	userSecretHex: string;
	message: string;
}

export interface DirectMessageSendResponse {
	channelID: string;
	replyTargetID: string;
	messageID: string;
}

export interface DirectMessageEnsureRequest {
	userSecretHex: string;
}

export interface DirectMessageEnsureResponse {
	channelID: string;
	replyTargetID: string;
	historyCursor: string;
}

export interface MessageEditRequest {
	replyTargetID: string;
	messageID: string;
	message: string;
}

export interface MessageDeleteRequest {
	replyTargetID: string;
	messageID: string;
}

export interface InteractionResolveRequest {
	dispatchID: string;
}

export interface InputAttachmentDocument {
	platform?: string;
	fileID?: string;
	messageID?: string;
	filename?: string;
	contentType?: string;
	sizeBytes?: number;
	path?: string;
	isAvailable?: boolean;
	errorCode?: string;
	message?: string;
}

export interface AttachmentImportRequest {
	messageID: string;
	targetDirectoryPath: string;
	inputAttachments: InputAttachmentDocument[];
}

export interface AgentPartSourceDocument {
	platform?: string;
	messageID?: string;
	fileID?: string;
}

export interface AgentFilePartDocument {
	path: string;
	filename?: string;
	contentType?: string;
	sizeBytes?: number;
}

export interface AgentImagePartDocument {
	path: string;
	filename?: string;
	mimeType?: string;
}

export interface AgentPartDocument {
	type: "file" | "image";
	file?: AgentFilePartDocument;
	image?: AgentImagePartDocument;
	source?: AgentPartSourceDocument;
}

export interface AttachmentImportResponse {
	inputParts: AgentPartDocument[];
	inputAttachments: InputAttachmentDocument[];
}

export interface IdentityResolveRequest {
	senderID: string;
}

export interface IdentityResolveResponse {
	displayName?: string;
	email?: string;
}
