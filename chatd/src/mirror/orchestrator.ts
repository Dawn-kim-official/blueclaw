import { EchoSuppressor } from './echo-suppressor.ts';
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

export type BuzzPublish = {
	userSecretHex: string;
	buzzChannelId: string;
	text: string;
	origin: MessageOrigin;
	replyToBuzzEventId?: string;
};

export type BuzzEdit = {
	userSecretHex: string;
	buzzChannelId: string;
	targetEventId: string;
	text: string;
	origin: MessageOrigin;
};

export type BuzzDelete = {
	userSecretHex: string;
	buzzChannelId: string;
	targetEventId: string;
	origin: MessageOrigin;
};

export type BuzzReaction = {
	userSecretHex: string;
	buzzChannelId: string;
	targetEventId: string;
	emoji: string;
	origin: MessageOrigin;
};

export interface BuzzGateway {
	publish(publish: BuzzPublish): Promise<{ eventId: string }>;
	edit(edit: BuzzEdit): Promise<void>;
	remove(remove: BuzzDelete): Promise<void>;
	react(react: BuzzReaction): Promise<void>;
}

export type PlatformPost = {
	target: string;
	externalChannelId: string;
	text: string;
	senderName: string;
	senderEmail?: string;
	replyToExternalId?: string;
};

export type PlatformEdit = {
	target: string;
	externalChannelId: string;
	externalId: string;
	text: string;
	senderEmail?: string;
};

export type PlatformDelete = {
	target: string;
	externalChannelId: string;
	externalId: string;
	senderEmail?: string;
};

export type PlatformReaction = {
	target: string;
	externalChannelId: string;
	externalId: string;
	emoji: string;
	senderEmail?: string;
};

export interface PlatformGateway {
	post(post: PlatformPost): Promise<{ externalId: string }>;
	edit(edit: PlatformEdit): Promise<void>;
	remove(remove: PlatformDelete): Promise<void>;
	react(react: PlatformReaction): Promise<void>;
}

export type InboundPlatformMessage = {
	platform: string;
	externalId: string;
	externalChannelId: string;
	text: string;
	sender: PlatformIdentity;
	replyToExternalId?: string;
};

export type InboundPlatformEdit = {
	platform: string;
	externalId: string;
	externalChannelId: string;
	text: string;
	sender: PlatformIdentity;
};

export type InboundPlatformDelete = {
	platform: string;
	externalId: string;
	externalChannelId: string;
	sender: PlatformIdentity;
};

export type InboundPlatformReaction = {
	platform: string;
	externalId: string;
	externalChannelId: string;
	emoji: string;
	sender: PlatformIdentity;
};

export type InboundBuzzMessage = {
	buzzEventId: string;
	buzzChannelId: string;
	text: string;
	origin: MessageOrigin | null;
	senderName: string;
	senderEmail?: string;
	replyToBuzzEventId?: string;
};

export type InboundBuzzEdit = {
	targetEventId: string;
	buzzChannelId: string;
	text: string;
	origin: MessageOrigin | null;
	senderEmail?: string;
};

export type InboundBuzzDelete = {
	targetEventId: string;
	buzzChannelId: string;
	origin: MessageOrigin | null;
	senderEmail?: string;
};

export type InboundBuzzReaction = {
	targetEventId: string;
	buzzChannelId: string;
	emoji: string;
	origin: MessageOrigin | null;
	senderEmail?: string;
};

