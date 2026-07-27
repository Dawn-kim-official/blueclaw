import { describe, expect, test } from 'bun:test';
import { mirrorTargets, originOfTags, originTag } from '../src/mirror/origin.ts';

describe('message origin tags', () => {
	test('round-trips a platform origin through tag encoding', () => {
		const tag = originTag({ platform: 'mattermost', externalId: 'post123' });
		expect(tag).toEqual(['origin', 'mattermost', 'post123']);
		expect(originOfTags([['h', 'channel'], tag])).toEqual({
			platform: 'mattermost',
			externalId: 'post123',
		});
	});

	test('returns null when no origin tag is present', () => {
		expect(originOfTags([['h', 'channel'], ['e', 'root', '', 'reply']])).toBeNull();
	});

	test('ignores a malformed origin tag missing fields', () => {
		expect(originOfTags([['origin', 'mattermost']])).toBeNull();
	});
});

describe('mirror fan-out targets', () => {
	const platforms = ['mattermost', 'slack', 'signal'];

	test('excludes the origin platform so a message never echoes back', () => {
		expect(mirrorTargets(platforms, 'mattermost')).toEqual(['slack', 'signal']);
	});

	test('fans out to every platform when the message is hub-native', () => {
		expect(mirrorTargets(platforms, null)).toEqual(['mattermost', 'slack', 'signal']);
	});
});
