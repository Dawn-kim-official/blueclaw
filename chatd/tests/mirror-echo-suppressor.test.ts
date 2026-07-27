import { describe, expect, test } from 'bun:test';
import { EchoSuppressor } from '../src/mirror/echo-suppressor.ts';

describe('echo suppressor', () => {
	test('consumes a registered expectation exactly once', () => {
		const suppressor = new EchoSuppressor();
		suppressor.expect(['platform', 'mattermost', 'post-1', 'edit', 'hello']);
		expect(suppressor.consume(['platform', 'mattermost', 'post-1', 'edit', 'hello'])).toBe(true);
		expect(suppressor.consume(['platform', 'mattermost', 'post-1', 'edit', 'hello'])).toBe(false);
	});

	test('does not suppress a genuine event after the entry expires', () => {
		let now = 1_000;
		const suppressor = new EchoSuppressor(30_000, () => now);
		suppressor.expect(['platform', 'mattermost', 'post-1', 'delete']);
		now = 40_000;
		expect(suppressor.consume(['platform', 'mattermost', 'post-1', 'delete'])).toBe(false);
	});
});
