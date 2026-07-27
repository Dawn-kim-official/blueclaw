import { createBuzzGateway } from './buzz-publisher.ts';
import type { BuzzMirrorSink, PlatformMirrorSink } from './inbound.ts';
import { MappingStore } from './mapping-store.ts';
import { createMattermostGateway } from './mattermost-puppet.ts';
import { MirrorOrchestrator, type PlatformGateway } from './orchestrator.ts';

export type MirrorWiring = {
	mattermost: PlatformMirrorSink;
	buzz: BuzzMirrorSink;
};

// Assembles the star-topology mirror: an admind-backed mapping store, a per-user
// Buzz gateway, and per-user platform gateways, driven by the orchestrator. The
// returned sinks are handed to each adapter's inbound tap.
export function createMirror(options: {
	admindBaseURL: string;
	connectedPlatforms: string[];
	buzz: { relayURL: string; authTagJSON?: string };
	mattermost?: { baseURL: string; adminToken: string };
	onError?: (context: string, detail: unknown) => void;
}): MirrorWiring {
	const mapping = new MappingStore(options.admindBaseURL);
	const platforms: Record<string, PlatformGateway> = {};
	if (options.mattermost) {
		platforms.mattermost = createMattermostGateway(options.mattermost);
	}
	const orchestrator = new MirrorOrchestrator(
		mapping,
		options.connectedPlatforms,
		createBuzzGateway(options.buzz.relayURL, options.buzz.authTagJSON),
		platforms,
		mapping,
	);
	const run = (context: string, work: Promise<void>): void => {
		void work.catch((error) => options.onError?.(context, error));
	};
	const skip = (context: string, detail: unknown): void => options.onError?.(context, detail);

	return {
		mattermost: {
			message(inbound) {
				if (!inbound.senderEmail) return skip('mattermost message skipped: no linked email', inbound.externalId);
				run('mattermost -> buzz message failed', orchestrator.onPlatformMessage({
					platform: 'mattermost',
					externalId: inbound.externalId,
					externalChannelId: inbound.externalChannelId,
					text: inbound.text,
					sender: { platform: 'mattermost', platformUserId: inbound.senderPlatformUserId, email: inbound.senderEmail },
					replyToExternalId: inbound.replyToExternalId,
				}));
			},
			edit(inbound) {
				if (!inbound.senderEmail) return skip('mattermost edit skipped: no linked email', inbound.externalId);
				run('mattermost -> buzz edit failed', orchestrator.onPlatformEdit({
					platform: 'mattermost',
					externalId: inbound.externalId,
					externalChannelId: inbound.externalChannelId,
					text: inbound.text,
					sender: { platform: 'mattermost', platformUserId: inbound.senderPlatformUserId, email: inbound.senderEmail },
				}));
			},
			remove(inbound) {
				if (!inbound.senderEmail) return skip('mattermost delete skipped: no linked email', inbound.externalId);
				run('mattermost -> buzz delete failed', orchestrator.onPlatformDelete({
					platform: 'mattermost',
					externalId: inbound.externalId,
					externalChannelId: inbound.externalChannelId,
					sender: { platform: 'mattermost', platformUserId: inbound.senderPlatformUserId, email: inbound.senderEmail },
				}));
			},
			react(inbound) {
				if (!inbound.senderEmail) return skip('mattermost reaction skipped: no linked email', inbound.externalId);
				run('mattermost -> buzz reaction failed', orchestrator.onPlatformReaction({
					platform: 'mattermost',
					externalId: inbound.externalId,
					externalChannelId: inbound.externalChannelId,
					emoji: inbound.emoji,
					sender: { platform: 'mattermost', platformUserId: inbound.senderPlatformUserId, email: inbound.senderEmail },
				}));
			},
		},
		buzz: {
			message(inbound) {
				run('buzz -> platforms message failed', orchestrator.onBuzzMessage(inbound));
			},
			edit(inbound) {
				run('buzz -> platforms edit failed', orchestrator.onBuzzEdit(inbound));
			},
			remove(inbound) {
				run('buzz -> platforms delete failed', orchestrator.onBuzzDelete(inbound));
			},
			react(inbound) {
				run('buzz -> platforms reaction failed', orchestrator.onBuzzReaction(inbound));
			},
		},
	};
}
