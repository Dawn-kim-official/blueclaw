import type { MessageOrigin } from './origin.ts';

// Raw, already-resolved inbound events an adapter hands to the mirror. Adapters
// resolve the cheap identity fields they already know (email, profile name)
// before emitting, so the mirror wiring stays platform-agnostic.
export type MirrorPlatformInbound = {
	externalId: string;
	externalChannelId: string;
	text: string;
	senderPlatformUserId: string;
	senderEmail?: string;
	replyToExternalId?: string;
};

export type MirrorBuzzInbound = {
	buzzEventId: string;
	buzzChannelId: string;
	text: string;
	senderPubkey: string;
	senderName: string;
	senderEmail?: string;
	origin: MessageOrigin | null;
	replyToBuzzEventId?: string;
};
