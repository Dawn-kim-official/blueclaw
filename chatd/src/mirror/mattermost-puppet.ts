import type {
	PlatformDelete,
	PlatformEdit,
	PlatformGateway,
	PlatformPost,
	PlatformReaction,
} from './orchestrator.ts';

type FetchImpl = typeof fetch;

// Mirrors operations to Mattermost as the real author by acting through that
// person's own personal access token, minted once with the admin token and
// cached. This is per-user puppeting, not a bot acting under someone's name. An
// author with no linked email cannot be puppeted, so that operation is dropped
// rather than performed under the wrong identity.
export function createMattermostGateway(options: {
	baseURL: string;
	adminToken: string;
	tokenDescription?: string;
	fetchImpl?: FetchImpl;
}): PlatformGateway {
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
		if (!response.ok) throw new Error(`mattermost ${path} returned ${response.status}`);
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

	async function tokenForUser(userId: string): Promise<string> {
		const cached = tokenByUserId.get(userId);
		if (cached) return cached;
		const created = await request<{ token: string }>(options.adminToken, `/users/${userId}/tokens`, {
			method: 'POST',
			body: JSON.stringify({ description }),
		});
		tokenByUserId.set(userId, created.token);
		return created.token;
	}

	async function actAs(email: string | undefined): Promise<{ userId: string; token: string }> {
		if (!email) throw new Error('mattermost mirror: author has no linked email to puppet');
		const userId = await userIdForEmail(email);
		return { userId, token: await tokenForUser(userId) };
	}

	return {
		async post(post: PlatformPost): Promise<{ externalId: string }> {
			const { token } = await actAs(post.senderEmail);
			const created = await request<{ id: string }>(token, '/posts', {
				method: 'POST',
				body: JSON.stringify({
					channel_id: post.externalChannelId,
					message: post.text,
					...(post.replyToExternalId ? { root_id: post.replyToExternalId } : {}),
				}),
			});
			return { externalId: created.id };
		},
		async edit(edit: PlatformEdit): Promise<void> {
			const { token } = await actAs(edit.senderEmail);
			await request<void>(token, `/posts/${edit.externalId}/patch`, {
				method: 'PUT',
				body: JSON.stringify({ message: edit.text }),
			});
		},
		async remove(remove: PlatformDelete): Promise<void> {
			const { token } = await actAs(remove.senderEmail);
			await request<void>(token, `/posts/${remove.externalId}`, { method: 'DELETE' });
		},
		async react(reaction: PlatformReaction): Promise<void> {
			const { userId, token } = await actAs(reaction.senderEmail);
			await request<void>(token, '/reactions', {
				method: 'POST',
				body: JSON.stringify({
					user_id: userId,
					post_id: reaction.externalId,
					emoji_name: reaction.emoji.replace(/^:|:$/g, ''),
				}),
			});
		},
	};
}
