import { deriveBuzzChannelId, deriveBuzzSecret, type PlatformIdentity } from './identity.ts';
import type { ChannelMapping, MessageMapping } from './mapping-store.ts';
import { mirrorTargets, type MessageOrigin } from './origin.ts';

export interface MappingStoreLike {
	recordMessage(mapping: MessageMapping): Promise<void>;
	messageByExternal(platform: string, externalId: string): Promise<MessageMapping | null>;
	messageByEvent(buzzEventId: string, platform: string): Promise<MessageMapping | null>;
	recordChannel(mapping: ChannelMapping): Promise<void>;
	channelByBuzz(buzzChannelId: string, platform: string): Promise<ChannelMapping | null>;
}

export type InboundPlatformMessage = {
	platform: string;
	externalId: string;
	externalChannelId: string;
	text: string;
	sender: PlatformIdentity;
	replyToExternalId?: string;
};

export type BuzzPublish = {
	userSecretHex: string;
	buzzChannelId: string;
	text: string;
	origin: MessageOrigin;
	replyToBuzzEventId?: string;
};

export type BuzzPublisher = (publish: BuzzPublish) => Promise<{ eventId: string }>;

export type InboundBuzzMessage = {
	buzzEventId: string;
	buzzChannelId: string;
	text: string;
	origin: MessageOrigin | null;
	senderName: string;
	replyToBuzzEventId?: string;
};

export type PlatformPost = {
	target: string;
	externalChannelId: string;
	text: string;
	senderName: string;
	replyToExternalId?: string;
};

export type PlatformPoster = (post: PlatformPost) => Promise<{ externalId: string }>;

// Star-topology mirror: every platform message becomes a Buzz event (SoT), and
// every Buzz event fans out to the other platforms. Buzz is always the hub, so
// platform-to-platform traffic is never direct. Echo loops are broken two ways:
// a platform message we already mirrored is skipped on re-ingest, and a Buzz
// event is never sent back to the platform it originated from.
export class MirrorOrchestrator {
	constructor(
		private readonly seed: string,
		private readonly mapping: MappingStoreLike,
		private readonly connectedPlatforms: readonly string[],
		private readonly publishToBuzz: BuzzPublisher,
		private readonly posters: Record<string, PlatformPoster>,
	) {}

	async onPlatformMessage(message: InboundPlatformMessage): Promise<void> {
		if (await this.mapping.messageByExternal(message.platform, message.externalId)) return;
		const buzzChannelId = deriveBuzzChannelId(this.seed, message.externalChannelId);
		const userSecretHex = deriveBuzzSecret(this.seed, message.sender);
		let replyToBuzzEventId: string | undefined;
		if (message.replyToExternalId) {
			const parent = await this.mapping.messageByExternal(message.platform, message.replyToExternalId);
			replyToBuzzEventId = parent?.buzzEventId;
		}
		const published = await this.publishToBuzz({
			userSecretHex,
			buzzChannelId,
			text: message.text,
			origin: { platform: message.platform, externalId: message.externalId },
			replyToBuzzEventId,
		});
		await this.mapping.recordMessage({
			buzzEventId: published.eventId,
			platform: message.platform,
			externalId: message.externalId,
			externalChannelId: message.externalChannelId,
		});
		await this.mapping.recordChannel({
			buzzChannelId,
			platform: message.platform,
			externalChannelId: message.externalChannelId,
		});
	}

	async onBuzzMessage(message: InboundBuzzMessage): Promise<void> {
		const targets = mirrorTargets(this.connectedPlatforms, message.origin?.platform ?? null);
		for (const target of targets) {
			if (await this.mapping.messageByEvent(message.buzzEventId, target)) continue;
			const channel = await this.mapping.channelByBuzz(message.buzzChannelId, target);
			if (!channel) continue;
			const poster = this.posters[target];
			if (!poster) continue;
			let replyToExternalId: string | undefined;
			if (message.replyToBuzzEventId) {
				const parent = await this.mapping.messageByEvent(message.replyToBuzzEventId, target);
				replyToExternalId = parent?.externalId;
			}
			const posted = await poster({
				target,
				externalChannelId: channel.externalChannelId,
				text: message.text,
				senderName: message.senderName,
				replyToExternalId,
			});
			await this.mapping.recordMessage({
				buzzEventId: message.buzzEventId,
				platform: target,
				externalId: posted.externalId,
				externalChannelId: channel.externalChannelId,
			});
		}
	}
}
