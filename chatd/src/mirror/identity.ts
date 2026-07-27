import { createHash } from 'node:crypto';

export type PlatformIdentity = {
	platform: string;
	platformUserId: string;
	email?: string;
};

function sha256Hex(value: string): string {
	return createHash('sha256').update(value).digest('hex');
}

export function deriveBuzzSecret(seed: string, identity: PlatformIdentity): string {
	const email = identity.email?.trim().toLowerCase();
	const subject = email ? email : `${identity.platform}:${identity.platformUserId}`;
	return sha256Hex(`${seed}|secret|${subject}`);
}

// Derives the canonical Buzz channel id for a platform channel. Uses the bare
// external channel id (no platform prefix) so live messages land in the same
// Buzz channel the one-time importer created; platform-native channel ids are
// already disjoint across platforms. Matches the Go importer deriveChannelID.
export function deriveBuzzChannelId(seed: string, externalChannelId: string): string {
	const hex = sha256Hex(`${seed}|channel|${externalChannelId}`);
	return [
		hex.slice(0, 8),
		hex.slice(8, 12),
		hex.slice(12, 16),
		hex.slice(16, 20),
		hex.slice(20, 32),
	].join('-');
}
