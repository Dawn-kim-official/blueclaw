import { describe, expect, test } from 'bun:test';
import { MappingStore } from '../src/mirror/mapping-store.ts';

type RecordedCall = { url: string; method: string; body?: unknown };

function stubFetch(routes: Record<string, unknown>): { fetch: typeof fetch; calls: RecordedCall[] } {
	const calls: RecordedCall[] = [];
	const fetchImpl = (async (input: string | URL | Request, init?: RequestInit) => {
		const url = typeof input === 'string' ? input : input.toString();
		calls.push({
			url,
			method: init?.method ?? 'GET',
			body: init?.body ? JSON.parse(String(init.body)) : undefined,
		});
		const pathname = new URL(url).pathname;
		const payload = routes[pathname] ?? { ok: true };
		return new Response(JSON.stringify(payload), {
			status: 200,
			headers: { 'Content-Type': 'application/json' },
		});
	}) as unknown as typeof fetch;
	return { fetch: fetchImpl, calls };
}

describe('MappingStore over admind HTTP', () => {
	test('records a message mapping with a POST body', async () => {
		const { fetch, calls } = stubFetch({});
		const store = new MappingStore('http://admind/', fetch);
		await store.recordMessage({
			buzzEventId: 'e1',
			platform: 'mattermost',
			externalId: 'p1',
			externalChannelId: 'c1',
		});
		expect(calls[0]?.method).toBe('POST');
		expect(calls[0]?.url).toBe('http://admind/bridge/api/message');
		expect(calls[0]?.body).toEqual({
			buzzEventId: 'e1',
			platform: 'mattermost',
			externalId: 'p1',
			externalChannelId: 'c1',
		});
	});

	test('returns the mapping when the lookup is found', async () => {
		const mapping = { buzzEventId: 'e1', platform: 'mattermost', externalId: 'p1', externalChannelId: 'c1' };
		const { fetch, calls } = stubFetch({ '/bridge/api/message': { found: true, mapping } });
		const store = new MappingStore('http://admind', fetch);
		const result = await store.messageByExternal('mattermost', 'p1');
		expect(result).toEqual(mapping);
		expect(calls[0]?.url ?? '').toContain('platform=mattermost');
		expect(calls[0]?.url ?? '').toContain('externalId=p1');
	});

	test('returns null when the lookup misses', async () => {
		const { fetch } = stubFetch({ '/bridge/api/message': { found: false, mapping: {} } });
		const store = new MappingStore('http://admind', fetch);
		expect(await store.messageByEvent('unknown', 'slack')).toBeNull();
	});

	test('resolves a person Buzz secret by email through admind', async () => {
		const { fetch, calls } = stubFetch({ '/bridge/api/identity': { secretHex: 'abc123' } });
		const store = new MappingStore('http://admind', fetch);
		expect(await store.secretForEmail('a@example.com')).toBe('abc123');
		expect(calls[0]?.url ?? '').toContain('email=a%40example.com');
	});

	test('resolves a Buzz channel id for an external channel through admind', async () => {
		const { fetch, calls } = stubFetch({ '/bridge/api/channel/resolve': { buzzChannelId: 'bc-1' } });
		const store = new MappingStore('http://admind', fetch);
		expect(await store.buzzChannelForExternal('mattermost', 'chan-1')).toBe('bc-1');
		expect(calls[0]?.method).toBe('POST');
		expect(calls[0]?.body).toEqual({ platform: 'mattermost', externalChannelId: 'chan-1' });
	});

	test('throws on a non-ok response so callers do not treat errors as a miss', async () => {
		const failing = (async () => new Response('boom', { status: 500 })) as unknown as typeof fetch;
		const store = new MappingStore('http://admind', failing);
		await expect(store.messageByExternal('mattermost', 'p1')).rejects.toThrow('returned 500');
	});
});
