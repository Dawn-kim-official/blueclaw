import { BuzzAdapter } from "./adapter.ts";
import type { BuzzAdapterConfig } from "./types.ts";

export { BuzzAdapter } from "./adapter.ts";
export type { BuzzAdapterConfig, BuzzThreadId } from "./types.ts";

export function createBuzzAdapter(config: BuzzAdapterConfig): BuzzAdapter {
	if (!config.relayURL) throw new Error("buzz relayURL is required");
	if (!/^[0-9a-f]{64}$/.test(config.privateKeyHex)) {
		throw new Error("buzz privateKeyHex must be 64 hex characters");
	}
	return new BuzzAdapter(config);
}
