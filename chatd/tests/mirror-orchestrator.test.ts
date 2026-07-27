import { beforeEach, describe, expect, test } from 'bun:test';
import type { ChannelMapping, MessageMapping } from '../src/mirror/mapping-store.ts';
import {
	MirrorOrchestrator,
	type BuzzPublish,
	type MappingStoreLike,
	type PlatformPost,
} from '../src/mirror/orchestrator.ts';

class FakeMappingStore implements MappingStoreLike {
	messages: MessageMapping[] = [];
	channels: ChannelMapping[] = [];
	async recordMessage(mapping: MessageMapping): Promise<void> {
		this.messages.push(mapping);
	}
	async messageByExternal(platform: string, externalId: string): Promise<MessageMapping | null> {
		return this.messages.find((m) => m.platform === platform && m.externalId === externalId) ?? null;
	}
	async messageByEvent(buzzEventId: string, platform: string): Promise<MessageMapping | null> {
		return this.messages.find((m) => m.buzzEventId === buzzEventId && m.platform === platform) ?? null;
	}
	async recordChannel(mapping: ChannelMapping): Promise<void> {
		this.channels.push(mapping);
	}
	async channelByBuzz(buzzChannelId: string, platform: string): Promise<ChannelMapping | null> {
		return this.channels.find((c) => c.buzzChannelId === buzzChannelId && c.platform === platform) ?? null;
	}
}

const SEED = 'orchestrator-test-seed';

describe('platform -> Buzz', () => {
	let store: FakeMappingStore;
	let published: BuzzPublish[];
	let orchestrator: MirrorOrchestrator;
	let publishCount: number;

	beforeEach(() => {
		store = new FakeMappingStore();
		published = [];
		publishCount = 0;
		orchestrator = new MirrorOrchestrator(
			SEED,
			store,
			['mattermost', 'slack'],
			async (publish) => {
				published.push(publish);
				publishCount += 1;
				return { eventId: `event-${publishCount}` };
			},
			{},
		);
	});

	test('publishes a platform message to Buzz with an origin and records the mapping', async () => {
		await orchestrator.onPlatformMessage({
			platform: 'mattermost',
			externalId: 'post-1',
			externalChannelId: 'mm-chan',
			text: 'hello',
			sender: { platform: 'mattermost', platformUserId: 'u1', email: 'a@example.com' },
		});
		expect(published).toHaveLength(1);
		expect(published[0]?.origin).toEqual({ platform: 'mattermost', externalId: 'post-1' });
		expect(await store.messageByExternal('mattermost', 'post-1')).not.toBeNull();
		expect(await store.channelByBuzz(published[0]!.buzzChannelId, 'mattermost')).not.toBeNull();
	});

	test('skips a message it already mirrored, so a fanned-out copy never re-publishes', async () => {
		await store.recordMessage({
			buzzEventId: 'event-x',
			platform: 'mattermost',
			externalId: 'post-1',
			externalChannelId: 'mm-chan',
		});
		await orchestrator.onPlatformMessage({
			platform: 'mattermost',
			externalId: 'post-1',
			externalChannelId: 'mm-chan',
			text: 'echo',
			sender: { platform: 'mattermost', platformUserId: 'u1' },
		});
		expect(publishCount).toBe(0);
	});
});

describe('Buzz -> platforms fan-out', () => {
	let store: FakeMappingStore;
	let posts: PlatformPost[];
	let orchestrator: MirrorOrchestrator;

	beforeEach(() => {
		store = new FakeMappingStore();
		posts = [];
		const poster = async (post: PlatformPost) => {
			posts.push(post);
			return { externalId: `${post.target}-msg` };
		};
		orchestrator = new MirrorOrchestrator(SEED, store, ['mattermost', 'slack'], async () => ({ eventId: 'e' }), {
			mattermost: poster,
			slack: poster,
		});
	});

	test('fans out to other platforms but never back to the origin platform', async () => {
		await store.recordChannel({ buzzChannelId: 'bc', platform: 'slack', externalChannelId: 'slack-chan' });
		await orchestrator.onBuzzMessage({
			buzzEventId: 'e1',
			buzzChannelId: 'bc',
			text: 'hi',
			origin: { platform: 'mattermost', externalId: 'post-1' },
			senderName: 'Alice',
		});
		expect(posts.map((p) => p.target)).toEqual(['slack']);
	});

	test('skips a target that already has the event mapped', async () => {
		await store.recordChannel({ buzzChannelId: 'bc', platform: 'slack', externalChannelId: 'slack-chan' });
		await store.recordMessage({ buzzEventId: 'e1', platform: 'slack', externalId: 'slack-existing', externalChannelId: 'slack-chan' });
		await orchestrator.onBuzzMessage({
			buzzEventId: 'e1',
			buzzChannelId: 'bc',
			text: 'hi',
			origin: null,
			senderName: 'Alice',
		});
		expect(posts).toHaveLength(0);
	});

	test('skips a target whose channel is not mapped yet', async () => {
		await orchestrator.onBuzzMessage({
			buzzEventId: 'e1',
			buzzChannelId: 'bc-unmapped',
			text: 'hi',
			origin: null,
			senderName: 'Alice',
		});
		expect(posts).toHaveLength(0);
	});
});
