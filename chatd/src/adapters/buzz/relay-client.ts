import { finalizeEvent, getPublicKey } from "nostr-tools/pure";
import type { BuzzEvent } from "./types.ts";

type EventListener = (event: BuzzEvent) => void;

export type BuzzRelayClient = {
	pubkeyHex: string;
	connect: () => Promise<void>;
	disconnect: () => void;
	subscribe: (filters: object[], onEvent: EventListener) => void;
	query: (filter: object, timeoutMs?: number) => Promise<BuzzEvent[]>;
	publish: (kind: number, content: string, tags: string[][]) => Promise<BuzzEvent>;
};

export function createBuzzRelayClient(relayURL: string, privateKeyHex: string, authTagJSON?: string): BuzzRelayClient {
	const secretKey = hexToBytes(privateKeyHex);
	const pubkeyHex = getPublicKey(secretKey);

	let websocket: WebSocket | null = null;
	let isAuthed = false;
	let reconnectDelayMs = 1_000;
	let shouldReconnect = true;
	let subscriptionSerial = 0;
	const liveSubscriptions = new Map<string, { filters: object[]; onEvent: EventListener }>();
	const pendingQueries = new Map<string, { events: BuzzEvent[]; resolve: (events: BuzzEvent[]) => void }>();
	const pendingPublishes = new Map<string, { resolve: () => void; reject: (error: Error) => void }>();
	let openWaiters: Array<() => void> = [];

	function signEvent(kind: number, content: string, tags: string[][]): BuzzEvent {
		return finalizeEvent(
			{ kind, content, tags, created_at: Math.floor(Date.now() / 1000) },
			secretKey,
		) as BuzzEvent;
	}

	const isDebugEnabled = Bun.env.BUZZ_RELAY_DEBUG === "1";

	function send(frame: unknown[]): void {
		if (isDebugEnabled) console.error("[buzz-relay] send", JSON.stringify(frame).slice(0, 160));
		websocket?.send(JSON.stringify(frame));
	}

	function openSocket(): void {
		websocket = new WebSocket(relayURL);
		websocket.onopen = () => {
			reconnectDelayMs = 1_000;
			isAuthed = false;
			for (const [subscriptionID, subscription] of liveSubscriptions) {
				send(["REQ", subscriptionID, ...subscription.filters]);
			}
			for (const waiter of openWaiters) waiter();
			openWaiters = [];
		};
		websocket.onmessage = (message) => {
			let frame: unknown[];
			try {
				frame = JSON.parse(String(message.data));
			} catch {
				return;
			}
			handleFrame(frame);
		};
		websocket.onclose = () => {
			if (!shouldReconnect) return;
			setTimeout(openSocket, reconnectDelayMs);
			reconnectDelayMs = Math.min(reconnectDelayMs * 2, 30_000);
		};
		websocket.onerror = () => {};
	}

	function handleFrame(frame: unknown[]): void {
		if (isDebugEnabled) console.error("[buzz-relay] recv", JSON.stringify(frame).slice(0, 160));
		const [frameType, ...rest] = frame;
		if (frameType === "AUTH" && typeof rest[0] === "string") {
			const challenge = rest[0];
			const authTags = [
				["relay", relayURL],
				["challenge", challenge],
			];
			if (authTagJSON) {
				try {
					authTags.push(JSON.parse(authTagJSON) as string[]);
				} catch {
					void 0;
				}
			}
			send(["AUTH", signEvent(22242, "", authTags)]);
			isAuthed = true;
			for (const [subscriptionID, subscription] of liveSubscriptions) {
				send(["REQ", subscriptionID, ...subscription.filters]);
			}
			return;
		}
		if (frameType === "EVENT" && typeof rest[0] === "string") {
			const subscriptionID = rest[0];
			const event = rest[1] as BuzzEvent;
			pendingQueries.get(subscriptionID)?.events.push(event);
			liveSubscriptions.get(subscriptionID)?.onEvent(event);
			return;
		}
		if (frameType === "EOSE" && typeof rest[0] === "string") {
			const query = pendingQueries.get(rest[0]);
			if (query) {
				pendingQueries.delete(rest[0]);
				send(["CLOSE", rest[0]]);
				query.resolve(query.events);
			}
			return;
		}
		if (frameType === "OK" && typeof rest[0] === "string") {
			const publishWaiter = pendingPublishes.get(rest[0]);
			if (!publishWaiter) return;
			pendingPublishes.delete(rest[0]);
			if (rest[1] === true) publishWaiter.resolve();
			else publishWaiter.reject(new Error(`relay rejected event: ${String(rest[2] ?? "")}`));
		}
	}

	async function waitForOpen(): Promise<void> {
		if (websocket?.readyState === WebSocket.OPEN) return;
		await new Promise<void>((resolve) => openWaiters.push(resolve));
	}

	return {
		pubkeyHex,
		async connect() {
			shouldReconnect = true;
			openSocket();
			await waitForOpen();
			await Bun.sleep(300);
			void isAuthed;
		},
		disconnect() {
			shouldReconnect = false;
			websocket?.close();
		},
		subscribe(filters, onEvent) {
			const subscriptionID = `live-${subscriptionSerial++}`;
			liveSubscriptions.set(subscriptionID, { filters, onEvent });
			if (websocket?.readyState === WebSocket.OPEN) {
				send(["REQ", subscriptionID, ...filters]);
			}
		},
		async query(filter, timeoutMs = 8_000) {
			await waitForOpen();
			const subscriptionID = `query-${subscriptionSerial++}`;
			return await new Promise<BuzzEvent[]>((resolve) => {
				const timeoutHandle = setTimeout(() => {
					const query = pendingQueries.get(subscriptionID);
					pendingQueries.delete(subscriptionID);
					send(["CLOSE", subscriptionID]);
					resolve(query?.events ?? []);
				}, timeoutMs);
				pendingQueries.set(subscriptionID, {
					events: [],
					resolve: (events) => {
						clearTimeout(timeoutHandle);
						resolve(events);
					},
				});
				send(["REQ", subscriptionID, filter]);
			});
		},
		async publish(kind, content, tags) {
			await waitForOpen();
			const event = signEvent(kind, content, tags);
			await new Promise<void>((resolve, reject) => {
				const timeoutHandle = setTimeout(() => {
					pendingPublishes.delete(event.id);
					reject(new Error("relay publish timed out"));
				}, 8_000);
				pendingPublishes.set(event.id, {
					resolve: () => {
						clearTimeout(timeoutHandle);
						resolve();
					},
					reject: (error) => {
						clearTimeout(timeoutHandle);
						reject(error);
					},
				});
				send(["EVENT", event]);
			});
			return event;
		},
	};
}

function hexToBytes(hex: string): Uint8Array {
	const bytes = new Uint8Array(hex.length / 2);
	for (let index = 0; index < bytes.length; index++) {
		bytes[index] = Number.parseInt(hex.slice(index * 2, index * 2 + 2), 16);
	}
	return bytes;
}
