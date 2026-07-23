import { ValidationError } from "@chat-adapter/shared";
import { MattermostAdapter } from "./adapter";
import type { MattermostAdapterConfig } from "./types";

export function createMattermostAdapter(
	config?: Partial<MattermostAdapterConfig>,
): MattermostAdapter {
	const baseUrl = config?.baseUrl ?? process.env.MATTERMOST_BASE_URL;
	const botToken = config?.botToken ?? process.env.MATTERMOST_BOT_TOKEN;

	if (!baseUrl) {
		throw new ValidationError(
			"mattermost",
			"Mattermost baseUrl is required. Pass it in config or set MATTERMOST_BASE_URL.",
		);
	}

	if (!botToken) {
		throw new ValidationError(
			"mattermost",
			"Mattermost botToken is required. Pass it in config or set MATTERMOST_BOT_TOKEN.",
		);
	}

	return new MattermostAdapter({
		...config,
		baseUrl,
		botToken,
	});
}
