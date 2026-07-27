import { describe, expect, test } from 'bun:test';
import { deriveBuzzSecret } from '../src/mirror/identity.ts';

describe('buzz identity derivation', () => {
	// Cross-language anchor: this vector is produced by the Go source of truth
	// (cmd/buzz-migrate deriveSecret = sha256(seed + "|secret|" + lower(trim(email)))).
	// If either side changes the formula this test must fail.
	test('email case matches the Go importer scheme byte-for-byte', () => {
		expect(
			deriveBuzzSecret('mirror-test-seed', {
				platform: 'mattermost',
				platformUserId: 'user-abc',
				email: ' Alice@Example.com ',
			}),
		).toBe('aa04132487d89014a53b0c1d6378dd99fcea6d503842036ee13602211423164b');
	});

	test('email presence makes the key independent of platform, so history and live match', () => {
		const fromImport = deriveBuzzSecret('seed', {
			platform: 'mattermost',
			platformUserId: 'mm-1',
			email: 'bob@dawn.kim',
		});
		const fromLiveOtherPlatform = deriveBuzzSecret('seed', {
			platform: 'slack',
			platformUserId: 'U999',
			email: 'bob@dawn.kim',
		});
		expect(fromLiveOtherPlatform).toBe(fromImport);
	});

	test('falls back to platform:userId when the platform has no email', () => {
		const noEmail = deriveBuzzSecret('seed', { platform: 'signal', platformUserId: '+15551234' });
		const explicit = deriveBuzzSecret('seed', {
			platform: 'signal',
			platformUserId: '+15551234',
			email: '',
		});
		expect(noEmail).toMatch(/^[0-9a-f]{64}$/);
		expect(explicit).toBe(noEmail);
	});
});