// Star-topology mirror: every platform message becomes a Buzz event (SoT), and
// every Buzz event fans out to the other platforms. Buzz is always the hub, so
// platform-to-platform traffic is never direct.
//
// Echo loops are broken by layer:
// - create: the durable message mapping (a fanned-out copy is already mapped,
//   so it is skipped on re-ingest).
// - edit/delete/reaction: they have no per-op mapping, so the origin tag keeps
//   a Buzz event off the platform it came from, and the EchoSuppressor drops the
//   inbound event a platform emits for the mirror's own apply.
export class MirrorOrchestrator {
	constructor(
		private readonly seed: string,
		private readonly mapping: MappingStoreLike,
		private readonly connectedPlatforms: readonly string[],
		private readonly buzz: BuzzGateway,
		private readonly platforms: Record<string, PlatformGateway>,
		private readonly echo: EchoSuppressor = new EchoSuppressor(),
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
		const published = await this.buzz.publish({
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

	async onPlatformEdit(edit: InboundPlatformEdit): Promise<void> {
		if (this.echo.consume(['platform', edit.platform, edit.externalId, 'edit', edit.text])) return;
		const mapping = await this.mapping.messageByExternal(edit.platform, edit.externalId);
		if (!mapping) return;
		await this.buzz.edit({
			userSecretHex: deriveBuzzSecret(this.seed, edit.sender),
			buzzChannelId: deriveBuzzChannelId(this.seed, edit.externalChannelId),
			targetEventId: mapping.buzzEventId,
			text: edit.text,
			origin: { platform: edit.platform, externalId: edit.externalId },
		});
	}

	async onPlatformDelete(remove: InboundPlatformDelete): Promise<void> {
		if (this.echo.consume(['platform', remove.platform, remove.externalId, 'delete'])) return;
		const mapping = await this.mapping.messageByExternal(remove.platform, remove.externalId);
		if (!mapping) return;
		await this.buzz.remove({
			userSecretHex: deriveBuzzSecret(this.seed, remove.sender),
			buzzChannelId: deriveBuzzChannelId(this.seed, remove.externalChannelId),
			targetEventId: mapping.buzzEventId,
			origin: { platform: remove.platform, externalId: remove.externalId },
		});
	}

	async onPlatformReaction(reaction: InboundPlatformReaction): Promise<void> {
		if (this.echo.consume(['platform', reaction.platform, reaction.externalId, 'react', reaction.emoji, reaction.sender.email ?? ''])) return;
		const mapping = await this.mapping.messageByExternal(reaction.platform, reaction.externalId);
		if (!mapping) return;
		await this.buzz.react({
			userSecretHex: deriveBuzzSecret(this.seed, reaction.sender),
			buzzChannelId: deriveBuzzChannelId(this.seed, reaction.externalChannelId),
			targetEventId: mapping.buzzEventId,
			emoji: reaction.emoji,
			origin: { platform: reaction.platform, externalId: reaction.externalId },
		});
	}

	async onBuzzMessage(message: InboundBuzzMessage): Promise<void> {
		await this.forEachTarget(message.origin, async (target, gateway) => {
			if (await this.mapping.messageByEvent(message.buzzEventId, target)) return;
			const channel = await this.mapping.channelByBuzz(message.buzzChannelId, target);
			if (!channel) return;
			let replyToExternalId: string | undefined;
			if (message.replyToBuzzEventId) {
				const parent = await this.mapping.messageByEvent(message.replyToBuzzEventId, target);
				replyToExternalId = parent?.externalId;
			}
			const posted = await gateway.post({
				target,
				externalChannelId: channel.externalChannelId,
				text: message.text,
				senderName: message.senderName,
				senderEmail: message.senderEmail,
				replyToExternalId,
			});
			await this.mapping.recordMessage({
				buzzEventId: message.buzzEventId,
				platform: target,
				externalId: posted.externalId,
				externalChannelId: channel.externalChannelId,
			});
		});
	}

	async onBuzzEdit(edit: InboundBuzzEdit): Promise<void> {
		await this.forEachMappedTarget(edit.origin, edit.targetEventId, async (target, gateway, mapping) => {
			this.echo.expect(['platform', target, mapping.externalId, 'edit', edit.text]);
			await gateway.edit({
				target,
				externalChannelId: mapping.externalChannelId,
				externalId: mapping.externalId,
				text: edit.text,
				senderEmail: edit.senderEmail,
			});
		});
	}

	async onBuzzDelete(remove: InboundBuzzDelete): Promise<void> {
		await this.forEachMappedTarget(remove.origin, remove.targetEventId, async (target, gateway, mapping) => {
			this.echo.expect(['platform', target, mapping.externalId, 'delete']);
			await gateway.remove({
				target,
				externalChannelId: mapping.externalChannelId,
				externalId: mapping.externalId,
				senderEmail: remove.senderEmail,
			});
		});
	}

	async onBuzzReaction(reaction: InboundBuzzReaction): Promise<void> {
		await this.forEachMappedTarget(reaction.origin, reaction.targetEventId, async (target, gateway, mapping) => {
			this.echo.expect(['platform', target, mapping.externalId, 'react', reaction.emoji, reaction.senderEmail ?? '']);
			await gateway.react({
				target,
				externalChannelId: mapping.externalChannelId,
				externalId: mapping.externalId,
				emoji: reaction.emoji,
				senderEmail: reaction.senderEmail,
			});
		});
	}

	private async forEachTarget(
		origin: MessageOrigin | null,
		apply: (target: string, gateway: PlatformGateway) => Promise<void>,
	): Promise<void> {
		for (const target of mirrorTargets(this.connectedPlatforms, origin?.platform ?? null)) {
			const gateway = this.platforms[target];
			if (gateway) await apply(target, gateway);
		}
	}

	private async forEachMappedTarget(
		origin: MessageOrigin | null,
		targetEventId: string,
		apply: (target: string, gateway: PlatformGateway, mapping: MessageMapping) => Promise<void>,
	): Promise<void> {
		await this.forEachTarget(origin, async (target, gateway) => {
			const mapping = await this.mapping.messageByEvent(targetEventId, target);
			if (mapping) await apply(target, gateway, mapping);
		});
	}
}
