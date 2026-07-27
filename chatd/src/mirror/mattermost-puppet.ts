import type { PlatformPost, PlatformPoster } from './orchestrator.ts';

type FetchImpl = typeof fetch;

// Posts a mirrored message to Mattermost as the real author by acting through
// that person's own personal access token, minted once with the admin token and
// cached. This is per-user puppeting, not a bot posting under someone's name.
// A Buzz sender with no linked email cannot be puppeted, so that message is
// dropped rather than posted under the wrong identity.
export function createMattermostPuppetPoster(options: {
	baseURL: string;
	adminToken: string;
	tokenDescription?: string;
	fetchImpl?: FetchImpl;
}): PlatformPoster {
	const baseURL = options.baseURL.replace(/\/$/, '');
	const fetchImpl = options.fetchImpl ?? fetch;
	const description = options.tokenDescription ?? 'chatd mirror puppet';
	const userIdByEmail = new Map<string, string>();
	const tokenByUserId = new Map<string, string>();

	function apiURL(path: string): string {
		return `${baseURL}/api/v4${path.startsWith('/') ? path : `/${path}`}`;
	}

	async function request<T>(token: string, path: string, init: RequestInit = {}): Promise<T> {
		const headers = new Headers(init.headers);
		headers.set('Authorization', `Bearer ${token}`);
		headers.set('Accept', 'application/json');
		if (init.body && !headers.has('Content-Type')) headers.set('Content-Type', 'application/json');
		const response = await fetchImpl(apiURL(path), { ...init, headers });
		if (!response.ok) {
			throw new Error(`mattermost ${path} returned ${response.status}`);
		}
		if (response.status === 204) return undefined as T;
		return (await response.json()) as T;
	}

	async function userIdForEmail(email: string): Promise<string> {
		const cached = userIdByEmail.get(email);
		if (cached) return cached;
		const user = await request<{ id: string }>(options.adminToken, `/users/email/${encodeURIComponent(email)}`);
		userIdByEmail.set(email, user.id);
		return user.id;
	}

	async function accessTokenForUser(userId: string): Promise<string> {
		const cached = tokenByUserId.get(userId);
		if (cached) return cached;
		const created = await request<{ token: string }>(options.adminToken, `/users/${userId}/tokens`, {
			method: 'POST',
			body: JSON.stringify({ description }),
		});
		tokenByUserId.set(userId, created.token);
		return created.token;
	}

	return async (post: PlatformPost): Promise<{ externalId: string }> => {
		if (!post.senderEmail) {
			throw new Error('mattermost mirror: sender has no linked email to puppet');
		}
		const userId = await userIdForEmail(post.senderEmail);
		const userToken = await accessTokenForUser(userId);
		const created = await request<{ id: string }>(userToken, '/posts', {
			method: 'POST',
			body: JSON.stringify({
				channel_id: post.externalChannelId,
				message: post.text,
				...(post.replyToExternalId ? { root_id: post.replyToExternalId } : {}),
			}),
		});
		return { externalId: created.id };
	};
}
