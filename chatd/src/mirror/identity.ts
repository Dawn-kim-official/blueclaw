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
