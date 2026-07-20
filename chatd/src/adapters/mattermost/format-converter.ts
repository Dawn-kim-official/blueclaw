import {
	BaseFormatConverter,
	convertEmojiPlaceholders,
	parseMarkdown,
	stringifyMarkdown,
	type AdapterPostableMessage,
	type Root,
} from "chat";

export class MattermostFormatConverter extends BaseFormatConverter {
	toAst(platformText: string): Root {
		return parseMarkdown(platformText);
	}

	fromAst(ast: Root): string {
		return stringifyMarkdown(ast);
	}

	override renderPostable(message: AdapterPostableMessage): string {
		// Mattermost uses Slack-style :emoji_name: tokens in message text.
		return convertEmojiPlaceholders(super.renderPostable(message), "slack");
	}
}
