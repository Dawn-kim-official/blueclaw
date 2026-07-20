import type { Logger } from "chat";

export interface MattermostAdapterConfig {
	/** Mattermost server base URL, e.g. https://mattermost.example.com */
	baseUrl: string;
	/** Bot access token used for REST API and websocket authentication */
	botToken: string;
	/**
	 * Public URL where Mattermost can send interactive action callbacks.
	 * This is the URL of the webhook route in your HTTP server (e.g. https://my-bot.example.com/webhooks/mattermost),
	 * not the Mattermost server URL. Required for interactive buttons and selects.
	 */
	callbackUrl?: string;
	/** Optional logger used before Chat injects its own logger */
	logger?: Logger;
	/** Optional bot username override until /users/me resolves */
	userName?: string;
	/** Websocket connection behavior */
	websocket?: {
		/** Connect the websocket automatically during initialize. Defaults to true. */
		enabled?: boolean;
		/** Initial reconnect delay in milliseconds. Defaults to 1000. */
		reconnectDelayMs?: number;
		/** Maximum reconnect delay in milliseconds. Defaults to 30000. */
		maxReconnectDelayMs?: number;
	};
}

/** Decoded Mattermost thread identifier. */
export interface MattermostThreadId {
	/** Mattermost channel ID */
	channelId: string;
	/** Root post ID for threaded replies. Omitted for channel-level posting contexts. */
	rootPostId?: string;
}

export interface MattermostMessageMetadata {
	/** Mattermost post type, e.g. empty string or system_* values */
	type: string;
	/** Adapter-level thread identifier for the message */
	mattermostThreadId: MattermostThreadId;
}

export type MattermostChannelType = "O" | "P" | "D" | "G";

export interface MattermostFileInfo {
	id: string;
	name: string;
	extension?: string;
	mime_type?: string;
	size?: number;
	width?: number;
	height?: number;
	has_preview_image?: boolean;
	post_id?: string;
	user_id?: string;
}

/** Raw Mattermost post payload returned by REST API and websocket events */
export interface MattermostPost {
	id: string;
	channel_id: string;
	user_id: string;
	message: string;
	type: string;
	create_at: number;
	update_at: number;
	edit_at: number;
	delete_at: number;
	is_pinned: boolean;
	root_id?: string;
	original_id?: string;
	hashtags?: string;
	pending_post_id?: string;
	file_ids?: string[];
	props?: Record<string, unknown>;
	metadata?: {
		files?: MattermostFileInfo[];
		file_infos?: MattermostFileInfo[];
		[key: string]: unknown;
	};
	reply_count?: number;
	last_reply_at?: number;
}

export interface MattermostUser {
	id: string;
	username: string;
	first_name?: string;
	last_name?: string;
	nickname?: string;
	delete_at?: number;
	is_bot?: boolean;
	position?: string;
}

export interface MattermostChannel {
	id: string;
	name: string;
	display_name?: string;
	type: MattermostChannelType;
	team_id?: string;
	header?: string;
	purpose?: string;
	delete_at?: number;
}

export interface MattermostPostsResponse {
	order: string[];
	posts: Record<string, MattermostPost>;
	next_post_id?: string;
	prev_post_id?: string;
	has_next?: boolean;
}

export interface MattermostReaction {
	post_id: string;
	emoji_name: string;
	user_id: string;
	create_at?: number;
}

export interface MattermostWebSocketEvent {
	event?: string;
	data?: Record<string, unknown>;
	broadcast?: {
		channel_id?: string;
		team_id?: string;
		user_id?: string;
	};
	seq?: number;
	seq_reply?: number;
	status?: string;
	error?: {
		id?: string;
		message?: string;
		detailed_error?: string;
		request_id?: string;
		status_code?: number;
	};
}

export interface MattermostApiError {
	id?: string;
	message?: string;
	detailed_error?: string;
	request_id?: string;
	status_code?: number;
}

export interface MattermostCreatePostRequest {
	channel_id: string;
	message: string;
	root_id?: string;
	file_ids?: string[];
	props?: Record<string, unknown>;
}
