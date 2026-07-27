import { createBuzzPublisher } from './buzz-publisher.ts';
import type { MirrorBuzzInbound, MirrorPlatformInbound } from './inbound.ts';
import { MappingStore } from './mapping-store.ts';
import { createMattermostPuppetPoster } from './mattermost-puppet.ts';
import { MirrorOrchestrator, type PlatformPoster } from './orchestrator.ts';

export type MirrorWiring = {
	onMattermostInbound: (message: MirrorPlatformInbound) => void;
	onBuzzInbound: (message: MirrorBuzzInbound) => void;
};

// Assembles the star-topology mirror: an admind-backed mapping store, a per-user
// Buzz publisher, and per-user platform posters, driven by the orchestrator. The
// returned callbacks are handed to each adapter's inbound tap.
export function createMirror(options: {
	seed: string;
	admindBaseURL: string;
	connectedPlatforms: string[];
	buzz: { relayURL: string; authTagJSON?: string };
	mattermost?: { baseURL: string; adminToken: string };
	onError?: (context: string, detail: unknown) => void;
}): MirrorWiring {
	const mapping = new MappingStore(options.admindBaseURL);
	const posters: Record<string, PlatformPoster> = {};
	if (options.mattermost) {
		posters.mattermost = createMattermostPuppetPoster(options.mattermost);
	}
	const orchestrator = new MirrorOrchestrator(
		options.seed,
		mapping,
		options.connectedPlatforms,
		createBuzzPublisher(options.buzz.relayURL, options.buzz.authTagJSON),
		posters,
	);
	const report = (context: string, detail: unknown): void => options.onError?.(context, detail);
	return {
		onMattermostInbound: (message) => {
			if (!message.senderEmail) {
				report('mattermost inbound skipped: sender has no linked email', message.externalId);
				return;
			}
			void orchestrator
				.onPlatformMessage({
					platform: 'mattermost',
					externalId: message.externalId,
					externalChannelId: message.externalChannelId,
					text: message.text,
					sender: {
						platform: 'mattermost',
						platformUserId: message.senderPlatformUserId,
						email: message.senderEmail,
					},
					replyToExternalId: message.replyToExternalId,
				})
				.catch((error) => report('mattermost -> buzz mirror failed', error));
		},
		onBuzzInbound: (message) => {
			void orchestrator
				.onBuzzMessage({
					buzzEventId: message.buzzEventId,
					buzzChannelId: message.buzzChannelId,
					text: message.text,
					origin: message.origin,
					senderName: message.senderName,
					senderEmail: message.senderEmail,
					replyToBuzzEventId: message.replyToBuzzEventId,
				})
				.catch((error) => report('buzz -> platforms mirror failed', error));
		},
	};
}
