import { describe, expect, test } from 'bun:test';
import { createMattermostPuppetPoster } from '../src/mirror/mattermost-puppet.ts';

function stubFetch() {
	const calls: Array<{ url: string; method: string; body: unknown }> = [];
	const fetchImpl = (async (input: string, init: RequestInit = {}) => {
		const url = String(input);
		const method = init.method ?? 'GET';
		const body = init.body ? JSON.parse(String(init.body)) : undefined;
		calls.push({ url, method, body });
		const pathname = new URL(url).pathname;
		if (pathname.startsWith('/api/v4/users/email/')) {
			return new Response(JSON.stringify({ id: 'user-77' }), { status: 200 });
		}
		if (pathname === '/api/v4/users/user-77/tokens') {
			return new Response(JSON.stringify({ token: 'pat-abc' }), { status: 200 });
		}
		if (pathname === '/api/v4/posts') {
			return new Response(JSON.stringify({ id: 'mmpost-1' }), { status: 200 });
		}
		return new Response('not found', { status: 404 });
	}) as unknown as typeof fetch;
	return { calls, fetchImpl };
}

describe('mattermost puppet poster', () => {
	test('posts as the resolved user with a minted token and threads replies', async () => {
		const { calls, fetchImpl } = stubFetch();
		const poster = createMattermostPuppetPoster({
			baseURL: 'https://mm.example.com/',
			adminToken: 'admin-tok',
			fetchImpl,
		});

		const result = await poster({
			target: 'mattermost',
			externalChannelId: 'chan-x',
			text: 'mirrored',
			senderName: 'Alice',
			senderEmail: 'alice@dawn.kim',
			replyToExternalId: 'root-2',
		});

		expect(result).toEqual({ externalId: 'mmpost-1' });
		const postCall = calls.find((call) => call.url.endsWith('/api/v4/posts'));
		expect(postCall?.body).toEqual({ channel_id: 'chan-x', message: 'mirrored', root_id: 'root-2' });
	});

	test('caches the user id and token across posts', async () => {
		const { calls, fetchImpl } = stubFetch();
		const poster = createMattermostPuppetPoster({ baseURL: 'https://mm.example.com', adminToken: 'admin-tok', fetchImpl });
		const post = () =>
			poster({ target: 'mattermost', externalChannelId: 'c', text: 't', senderName: 'A', senderEmail: 'a@dawn.kim' });

		await post();
		await post();

		expect(calls.filter((call) => call.url.includes('/users/email/'))).toHaveLength(1);
		expect(calls.filter((call) => call.url.endsWith('/tokens'))).toHaveLength(1);
		expect(calls.filter((call) => call.url.endsWith('/api/v4/posts'))).toHaveLength(2);
	});

	test('refuses to post when the sender has no linked email', async () => {
		const { fetchImpl } = stubFetch();
		const poster = createMattermostPuppetPoster({ baseURL: 'https://mm.example.com', adminToken: 'admin-tok', fetchImpl });
		await expect(
			poster({ target: 'mattermost', externalChannelId: 'c', text: 't', senderName: 'A' }),
		).rejects.toThrow('no linked email');
	});
});
