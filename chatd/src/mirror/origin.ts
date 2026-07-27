const ORIGIN_TAG = 'origin';

export type MessageOrigin = {
	platform: string;
	externalId: string;
};

export function originTag(origin: MessageOrigin): string[] {
	return [ORIGIN_TAG, origin.platform, origin.externalId];
}

export function originOfTags(tags: string[][]): MessageOrigin | null {
	for (const tag of tags) {
		if (tag[0] === ORIGIN_TAG && tag[1] && tag[2]) {
			return { platform: tag[1], externalId: tag[2] };
		}
	}
	return null;
}

export function mirrorTargets(
	connectedPlatforms: readonly string[],
	originPlatform: string | null,
): string[] {
	return connectedPlatforms.filter((platform) => platform !== originPlatform);
}
