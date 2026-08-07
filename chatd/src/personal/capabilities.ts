import type { PersonalGateway } from "./gateway.ts";
import {
	MalformedRequest,
	parsePersonRequest,
	requireConversation,
	requireMessage,
} from "./parse.ts";

export type PersonCapability = (gateway: PersonalGateway, requestBody: unknown) => Promise<object>;

export const personCapabilities: Record<string, PersonCapability> = {
	"person.identity": async (gateway, body) => {
		const request = parsePersonRequest(body);
		return await gateway.identity(request.actor);
	},
	"person.conversations.list": async (gateway, body) => {
		const request = parsePersonRequest(body);
		return { conversations: await gateway.listConversations(request.actor) };
	},
	"person.people.list": async (gateway, body) => {
		const request = parsePersonRequest(body);
		return { people: await gateway.listPeople(request.actor) };
	},
	"person.dm.ensure": async (gateway, body) => {
		const request = parsePersonRequest(body);
		return await gateway.ensureDirectConversation(request.actor, request.counterpartExternalIDs);
	},
	"person.messages.list": async (gateway, body) => {
		const request = parsePersonRequest(body);
		return await gateway.listMessages(request.actor, requireConversation(request), request.before);
	},
	"person.message.send": async (gateway, body) => {
		const request = parsePersonRequest(body);
		return await gateway.sendMessage(
			request.actor,
			requireConversation(request),
			request.body ?? "",
			request.parentID,
		);
	},
	"person.message.edit": async (gateway, body) => {
		const request = parsePersonRequest(body);
		return await gateway.editMessage(
			request.actor,
			requireConversation(request),
			requireMessage(request),
			request.body ?? "",
		);
	},
	"person.message.delete": async (gateway, body) => {
		const request = parsePersonRequest(body);
		await gateway.deleteMessage(request.actor, requireConversation(request), requireMessage(request));
		return {};
	},
	"person.reaction.add": async (gateway, body) => {
		const request = parsePersonRequest(body);
		await gateway.addReaction(
			request.actor,
			requireConversation(request),
			requireMessage(request),
			requireEmoji(request.emoji),
		);
		return {};
	},
	"person.reaction.remove": async (gateway, body) => {
		const request = parsePersonRequest(body);
		await gateway.removeReaction(
			request.actor,
			requireConversation(request),
			requireMessage(request),
			requireEmoji(request.emoji),
		);
		return {};
	},
};

function requireEmoji(emoji: string | undefined): string {
	if (!emoji) throw new MalformedRequest("missing required field emoji");
	return emoji;
}
