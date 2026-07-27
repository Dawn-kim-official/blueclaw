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

export type MirrorPlatformEdit = {
	externalId: string;
	externalChannelId: string;
	text: string;
	senderPlatformUserId: string;
	senderEmail?: string;
};

export type MirrorPlatformDelete = {
	externalId: string;
	externalChannelId: string;
	senderPlatformUserId: string;
	senderEmail?: string;
};

export type MirrorPlatformReaction = {
	externalId: string;
	externalChannelId: string;
	emoji: string;
	senderPlatformUserId: string;
	senderEmail?: string;
};

export type PlatformMirrorSink = {
	message(inbound: MirrorPlatformInbound): void;
	edit(inbound: MirrorPlatformEdit): void;
	remove(inbound: MirrorPlatformDelete): void;
	react(inbound: MirrorPlatformReaction): void;
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

export type MirrorBuzzEdit = {
	targetEventId: string;
	buzzChannelId: string;
	text: string;
	senderEmail?: string;
	origin: MessageOrigin | null;
};

export type MirrorBuzzDelete = {
	targetEventId: string;
	buzzChannelId: string;
	senderEmail?: string;
	origin: MessageOrigin | null;
};

export type MirrorBuzzReaction = {
	targetEventId: string;
	buzzChannelId: string;
	emoji: string;
	senderEmail?: string;
	origin: MessageOrigin | null;
};

export type BuzzMirrorSink = {
	message(inbound: MirrorBuzzInbound): void;
	edit(inbound: MirrorBuzzEdit): void;
	remove(inbound: MirrorBuzzDelete): void;
	react(inbound: MirrorBuzzReaction): void;
};
