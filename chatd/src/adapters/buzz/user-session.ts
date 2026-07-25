import { createBuzzRelayClient } from "./relay-client.ts";
import { firstTagValue, type BuzzEvent } from "./types.ts";

const STREAM_MESSAGE_KIND = 9;
const DM_OPEN_KIND = 41010;
const GROUP_METADATA_KIND = 39000;

export type UserDirectMessageSend = {
	relayURL: string;
	userSecretHex: string;
	counterpartPubkeyHex: string;
	message: string;
};

export type UserDirectMessageChannel = {
	channelID: string;
	userPubkeyHex: string;
};

export async function ensureUserDirectMessageChannel(
	relayURL: string,
	userSecretHex: string,
	counterpartPubkeyHex: string,
): Promise<UserDirectMessageChannel> {
	const relay = createBuzzRelayClient(relayURL, userSecretHex);
	try {
		await relay.connect();
		const existingChannelID = await findDirectMessageChannelID(relay, relay.pubkeyHex, counterpartPubkeyHex);
		if (existingChannelID) {
			return { channelID: existingChannelID, userPubkeyHex: relay.pubkeyHex };
		}
		const acknowledgement = await relay.publishForAcknowledgement(DM_OPEN_KIND, "", [
			["p", counterpartPubkeyHex],
		]);
		const openedChannelID = channelIDFromAcknowledgement(acknowledgement);
		if (!openedChannelID) {
			throw new Error("relay did not return a direct message channel");
		}
		return { channelID: openedChannelID, userPubkeyHex: relay.pubkeyHex };
	} finally {
		relay.disconnect();
	}
}

export async function sendDirectMessageAsUser(request: UserDirectMessageSend): Promise<string> {
	const channel = await ensureUserDirectMessageChannel(
		request.relayURL,
		request.userSecretHex,
		request.counterpartPubkeyHex,
	);
	const relay = createBuzzRelayClient(request.relayURL, request.userSecretHex);
	try {
		await relay.connect();
		const event = await relay.publish(STREAM_MESSAGE_KIND, request.message, [
			["h", channel.channelID],
			["p", request.counterpartPubkeyHex],
		]);
		return event.id;
	} finally {
		relay.disconnect();
	}
}

async function findDirectMessageChannelID(
	relay: { query: (filter: object) => Promise<BuzzEvent[]> },
	userPubkeyHex: string,
	counterpartPubkeyHex: string,
): Promise<string | undefined> {
	const metadataEvents = await relay.query({ kinds: [GROUP_METADATA_KIND], "#p": [counterpartPubkeyHex] });
	const latestByChannel = new Map<string, BuzzEvent>();
	for (const event of metadataEvents) {
		const channelID = firstTagValue(event, "d");
		if (!channelID) continue;
		const known = latestByChannel.get(channelID);
		if (!known || event.created_at > known.created_at) latestByChannel.set(channelID, event);
	}
	for (const [channelID, event] of latestByChannel) {
		if (firstTagValue(event, "t") !== "dm") continue;
		const participants = event.tags.filter((tag) => tag[0] === "p").map((tag) => tag[1]);
		if (participants.length !== 2) continue;
		if (participants.includes(userPubkeyHex) && participants.includes(counterpartPubkeyHex)) {
			return channelID;
		}
	}
	return undefined;
}

function channelIDFromAcknowledgement(acknowledgement: string): string | undefined {
	const payloadStart = acknowledgement.indexOf("{");
	if (payloadStart < 0) return undefined;
	try {
		const payload: unknown = JSON.parse(acknowledgement.slice(payloadStart));
		if (typeof payload === "object" && payload !== null && "channel_id" in payload) {
			const { channel_id: channelID } = payload as { channel_id: unknown };
			return typeof channelID === "string" && channelID.trim() !== "" ? channelID : undefined;
		}
	} catch {
		return undefined;
	}
	return undefined;
}
