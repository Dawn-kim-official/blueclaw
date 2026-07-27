import { sendChannelMessageAsUser } from '../adapters/buzz/user-session.ts';
import type { BuzzPublish, BuzzPublisher } from './orchestrator.ts';
import { originTag } from './origin.ts';

type SendChannelMessage = typeof sendChannelMessageAsUser;

// Publishes a mirrored message to Buzz signed by the originating person's own
// derived key (per-user puppeting, never a bot relay). The origin tag lets the
// fan-out skip sending the event back to the platform it came from.
export function createBuzzPublisher(
	relayURL: string,
	authTagJSON: string | undefined,
	send: SendChannelMessage = sendChannelMessageAsUser,
): BuzzPublisher {
	return async (publish: BuzzPublish): Promise<{ eventId: string }> => {
		const eventId = await send({
			relayURL,
			authTagJSON,
			userSecretHex: publish.userSecretHex,
			channelID: publish.buzzChannelId,
			message: publish.text,
			replyToRootId: publish.replyToBuzzEventId,
			extraTags: [originTag(publish.origin)],
		});
		return { eventId };
	};
}
