// Breaks edit/delete/reaction echo loops. When the mirror applies an operation
// to a platform, that platform emits an inbound event for the mirror's own
// action; without suppression the mirror would republish it and bounce forever.
// Unlike create (guarded by the durable message mapping), these operations have
// no per-op mapping, so the mirror registers what it is about to cause and the
// inbound tap consumes that expectation once. Entries expire so a missed inbound
// event never suppresses a genuine later one.
export class EchoSuppressor {
	private readonly seen = new Map<string, number>();

	constructor(
		private readonly ttlMs = 30_000,
		private readonly now: () => number = () => Date.now(),
	) {}

	expect(parts: readonly (string | number | boolean)[]): void {
		this.prune();
		this.seen.set(JSON.stringify(parts), this.now() + this.ttlMs);
	}

	consume(parts: readonly (string | number | boolean)[]): boolean {
		const key = JSON.stringify(parts);
		const expiresAt = this.seen.get(key);
		if (expiresAt === undefined) return false;
		this.seen.delete(key);
		return expiresAt >= this.now();
	}

	private prune(): void {
		const current = this.now();
		for (const [key, expiresAt] of this.seen) {
			if (expiresAt < current) this.seen.delete(key);
		}
	}
}
